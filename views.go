package logkeeper

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/evergreen-ci/render"
	"github.com/gorilla/mux"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"go.mongodb.org/mongo-driver/mongo/bson"
	"go.mongodb.org/mongo-driver/mongo/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	maxLogChars = 4 * 1024 * 1024 // 4 MB

)

type Options struct {
	//Base URL to append to relative paths
	URL string

	// Maximum Request Size
	MaxRequestSize int
}

type logKeeper struct {
	render *render.Render
	opts   Options
}

type createdResponse struct {
	Id  string `json:"id,omitempty"`
	URI string `json:"uri"`
}

func earliestLogTime(logs []LogLine) *time.Time {
	var earliest *time.Time
	for _, v := range logs {
		if earliest == nil || v.Time().Before(*earliest) {
			t := v.Time()
			earliest = &t
		}
	}
	return earliest
}

func New(opts Options) *logKeeper {
	render := render.New(render.Options{
		Directory: "templates",
		Funcs: template.FuncMap{
			"MutableVar": func() interface{} {
				return &MutableVar{""}
			},
			"ColorSet": func() *ColorSet {
				return NewColorSet()
			},
			"DateFormat": func(when time.Time, layout string) string {
				return when.Format(layout)
			},
		},
	})

	return &logKeeper{render, opts}
}

type apiError struct {
	Err     string `json:"err"`
	MaxSize int    `json:"max_size,omitempty"`
	code    int
}

func (lk *logKeeper) createBuild(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := lk.checkContentLength(r); err != nil {
		lk.logErrorf(r, "content length limit exceeded for createBuild: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	buildParameters := struct {
		Builder  string `json:"builder"`
		BuildNum int    `json:"buildnum"`
		TaskId   string `json:"task_id"`
	}{}

	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &buildParameters); err != nil {
		lk.logErrorf(r, "Bad request to createBuild: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	existingBuild, err := findBuildByBuilder(buildParameters.Builder, buildParameters.BuildNum)
	if err != nil {
		lk.logErrorf(r, "Error finding build by builder: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}
	if existingBuild != nil {
		existingBuildUri := fmt.Sprintf("%v/build/%v", lk.opts.URL, existingBuild.Id)
		response := createdResponse{existingBuild.Id, existingBuildUri}
		lk.render.WriteJSON(w, http.StatusOK, response)
		return
	}

	buildInfo := map[string]interface{}{"task_id": buildParameters.TaskId}

	hasher := md5.New()
	if _, err = hasher.Write([]byte(primitive.NewObjectID().Hex())); err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	newBuildId := hex.EncodeToString(hasher.Sum(nil))
	newBuild := LogKeeperBuild{
		Id:       newBuildId,
		Builder:  buildParameters.Builder,
		BuildNum: buildParameters.BuildNum,
		Name:     fmt.Sprintf("%v #%v", buildParameters.Builder, buildParameters.BuildNum),
		Started:  time.Now(),
		Info:     buildInfo,
	}

	_, err = db.C("builds").InsertOne(db.Context(), newBuild)
	if err != nil {
		lk.logErrorf(r, "inserting build object: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	newBuildUri := fmt.Sprintf("%v/build/%v", lk.opts.URL, newBuildId)

	response := createdResponse{newBuildId, newBuildUri}
	lk.render.WriteJSON(w, http.StatusCreated, response)
}

func (lk *logKeeper) createTest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := lk.checkContentLength(r); err != nil {
		lk.logErrorf(r, "content length limit exceeded for createTest: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	vars := mux.Vars(r)
	buildId := vars["build_id"]

	build, err := findBuildById(buildId)
	if err != nil {
		lk.logErrorf(r, "error finding build: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "creating test: build not found"})
		return
	}

	testParams := struct {
		TestFilename string `json:"test_filename"`
		Command      string `json:"command"`
		Phase        string `json:"phase"`
		TaskId       string `json:"task_id"`
	}{}

	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &testParams); err != nil {
		lk.logErrorf(r, "Bad request to createTest: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	// create info
	testInfo := map[string]interface{}{"task_id": testParams.TaskId}

	newTest := Test{
		Id:        primitive.NewObjectID(),
		BuildId:   build.Id,
		BuildName: build.Name,
		Name:      testParams.TestFilename,
		Command:   testParams.Command,
		Started:   time.Now(),
		Phase:     testParams.Phase,
		Info:      testInfo,
	}

	_, err = db.C("tests").InsertOne(db.Context(), newTest)
	if err != nil {
		lk.logErrorf(r, "Error inserting test: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	testUri := fmt.Sprintf("%vbuild/%v/test/%v", lk.opts.URL, build.Id, newTest.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{newTest.Id.Hex(), testUri})
}

func (lk *logKeeper) appendLog(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := lk.checkContentLength(r); err != nil {
		lk.logWarningf(r, "content length limit exceeded for appendLog: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	vars := mux.Vars(r)
	buildId := vars["build_id"]

	build, err := findBuildById(buildId)
	if err != nil || build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "appending log: build not found"})
		return
	}

	test_id := vars["test_id"]
	test, err := findTest(test_id)
	if err != nil || test == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "test not found"})
		return
	}

	var info [][]interface{}
	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &info); err != nil {
		lk.logErrorf(r, "Bad request to appendLog: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	if len(info) == 0 {
		// no need to insert anything, so stop here
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	lineSets := make([][]LogLine, 1, len(info))
	lineSets[0] = make([]LogLine, 0, len(info))
	log := 0
	logChars := 0
	for _, v := range info {
		line := *NewLogLine(v)

		if len(line.Msg()) > maxLogChars {
			lk.render.WriteJSON(w, http.StatusBadRequest, "Log line exceeded 4MB")
			return
		}

		if len(line.Msg())+logChars > maxLogChars {
			log++
			lineSets = append(lineSets, make([]LogLine, 0, len(info)))
			logChars = 0
		}

		lineSets[log] = append(lineSets[log], line)
		logChars += len(line.Msg())
	}

	findOneResult := db.C("tests").FindOneAndUpdate(db.Context(), bson.M{"_id": test.Id}, bson.M{"$inc": bson.M{"seq": len(lineSets)}})
	if err := findOneResult.Err(); err != nil {
		lk.logErrorf(r, "updating tests: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}
	if err = findOneResult.Decode(test); err != nil {
		lk.logErrorf(r, "decoding updated test: %s", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	for i, lines := range lineSets {
		logEntry := Log{
			BuildId: build.Id,
			TestId:  &(test.Id),
			Seq:     test.Seq - len(lineSets) + i + 1,
			Lines:   lines,
			Started: earliestLogTime(lines),
		}
		_, err := db.C("logs").InsertOne(db.Context(), logEntry)
		if err != nil {
			lk.logErrorf(r, "Error inserting logs entry: %v", err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
			return
		}
	}

	testUrl := fmt.Sprintf("%vbuild/%v/test/%v", lk.opts.URL, build.Id, test.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

func (lk *logKeeper) appendGlobalLog(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := lk.checkContentLength(r); err != nil {
		lk.logWarningf(r, "content length limit exceeded for appendGlobalLog: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	vars := mux.Vars(r)
	buildId := vars["build_id"]

	build, err := findBuildById(buildId)
	if err != nil {
		lk.logErrorf(r, "finding builds entry: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding builds in append global log:" + err.Error()})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "append global log: build not found"})
		return
	}

	var info [][]interface{}
	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &info); err != nil {
		lk.logErrorf(r, "Bad request to appendGlobalLog: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	if len(info) == 0 {
		// no need to insert anything, so stop here
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	lineSets := make([][]LogLine, 1, len(info))
	lineSets[0] = make([]LogLine, 0, len(info))
	log := 0
	logChars := 0
	for _, v := range info {
		line := *NewLogLine(v)

		if len(line.Msg()) > maxLogChars {
			lk.render.WriteJSON(w, http.StatusBadRequest, "Log line exceeded 4MB")
			return
		}

		if len(line.Msg())+logChars > maxLogChars {
			log++
			lineSets = append(lineSets, make([]LogLine, 0, len(info)))
			logChars = 0
		}

		lineSets[log] = append(lineSets[log], line)
		logChars += len(line.Msg())
	}

	findOneResult := db.C("builds").FindOneAndUpdate(db.Context(), bson.M{"_id": build.Id}, bson.M{"$inc": bson.M{"seq": len(lineSets)}})
	if err := findOneResult.Err(); err != nil {
		lk.logErrorf(r, "updating builds entry: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}
	if err = findOneResult.Decode(build); err != nil {
		lk.logErrorf(r, "decoding updated build: %s", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	for i, lines := range lineSets {
		logEntry := Log{
			BuildId: build.Id,
			TestId:  nil,
			Seq:     build.Seq - len(lineSets) + i + 1,
			Lines:   lines,
			Started: earliestLogTime(lines),
		}
		_, err = db.C("logs").InsertOne(db.Context(), logEntry)
		if err != nil {
			lk.logErrorf(r, "inserting logs entry: %v", err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
			return
		}
	}

	testUrl := fmt.Sprintf("%vbuild/%v/", lk.opts.URL, build.Id)
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

func (lk *logKeeper) viewBuildById(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildId := vars["build_id"]

	build, err := findBuildById(buildId)
	if err != nil {
		lk.logErrorf(r, "Error finding build: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "failed to find build:" + err.Error()})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "view build: build not found"})
		return
	}
	tests, err := findTestsForBuild(buildId)
	if err != nil {
		lk.logErrorf(r, "Error finding tests for build: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	lk.render.WriteHTML(w, http.StatusOK, struct {
		Build *LogKeeperBuild
		Tests []Test
	}{build, tests}, "base", "build.html")
}

func (lk *logKeeper) viewAllLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildId := vars["build_id"]

	build, err := findBuildById(buildId)
	if err != nil || build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "view all logs: build not found"})
		return
	}

	globalLogs := lk.findLogs(ctx, bson.M{"build_id": build.Id, "test_id": nil}, []string{"seq"}, nil, nil)
	testLogs := lk.findLogs(ctx, bson.M{"build_id": build.Id, "test_id": bson.M{"$ne": nil}}, []string{"build_id", "started"}, nil, nil)
	merged := MergeLog(testLogs, globalLogs)

	if len(r.FormValue("raw")) > 0 || r.Header.Get("Accept") == "text/plain" {
		for line := range merged {
			_, err = w.Write([]byte(line.Data + "\n"))
			if err != nil {
				return
			}
		}
		return
	} else if len(r.FormValue("html")) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/all", buildId), http.StatusFound)
		return
	} else {
		err = lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *LogLineItem
			BuildId  string
			Builder  string
			TestId   string
			TestName string
			Info     map[string]interface{}
		}{merged, build.Id, build.Builder, "", "All logs", build.Info}, "base", "test.html")
		if err != nil {
			lk.logErrorf(r, "Error rendering template: %v", err)
		}

	}
}

func (lk *logKeeper) viewTestByBuildIdTestId(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")

	ctx := r.Context()
	defer r.Body.Close()

	vars := mux.Vars(r)
	build_id := vars["build_id"]

	build, err := findBuildById(build_id)
	if err != nil || build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "view test by id: build not found"})
		return
	}

	test_id := vars["test_id"]
	test, err := findTest(test_id)
	if err != nil || test == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "test not found"})
		return
	}

	globalLogs, err := lk.findGlobalLogsDuringTest(ctx, build, test)

	if err != nil {
		lk.logErrorf(r, "Error finding global logs during test: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	testLogs := lk.findLogs(ctx, bson.M{"build_id": build.Id, "test_id": test.Id}, []string{"seq"}, nil, nil)

	merged := MergeLog(testLogs, globalLogs)

	if len(r.FormValue("raw")) > 0 || r.Header.Get("Accept") == "text/plain" {
		emptyLog := true
		for line := range merged {
			emptyLog = false
			_, err = w.Write([]byte(line.Data + "\n"))
			if err != nil {
				lk.render.WriteJSON(w, http.StatusInternalServerError,
					apiError{Err: err.Error()})
				return
			}
		}
		if emptyLog {
			lk.render.WriteJSON(w, http.StatusOK, nil)
		}
	} else if len(r.FormValue("html")) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/test/%s", build_id, test_id), http.StatusFound)
		return
	} else {
		err = lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *LogLineItem
			BuildId  string
			Builder  string
			TestId   string
			TestName string
			Info     map[string]interface{}
		}{merged, build.Id, build.Builder, test.Id.Hex(), test.Name, test.Info}, "base", "test.html")
		// If there was an error, it won't show up in the UI since it's being streamed, so log it here
		// instead
		if err != nil {
			lk.logErrorf(r, "Error rendering template: %v", err)
		}
	}
}

func (lk *logKeeper) viewInLobster(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	err := lk.render.StreamHTML(w, http.StatusOK, nil, "base", "lobster/build/index.html")
	if err != nil {
		lk.logErrorf(r, "Error rendering template: %v", err)
	}
}

func (lk *logKeeper) findLogs(ctx context.Context, query bson.M, sort []string, minTime, maxTime *time.Time) chan *LogLineItem {
	outputLog := make(chan *LogLineItem)
	go func() {
		defer close(outputLog)
		lineNum := 0
		cur, err := db.C("logs").Find(db.Context(), query, options.Find().SetSort(sort))
		if err != nil {
			return
		}

		for cur.Next(ctx) {
			var logItem Log
			if err = cur.Decode(logItem); err != nil {
				continue
			}

			for _, v := range logItem.Lines {
				if minTime != nil && v.Time().Before(*minTime) {
					continue
				}
				if maxTime != nil && v.Time().After(*maxTime) {
					continue
				}
				outputLog <- &LogLineItem{
					LineNum:   lineNum,
					Timestamp: v.Time(),
					Data:      v.Msg(),
					TestId:    logItem.TestId,
				}
				lineNum++
			}
		}
	}()
	return outputLog
}

func (lk *logKeeper) findGlobalLogsDuringTest(ctx context.Context, build *LogKeeperBuild, test *Test) (chan *LogLineItem, error) {
	globalSeqFirst, globalSeqLast := new(int), new(int)

	minTime := &(test.Started)
	var maxTime *time.Time

	// Find the first global log entry before this test started.
	// This may not actually contain any global log lines during the test run, if the entry returned
	// by this query comes from after the *next* test stared.
	var firstGlobalLog Log
	err := db.C("logs").
		FindOne(db.Context(), bson.M{"build_id": build.Id, "test_id": nil, "started": bson.M{"$lt": test.Started}}, options.FindOne().SetSort("-seq")).
		Decode(&firstGlobalLog)
	if err != nil {
		if err != mongo.ErrNoDocuments {
			return nil, err
		}
		// There are no global entries after this test started.
		globalSeqFirst = nil
	} else {
		*globalSeqFirst = firstGlobalLog.Seq
	}

	// Find the next test after this one.
	var nextTest Test
	err = db.C("tests").
		FindOne(db.Context(), bson.M{"build_id": build.Id, "started": bson.M{"$gt": test.Started}}, options.FindOne().SetSort("started")).
		Decode(&nextTest)
	if err != nil {
		if err != mongo.ErrNoDocuments {
			return nil, nil
		}
		// no next test exists
		globalSeqLast = nil
	} else {
		maxTime = &(nextTest.Started)
		// Find the last global log entry that covers this test. This may return a global log entry
		// that started before the test itself.
		var lastGlobalLog Log
		err = db.C("logs").
			FindOne(db.Context(), bson.M{"build_id": build.Id, "test_id": nil, "started": bson.M{"$lt": nextTest.Started}}, options.FindOne().SetSort("-seq")).
			Decode(&lastGlobalLog)
		if err != nil {
			if err != mongo.ErrNoDocuments {
				return nil, nil
			}
			globalSeqLast = nil
		} else {
			*globalSeqLast = lastGlobalLog.Seq
		}
	}

	var globalLogsSeq bson.M
	if globalSeqFirst == nil {
		globalLogsSeq = bson.M{"$gte": test.Seq}
	} else {
		globalLogsSeq = bson.M{"$gte": *globalSeqFirst}
	}
	if globalSeqLast != nil {
		globalLogsSeq["$lte"] = *globalSeqLast
	}

	return lk.findLogs(ctx, bson.M{"build_id": build.Id, "test_id": nil, "seq": globalLogsSeq}, []string{"seq"}, minTime, maxTime), nil
}

func (lk *logKeeper) logErrorf(r *http.Request, format string, v ...interface{}) {
	err := fmt.Sprintf(format, v...)
	grip.Error(message.Fields{
		"request": GetCtxRequestId(r),
		"error":   err,
	})
}

func (lk *logKeeper) logWarningf(r *http.Request, format string, v ...interface{}) {
	err := fmt.Sprintf(format, v...)
	grip.Warning(message.Fields{
		"request": GetCtxRequestId(r),
		"error":   err,
	})
}

func (lk *logKeeper) checkAppHealth(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	resp := struct {
		Err             string           `json:"err"`
		MaxRequestSize  int              `json:"maxRequestSize"`
		DB              bool             `json:"db"`
		Build           string           `json:"build_id"`
		BatchSize       int              `json:"batch_size"`
		NumWorkers      int              `json:"workers"`
		DurationSeconds float64          `json:"dur_secs"`
		MigrationStatus amboy.QueueStats `json:"migration_queue_stats"`
	}{
		Build:           BuildRevision,
		MaxRequestSize:  lk.opts.MaxRequestSize,
		BatchSize:       CleanupBatchSize,
		NumWorkers:      AmboyWorkers,
		DurationSeconds: AmboyInterval.Seconds(),
		MigrationStatus: db.GetMigrationQueue().Stats(),
	}

	if err := db.Client().Ping(r.Context(), nil); err != nil {
		resp.Err = err.Error()

		lk.render.WriteJSON(w, http.StatusServiceUnavailable, &resp)
		return
	}

	resp.DB = true
	lk.render.WriteJSON(w, http.StatusOK, &resp)
}

func (lk *logKeeper) NewRouter() http.Handler {
	r := mux.NewRouter().StrictSlash(false)

	//write methods
	r.Path("/build/").Methods("POST").HandlerFunc(lk.createBuild)
	r.Path("/build").Methods("POST").HandlerFunc(lk.createBuild)
	r.Path("/build/{build_id}/test/").Methods("POST").HandlerFunc(lk.createTest)
	r.Path("/build/{build_id}/test").Methods("POST").HandlerFunc(lk.createTest)
	r.Path("/build/{build_id}/test/{test_id}/").Methods("POST").HandlerFunc(lk.appendLog)
	r.Path("/build/{build_id}/test/{test_id}").Methods("POST").HandlerFunc(lk.appendLog)
	r.Path("/build/{build_id}/").Methods("POST").HandlerFunc(lk.appendGlobalLog)
	r.Path("/build/{build_id}").Methods("POST").HandlerFunc(lk.appendGlobalLog)

	//read methods
	r.StrictSlash(true).Path("/build/{build_id}").Methods("GET").HandlerFunc(lk.viewBuildById)
	r.StrictSlash(true).Path("/build/{build_id}/all").Methods("GET").HandlerFunc(lk.viewAllLogs)
	r.StrictSlash(true).Path("/build/{build_id}/test/{test_id}").Methods("GET").HandlerFunc(lk.viewTestByBuildIdTestId)
	r.PathPrefix("/lobster").Methods("GET").HandlerFunc(lk.viewInLobster)
	//r.Path("/{builder}/builds/{buildnum:[0-9]+}/").HandlerFunc(viewBuild)
	//r.Path("/{builder}/builds/{buildnum}/test/{test_phase}/{test_name}").HandlerFunc(app.MakeHandler(Name("view_test")))
	r.Path("/status").Methods("GET").HandlerFunc(lk.checkAppHealth)

	return r
}
