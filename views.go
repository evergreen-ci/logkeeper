package logkeeper

import (
	"encoding/json"
	"fmt"
	"github.com/evergreen-ci/render"
	"github.com/gorilla/mux"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"html/template"
	"net/http"
	"time"
)

type Options struct {
	// Name of DB in mongod to use for reading/writing log data
	DB string

	//Base URL to append to relative paths
	URL string
}

type logKeeper struct {
	db     *mgo.Database
	render *render.Render
	opts   Options
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
	db := session.DB(opts.DB)

	return &logKeeper{db, render, opts}
}

type apiError struct {
	err string `json:"err"`
}

func (lk *logKeeper) createBuild(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)

	info := createBuild{}
	err := decoder.Decode(&info)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}

	existingBuild, err := findBuildByBuilder(lk.db, info.Builder, info.BuildNum)
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

	err = lk.db.C("builds").Insert(newBuild)

	if err != nil {
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
	build, err := findBuildById(lk.db, buildId)
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"build not found"})
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

	err = lk.db.C("tests").Insert(newTest)

	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	testUri := fmt.Sprintf("%vbuild/%v/test/%v", lk.opts.URL, build.Id.Hex(), newTest.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{newTest.Id.Hex(), testUri})
}

func (lk *logKeeper) appendLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]
	build, err := findBuildById(lk.db, buildId)
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"build not found"})
		return
	}

	test_id := vars["test_id"]
	test, err := findTest(lk.db, test_id)
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

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": 1}}, ReturnNew: true}
	_, err = lk.db.C("tests").Find(bson.M{"_id": test.Id}).Apply(change, test)

	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	lines := make([]LogLine, 0, len(info))
	for _, v := range info {
		lines = append(lines, *NewLogLine(v))
	}

	if len(lines) == 0 {
		// no need to insert anything, so stop here
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	logEntry := Log{
		BuildId: build.Id,
		TestId:  &(test.Id),
		Seq:     test.Seq,
		Lines:   lines,
		Started: earliestLogTime(lines),
	}
	err = lk.db.C("logs").Insert(logEntry)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	testUrl := fmt.Sprintf("%vbuild/%v/test/%v", lk.opts.URL, build.Id.Hex(), test.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

func (lk *logKeeper) appendGlobalLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]
	build, err := findBuildById(lk.db, buildId)
	if err != nil && build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"build not found"})
		return
	}

	var info [][]interface{}
	decoder := json.NewDecoder(r.Body)
	err = decoder.Decode(&info)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{err.Error()})
		return
	}

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": 1}}, ReturnNew: true}
	_, err = lk.db.C("builds").Find(bson.M{"_id": build.Id}).Apply(change, build)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	lines := make([]LogLine, 0, len(info))
	for _, v := range info {
		lines = append(lines, *NewLogLine(v))
	}

	if len(lines) == 0 {
		// no need to insert anything, so stop here
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	logEntry := Log{
		BuildId: build.Id,
		TestId:  nil,
		Seq:     build.Seq,
		Lines:   lines,
		Started: earliestLogTime(lines),
	}
	err = lk.db.C("logs").Insert(logEntry)
	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	testUrl := fmt.Sprintf("%vbuild/%v/", lk.opts.URL, build.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

func (lk *logKeeper) viewBuildById(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildId := vars["build_id"]
	build, err := findBuildById(lk.db, buildId)
	if err != nil && build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"build not found"})
		return
	}
	tests, err := findTestsForBuild(lk.db, buildId)
	if err != nil {
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
	build, err := findBuildById(lk.db, buildId)
	if err != nil && build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"build not found"})
		return
	}

	globalLogs := lk.findLogs(bson.M{"build_id": build.Id, "test_id": nil}, "seq")
	testLogs := lk.findLogs(bson.M{"build_id": build.Id, "test_id": bson.M{"$ne": nil}}, "seq")
	merged := MergeLog(testLogs, globalLogs)

	if len(r.FormValue("raw")) > 0 {
		for line := range merged {
			w.Write([]byte(line.Data + "\n"))
		}
		return
	} else {
		err = lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *LogLineItem
		}{merged}, "base", "test.html")
	}
}

func (lk *logKeeper) viewTestByBuildIdTestId(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	build_id := vars["build_id"]
	build, err := findBuildById(lk.db, build_id)
	if err != nil || build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"build not found"})
		return
	}

	test_id := vars["test_id"]
	test, err := findTest(lk.db, test_id)
	if err != nil || test == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{"test not found"})
		return
	}
	globalLogs, err := lk.findGlobalLogsDuringTest(build, test)

	if err != nil {
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{err.Error()})
		return
	}

	testLogs := lk.findLogs(bson.M{"build_id": build.Id, "test_id": test.Id}, "seq")

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

func (lk *logKeeper) findLogs(query bson.M, sort string) chan *LogLineItem {
	ses := lk.db.Session.Copy()
	outputLog := make(chan *LogLineItem)
	logItem := &Log{}
	go func() {
		defer ses.Close()
		defer close(outputLog)
		lineNum := 0
		log := lk.db.With(ses).C("logs").Find(query).Sort(sort).Iter()
		for log.Next(logItem) {
			for _, v := range logItem.Lines {
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
	ses := lk.db.Session.Copy()
	defer ses.Close()

	globalLogRange := bson.M{}
	//lowerBound, upperBound := -1, -1
	// Find the first global log entry associated with the test
	log := &Log{}
	err := lk.db.With(ses).C("logs").Find(bson.M{"build_id": build.Id, "test_id": nil, "started": bson.M{"$gte": test.Started}}).Sort("seq").One(log)
	if err != nil {
		if err != mgo.ErrNotFound {
			return nil, err
		}
		globalLogRange["$gte"] = -1
	} else {
		globalLogRange["$gte"] = log.Seq
	}

	// Find the last global log entry associated with the test
	// First, find the test after this one.
	nextTest := &Test{}
	err = lk.db.With(ses).C("tests").Find(bson.M{"build_id": build.Id, "started": bson.M{"$gt": test.Started}}).Sort("started").One(nextTest)
	if err != nil {
		if err != mgo.ErrNotFound {
			return nil, err
		}
	} else {
		globalLogRange["$lt"] = nextTest.Seq
	}

	//Now find the actual logs within that range.
	return lk.findLogs(bson.M{"build_id": build.Id, "test_id": nil, "seq": globalLogRange}, "seq"), nil
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
	r.StrictSlash(true).Path("/build/{build_id}").Methods("GET").HandlerFunc(lk.viewBuildById)
	r.StrictSlash(true).Path("/build/{build_id}/all").Methods("GET").HandlerFunc(lk.viewAllLogs)
	r.StrictSlash(true).Path("/build/{build_id}/test/{test_id}").Methods("GET").HandlerFunc(lk.viewTestByBuildIdTestId)
	//r.Path("/{builder}/builds/{buildnum:[0-9]+}/").HandlerFunc(viewBuild)
	//r.Path("/{builder}/builds/{buildnum}/test/{test_phase}/{test_name}").HandlerFunc(app.MakeHandler(Name("view_test")))
	return r
}
