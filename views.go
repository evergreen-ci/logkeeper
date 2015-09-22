package logkeeper

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/evergreen-ci/render"
	"github.com/gorilla/mux"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"regexp"
	"strconv"
	"strings"
)

const maxLogChars int = 4 * 1024 * 1024 // 4 MB

type Options struct {
	// Name of DB in mongod to use for reading/writing log data
	DB string

	//Base URL to append to relative paths
	URL string
}

type logKeeper struct {
	session *mgo.Session
	render  *render.Render
	opts    Options
}

type createdResponse struct {
	Id  string `json:"id,omitempty"`
	URI string `json:"uri"`
}

type createBuild struct {
	Builder  string `json:"builder"`
	BuildNum int    `json:"buildnum"`
}

func earliestLogTime(logs []LogLine) *time.Time {
	var earliest *time.Time
	earliest = nil
	for _, v := range logs {
		if earliest == nil || v.Time().Before(*earliest) {
			t := v.Time()
			earliest = &t
		}
	}
	return earliest
}

func New(session *mgo.Session, opts Options) *logKeeper {
	if session == nil {
		panic("session must not be nil")
	}
	session.SetSocketTimeout(0)

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

	// Set default values for options
	if opts.DB == "" {
		opts.DB = "logkeeper"
	}

	return &logKeeper{session, render, opts}
}

func (lk *logKeeper) getSession() (*mgo.Session, *mgo.Database) {
	session := lk.session.Copy()

	return session, session.DB(lk.opts.DB)
}

type apiError struct {
	Err string `json:"err"`
}

func (lk *logKeeper) createBuild(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)

	info := createBuild{}
	err := decoder.Decode(&info)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}

	ses, db := lk.getSession()
	defer ses.Close()

	existingBuild, err := findBuildByBuilder(db, info.Builder, info.BuildNum)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}
	if existingBuild != nil {
		existingBuildUri := fmt.Sprintf("%v/build/%v", lk.opts.URL, existingBuild.Id.Hex())
		response := createdResponse{existingBuild.Id.Hex(), existingBuildUri}
		lk.render.WriteJSON(w, http.StatusOK, response)
		return
	}

	newBuild := LogKeeperBuild{
		Id:       bson.NewObjectId(),
		Builder:  info.Builder,
		BuildNum: info.BuildNum,
		Name:     fmt.Sprintf("%v #%v", info.Builder, info.BuildNum),
		Started:  time.Now(),
	}

	err = db.C("builds").Insert(newBuild)

	if err != nil {
		fmt.Println("Error inserting build object:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	newBuildUri := fmt.Sprintf("%v/build/%v", lk.opts.URL, newBuild.Id.Hex())

	response := createdResponse{newBuild.Id.Hex(), newBuildUri}
	lk.render.WriteJSON(w, http.StatusCreated, response)
}

func (lk *logKeeper) createTest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]

	ses, db := lk.getSession()
	defer ses.Close()

	build, err := findBuildById(db, buildId)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"creating test: build not found"})
		return
	}

	decoder := json.NewDecoder(r.Body)
	info := struct {
		TestFilename string `json:"test_filename"`
		Command      string `json:"command"`
		Phase        string `json:"phase"`
	}{}

	err = decoder.Decode(&info)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}

	newTest := Test{
		Id:        bson.NewObjectId(),
		BuildId:   build.Id,
		BuildName: build.Name,
		Name:      info.TestFilename,
		Command:   info.Command,
		Started:   time.Now(),
		Phase:     info.Phase,
	}

	err = db.C("tests").Insert(newTest)

	if err != nil {
		fmt.Println("Error inserting test:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	testUri := fmt.Sprintf("%vbuild/%v/test/%v", lk.opts.URL, build.Id.Hex(), newTest.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{newTest.Id.Hex(), testUri})
}

func (lk *logKeeper) appendLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]
	ses, db := lk.getSession()
	defer ses.Close()

	build, err := findBuildById(db, buildId)
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"appending log: build not found"})
		return
	}

	test_id := vars["test_id"]
	test, err := findTest(db, test_id)
	if err != nil || test == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"test not found"})
		return
	}

	var info [][]interface{}
	decoder := json.NewDecoder(r.Body)
	err = decoder.Decode(&info)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
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

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": len(lineSets)}}, ReturnNew: true}
	_, err = db.C("tests").With(ses).Find(bson.M{"_id": test.Id}).Apply(change, test)

	if err != nil {
		fmt.Println("Error updating tests:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
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
		err = db.C("logs").With(ses).Insert(logEntry)
		if err != nil {
			fmt.Println("Error inserting logs entry:", err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
			return
		}
	}

	testUrl := fmt.Sprintf("%vbuild/%v/test/%v", lk.opts.URL, build.Id.Hex(), test.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

func (lk *logKeeper) appendGlobalLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]

	ses, db := lk.getSession()
	defer ses.Close()

	build, err := findBuildById(db, buildId)
	if err != nil {
		fmt.Println("Error finding builds entry:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{"finding builds in append global log:" + err.Error()})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"append global log: build not found"})
		return
	}

	var info [][]interface{}
	decoder := json.NewDecoder(r.Body)
	err = decoder.Decode(&info)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
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

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": len(lineSets)}}, ReturnNew: true}
	_, err = db.C("builds").With(ses).Find(bson.M{"_id": build.Id}).Apply(change, build)
	if err != nil {
		fmt.Println("Error updating builds entry:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
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
		err = db.C("logs").With(ses).Insert(logEntry)
		if err != nil {
			fmt.Println("Error inserting logs entry:", err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
			return
		}
	}

	testUrl := fmt.Sprintf("%vbuild/%v/", lk.opts.URL, build.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

func (lk *logKeeper) search(w http.ResponseWriter, r *http.Request) {
	text := strings.Join(r.URL.Query()["text"], " ")

	isExactMatch := false
	if len(r.URL.Query()["exact"]) == 1 {
		isExactMatch = (r.URL.Query()["exact"][0] == "true")
	}

	var highlightTerms []string
	if isExactMatch {
		highlightTerms = []string{regexp.QuoteMeta(text)}
	} else {
		highlightTerms = strings.Fields(text)
		for i, term := range highlightTerms {
			highlightTerms[i] = regexp.QuoteMeta(term)
		}
	}

	skip := 0
	if getSkip, err := strconv.Atoi(strings.Join(r.URL.Query()["skip"], "")); err == nil {
		skip = getSkip
	}

	limit := 20

	ses, db := lk.getSession()
	defer ses.Close()

	searchResults, err := lk.getTextSearchDisplayResults(text, isExactMatch, skip, limit)
	if err != nil {
		fmt.Println("Error finding text search results:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	total := findTotalTextSearchResults(db, text, isExactMatch)

	lk.render.WriteHTML(w, http.StatusOK, struct {
		Text           string
		Exact          bool
		HighlightTerms []string
		Results        []TextSearchDisplayResult
		Start          int
		End            int
		PrevSkip       int
		Total          int
	}{text,
		isExactMatch,
		highlightTerms,
		searchResults,
		skip + 1,
		skip + len(searchResults),
		skip - limit,
		total,
	}, "search", "search.html")
}

func (lk *logKeeper) viewBuildById(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]

	ses, db := lk.getSession()
	defer ses.Close()

	build, err := findBuildById(db, buildId)
	if err != nil {
		fmt.Println("Error finding build:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{"failed to find build:" + err.Error()})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"view build: build not found"})
		return
	}
	tests, err := findTestsForBuild(db, buildId)
	if err != nil {
		fmt.Println("Error finding tests for build:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	lk.render.WriteHTML(w, http.StatusOK, struct {
		Build *LogKeeperBuild
		Tests []Test
	}{build, tests}, "base", "build.html")
}

func (lk *logKeeper) viewAllLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]

	ses, db := lk.getSession()
	defer ses.Close()

	build, err := findBuildById(db, buildId)
	if err != nil && build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"view all logs: build not found"})
		return
	}

	globalLogs := lk.findLogs(bson.M{"build_id": build.Id, "test_id": nil}, []string{"seq"}, nil, nil)
	testLogs := lk.findLogs(bson.M{"build_id": build.Id, "test_id": bson.M{"$ne": nil}}, []string{"build_id", "started"}, nil, nil)
	merged := MergeLog(testLogs, globalLogs)

	if len(r.FormValue("raw")) > 0 {
		for line := range merged {
			w.Write([]byte(line.Data + "\n"))
		}
		return
	} else {
		err = lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *LogLineItem
			BuildId  string
			Builder  string
			TestId   string
			TestName string
		}{merged, build.Id.Hex(), build.Builder, "", "All logs"}, "base", "test.html")
	}
}

func (lk *logKeeper) viewTestByBuildIdTestId(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	build_id := vars["build_id"]

	ses, db := lk.getSession()
	defer ses.Close()

	build, err := findBuildById(db, build_id)
	if err != nil || build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"view test by id: build not found"})
		return
	}

	test_id := vars["test_id"]
	test, err := findTest(db, test_id)
	if err != nil || test == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"test not found"})
		return
	}
	globalLogs, err := lk.findGlobalLogsDuringTest(build, test)

	if err != nil {
		fmt.Println("Error finding global logs during test:", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	testLogs := lk.findLogs(bson.M{"build_id": build.Id, "test_id": test.Id}, []string{"seq"}, nil, nil)

	merged := MergeLog(testLogs, globalLogs)

	if len(r.FormValue("raw")) > 0 {
		for line := range merged {
			w.Write([]byte(line.Data + "\n"))
		}
		return
	} else {
		err = lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *LogLineItem
			BuildId  string
			Builder  string
			TestId   string
			TestName string
		}{merged, build.Id.Hex(), build.Builder, test.Id.Hex(), test.Name}, "base", "test.html")
		// If there was an error, it won't show up in the UI since it's being streamed, so log it here
		// instead
		if err != nil {
			fmt.Println(err)
		}
	}
}

func (lk *logKeeper) getTextSearchDisplayResults(text string, isExactMatch bool, skip int, limit int) ([]TextSearchDisplayResult, error) {
	ses, db := lk.getSession()
	defer ses.Close()

	results, err := findTextSearchQueryResults(db, text, isExactMatch, skip, limit)
	if err != nil {
		return nil, err
	}

	buildIds := make([]bson.ObjectId, 0, limit)
	testIds := make([]bson.ObjectId, 0, limit)
	for _, item := range results {
		buildIds = append(buildIds, item.BuildId)
		if item.TestId != nil {
			testIds = append(testIds, *item.TestId)
		}
	}
	buildNames := findBuildNames(db, buildIds)
	testNames := findTestNames(db, testIds)

	searchResults := make([]TextSearchDisplayResult, 0, limit)
	for _, item := range results {
		testName := ""
		if item.TestId != nil {
			testName = testNames[*item.TestId]
		}
		searchResults = append(searchResults, TextSearchDisplayResult{
			BuildName: buildNames[item.BuildId],
			BuildId:   item.BuildId,
			TestName:  testName,
			TestId:    item.TestId,
			HasTestId: item.TestId != nil,
			Count:     item.Count,
			Time:      item.Line.Time(),
			Data:      item.Line.Msg(),
		})
	}
	return searchResults, nil
}

func (lk *logKeeper) findLogs(query bson.M, sort string, minTime, maxTime *time.Time) chan *LogLineItem {
	ses, db := lk.getSession()

	outputLog := make(chan *LogLineItem)
	logItem := &Log{}

	go func() {
		defer ses.Close()
		defer close(outputLog)
		lineNum := 0
		log := db.C("logs").Find(query).Sort(sort...).Iter()
		for log.Next(logItem) {
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

func (lk *logKeeper) findGlobalLogsDuringTest(build *LogKeeperBuild, test *Test) (chan *LogLineItem, error) {
	ses, db := lk.getSession()
	defer ses.Close()

	globalSeqFirst, globalSeqLast := new(int), new(int)

	minTime := &(test.Started)
	var maxTime *time.Time

	// Find the first global log entry after this test started.
	// This may not actually contain any global log lines during the test run, if the entry returned
	// by this query comes from after the *next* test stared.
	firstGlobalLog := &Log{}
	err := db.C("logs").Find(bson.M{"build_id": build.Id, "test_id": nil, "started": bson.M{"$lt": test.Started}}).Sort("-seq").Limit(1).One(firstGlobalLog)
	if err != nil {
		if err != mgo.ErrNotFound {
			return nil, err
		}
		// There are no global entries after this test started.
		globalSeqFirst = nil
	} else {
		*globalSeqFirst = firstGlobalLog.Seq
	}

	lastGlobalLog := &Log{}
	// Find the next test after this one.
	nextTest := &Test{}
	err = db.C("tests").Find(bson.M{"build_id": build.Id, "started": bson.M{"$gt": test.Started}}).Sort("started").Limit(1).One(nextTest)
	if err != nil {
		if err != mgo.ErrNotFound {
			return nil, err
		}
		// no next test exists
		globalSeqLast = nil
	} else {
		maxTime = &(nextTest.Started)
		// Find the last global log entry that covers this test. This may return a global log entry
		// that started before the test itself.
		err = db.C("logs").Find(bson.M{"build_id": build.Id, "test_id": nil, "started": bson.M{"$lt": nextTest.Started}}).Sort("-seq").Limit(1).One(lastGlobalLog)
		if err != nil {
			if err != mgo.ErrNotFound {
				return nil, err
			}
			globalSeqLast = nil
		} else {
			*globalSeqLast = lastGlobalLog.Seq
		}
	}

	if globalSeqFirst == nil {
		return emptyChannel(), nil
	}

	globalLogsSeq := bson.M{"$gte": *globalSeqFirst}
	if globalSeqLast != nil {
		globalLogsSeq["$lte"] = *globalSeqLast
	}

	return lk.findLogs(bson.M{"build_id": build.Id, "test_id": nil, "seq": globalLogsSeq}, []string{"seq"}, minTime, maxTime), nil
}

func emptyChannel() chan *LogLineItem {
	ch := make(chan *LogLineItem)
	close(ch)
	return ch
}

/*
func CreateBuild(ae web.HandlerApp, r *http.Request) web.HTTPResponse {

	decoder := json.NewDecoder(r.Body)
	info := make(map[string]interface{})
	err := decoder.Decode(&info)
	if err != nil {
		return web.JSONResponse{map[string]string{"err": "Bad Request"}, http.StatusBadRequest}
	}
	builder, ok1 := info["builder"]
	buildnum, ok2 := info["buildnum"]
	_ = builder
	if !ok1 || !ok2 {
		return web.JSONResponse{map[string]string{"err": "Fields \"builder\" and \"buildnum\" are required"}, http.StatusBadRequest}
	}

	var buildnumInt int

	switch buildnum.(type) {
	case int:
		buildnumInt = buildnum.(int)
	case int64:
		buildnumInt = int(buildnum.(int64))
	case float64:
		buildnumInt = int(buildnum.(float64))
	default:
		return web.JSONResponse{map[string]string{"err": "Field \"buildnum\" must be an integer"}, http.StatusBadRequest}
	}

	delete(info, "builder")
	delete(info, "buildnum")

	var buildId bson.ObjectId
	build, err := FindBuildByBuildNum(builder.(string), buildnumInt)
	if err != nil {
		mci.LOGGER.Logf(slogger.ERROR, "Error occurred finding build by build num: %v", err)
		return web.JSONResponse{map[string]string{"err": err.Error()}, http.StatusInternalServerError}
	}
	if build != nil {
		buildId = build.Id
	} else {
		newBuild := LogKeeperBuild{Builder: builder.(string), BuildNum: int(buildnum.(float64)), Started: time.Now(), Name: fmt.Sprintf("%v #%v", builder, buildnum), Info: info, Phases: []string{}}
		err = newBuild.Insert()
		if err != nil {
			if mgo.IsDup(err) {
				return web.JSONResponse{map[string]string{"err": err.Error()}, http.StatusConflict}
			} else {
				mci.LOGGER.Logf(slogger.ERROR, "Error occurred inserting build: %v", err)
				return web.JSONResponse{map[string]string{"err": err.Error()}, http.StatusInternalServerError}
			}
		}
		buildId = newBuild.Id
		mci.LOGGER.Logf(slogger.ERROR, "build is inserted,  now %v", buildId)
	}

	return web.JSONResponse{
		map[string]interface{}{
			"err": nil,
			"id":  buildId.Hex(),
			"uri": BUILDLOG_URL_ROOT + "/build/" + buildId.Hex(),
		}, http.StatusCreated}
}
*/

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
	r.StrictSlash(true).Path("/").Methods("GET").HandlerFunc(lk.search)
	r.StrictSlash(true).Path("/build/{build_id}").Methods("GET").HandlerFunc(lk.viewBuildById)
	r.StrictSlash(true).Path("/build/{build_id}/all").Methods("GET").HandlerFunc(lk.viewAllLogs)
	r.StrictSlash(true).Path("/build/{build_id}/test/{test_id}").Methods("GET").HandlerFunc(lk.viewTestByBuildIdTestId)
	//r.Path("/{builder}/builds/{buildnum:[0-9]+}/").HandlerFunc(viewBuild)
	//r.Path("/{builder}/builds/{buildnum}/test/{test_phase}/{test_name}").HandlerFunc(app.MakeHandler(Name("view_test")))
	return r
}
