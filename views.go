package logkeeper

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/featureswitch"
	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/logkeeper/storage"
	"github.com/evergreen-ci/pail"
	"github.com/evergreen-ci/render"
	"github.com/gorilla/mux"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	"gopkg.in/mgo.v2/bson"
)

const maxLogBytes = 4 * 1024 * 1024 // 4 MB

type Options struct {
	//Base URL to append to relative paths
	URL string

	// Maximum Request Size
	MaxRequestSize int

	// Bucket stores data in offline storage.
	Bucket storage.Bucket
}

type logKeeper struct {
	render *render.Render
	opts   Options
}

type createdResponse struct {
	Id  string `json:"id,omitempty"`
	URI string `json:"uri"`
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

type logFetchResponse struct {
	logLines chan *model.LogLineItem
	build    *model.Build
	test     *model.Test
}

func (lk *logKeeper) logErrorf(r *http.Request, format string, v ...interface{}) {
	err := fmt.Sprintf(format, v...)
	grip.Error(message.Fields{
		"request": getCtxRequestId(r),
		"error":   err,
	})
}

func (lk *logKeeper) logWarningf(r *http.Request, format string, v ...interface{}) {
	err := fmt.Sprintf(format, v...)
	grip.Warning(message.Fields{
		"request": getCtxRequestId(r),
		"error":   err,
	})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build

func (lk *logKeeper) createBuild(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := lk.checkContentLength(r); err != nil {
		lk.logErrorf(r, "content length limit exceeded for create build: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	buildParameters := struct {
		Builder  string `json:"builder"`
		BuildNum int    `json:"buildnum"`
		TaskId   string `json:"task_id"`
		S3       *bool  `json:"s3"`
	}{}

	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &buildParameters); err != nil {
		lk.logErrorf(r, "bad request to create build: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	existingBuild, err := model.FindBuildByBuilder(buildParameters.Builder, buildParameters.BuildNum)
	if err != nil {
		lk.logErrorf(r, "finding build by builder: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding build by builder"})
		return
	}
	if existingBuild != nil {
		existingBuildUri := fmt.Sprintf("%v/build/%v", lk.opts.URL, existingBuild.Id)
		response := createdResponse{existingBuild.Id, existingBuildUri}
		lk.render.WriteJSON(w, http.StatusOK, response)
		return
	}

	newBuildId, err := model.NewBuildId(buildParameters.Builder, buildParameters.BuildNum)
	if err != nil {
		lk.logErrorf(r, "creating new build ID: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "creating new build ID"})
		return
	}

	newBuild := model.Build{
		Id:       newBuildId,
		Builder:  buildParameters.Builder,
		BuildNum: buildParameters.BuildNum,
		Name:     fmt.Sprintf("%v #%v", buildParameters.Builder, buildParameters.BuildNum),
		Started:  time.Now(),
		Info:     model.BuildInfo{TaskID: buildParameters.TaskId},
		S3:       shouldWriteS3(buildParameters.S3, newBuildId),
	}
	if err = newBuild.Insert(); err != nil {
		lk.logErrorf(r, "inserting new build: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "inserting build"})
		return
	}

	if newBuild.S3 {
		if err := lk.opts.Bucket.UploadBuildMetadata(r.Context(), newBuild); err != nil {
			lk.logErrorf(r, "uploading build metadata: %v", err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "uploading build metadata"})
			return
		}
	}

	newBuildUri := fmt.Sprintf("%v/build/%v", lk.opts.URL, newBuildId)

	response := createdResponse{newBuildId, newBuildUri}
	lk.render.WriteJSON(w, http.StatusCreated, response)
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build/{build_id}/test

func (lk *logKeeper) createTest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if err := lk.checkContentLength(r); err != nil {
		lk.logErrorf(r, "content length limit exceeded for create test: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	build, err := model.FindBuildById(buildID)
	if err != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding build"})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "build not found"})
		return
	}

	testParams := struct {
		TestFilename string `json:"test_filename"`
		Command      string `json:"command"`
		Phase        string `json:"phase"`
		TaskId       string `json:"task_id"`
	}{}

	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &testParams); err != nil {
		lk.logErrorf(r, "bad request to create test for build '%s': %s", buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	newTest := model.Test{
		Id:        bson.NewObjectId(),
		BuildId:   build.Id,
		BuildName: build.Name,
		Name:      testParams.TestFilename,
		Command:   testParams.Command,
		Started:   time.Now(),
		Phase:     testParams.Phase,
		Info:      model.TestInfo{TaskID: testParams.TaskId},
	}
	if err := newTest.Insert(); err != nil {
		lk.logErrorf(r, "inserting test for build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	if build.S3 {
		if err := lk.opts.Bucket.UploadTestMetadata(r.Context(), newTest); err != nil {
			lk.logErrorf(r, "uploading test metadata for build '%s': %v", buildID, err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "uploading test metadata"})
			return
		}
	}

	testUri := fmt.Sprintf("%s/build/%s/test/%s", lk.opts.URL, build.Id, newTest.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{newTest.Id.Hex(), testUri})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build/{build_id}

func (lk *logKeeper) appendGlobalLog(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	if err := lk.checkContentLength(r); err != nil {
		lk.logWarningf(r, "content length limit exceeded for append log lines to build '%s': %s", buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	build, err := model.FindBuildById(buildID)
	if err != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding build"})
		return
	}
	if build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "build not found"})
		return
	}

	var lines []model.LogLine
	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &lines); err != nil {
		lk.logErrorf(r, "bad request to append log lines to build '%s': %s", buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	if len(lines) == 0 {
		// no need to insert anything, so stop here
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	chunks, err := model.GroupLines(lines, maxLogBytes)
	if err != nil {
		lk.logErrorf(r, "unmarshalling log lines for build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{Err: err.Error()})
		return
	}

	if err = build.IncrementSequence(len(chunks)); err != nil {
		lk.logErrorf(r, "incrementing sequence in build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "incrementing build sequence"})
		return
	}

	if err = model.InsertLogChunks(build.Id, nil, build.Seq, chunks); err != nil {
		lk.logErrorf(r, "inserting DB log lines to build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "inserting log lines"})
		return
	}

	if build.S3 {
		if err := lk.opts.Bucket.InsertLogChunks(r.Context(), build.Id, "", chunks); err != nil {
			lk.logErrorf(r, "appending log lines to build '%s': %v", buildID, err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "appending log lines"})
			return
		}
	}

	testUrl := fmt.Sprintf("%s/build/%s/", lk.opts.URL, build.Id)
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build/{build_id}/test/{test_id}

func (lk *logKeeper) appendLog(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildID := vars["build_id"]
	testID := vars["test_id"]

	if err := lk.checkContentLength(r); err != nil {
		lk.logWarningf(r, "content length limit exceeded for append log lines to test '%s' for build '%s': %s", buildID, testID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	build, err := model.FindBuildById(buildID)
	if err != nil || build == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "build not found"})
		return
	}

	test, err := model.FindTestByID(testID)
	if err != nil || test == nil {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "test not found"})
		return
	}

	var lines []model.LogLine
	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &lines); err != nil {
		lk.logErrorf(r, "bad request to append log to test '%s' for build '%s': %s", testID, buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	if len(lines) == 0 {
		// No need to insert anything, so stop here.
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	chunks, err := model.GroupLines(lines, maxLogBytes)
	if err != nil {
		lk.logErrorf(r, "unmarshalling log lines: %v", err)
		lk.render.WriteJSON(w, http.StatusBadRequest, apiError{Err: err.Error()})
		return
	}

	if err = test.IncrementSequence(len(chunks)); err != nil {
		lk.logErrorf(r, "incrementing sequence in test '%s' for build '%s': %v", buildID, testID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "incrementing test sequence"})
		return
	}

	if err = model.InsertLogChunks(build.Id, &test.Id, test.Seq, chunks); err != nil {
		lk.logErrorf(r, "inserting DB log lines to test '%s' for build '%s': %v", buildID, testID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: err.Error()})
		return
	}

	if build.S3 {
		if err := lk.opts.Bucket.InsertLogChunks(r.Context(), build.Id, test.Id.Hex(), chunks); err != nil {
			lk.logErrorf(r, "appending log lines to test '%s' for build '%s': %v", buildID, testID, err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "appending log lines"})
			return
		}
	}

	testUrl := fmt.Sprintf("%s/build/%s/test/%s", lk.opts.URL, build.Id, test.Id.Hex())
	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{"", testUrl})
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}

func (lk *logKeeper) viewBuild(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	var (
		build      *model.Build
		tests      []model.Test
		fetchError *apiError
	)
	if shouldRead, shouldFallback := shouldReadS3(r.FormValue("s3"), buildID); shouldRead {
		build, tests, fetchError = lk.viewBucketBuild(r, buildID, shouldFallback)
	} else {
		build, tests, fetchError = lk.viewDBBuild(r, buildID)
	}

	if fetchError != nil {
		lk.render.WriteJSON(w, fetchError.code, *fetchError)
		return
	}

	lk.render.WriteHTML(w, http.StatusOK, struct {
		Build *model.Build
		Tests []model.Test
	}{build, tests}, "base", "build.html")
}

func (lk *logKeeper) viewBucketBuild(r *http.Request, buildID string, shouldFallBack bool) (*model.Build, []model.Test, *apiError) {
	var (
		wg       sync.WaitGroup
		build    *model.Build
		buildErr error
		tests    []model.Test
		testsErr error
	)

	wg.Add(2)
	go func() {
		defer recovery.LogStackTraceAndContinue("finding build from bucket")
		defer wg.Done()

		build, buildErr = lk.opts.Bucket.FindBuildByID(r.Context(), buildID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("finding test for build from bucket")
		defer wg.Done()

		tests, testsErr = lk.opts.Bucket.FindTestsForBuild(r.Context(), buildID)
	}()
	wg.Wait()

	if pail.IsKeyNotFoundError(buildErr) {
		if shouldFallBack {
			return lk.viewDBBuild(r, buildID)
		} else {
			return nil, nil, &apiError{Err: "finding build", code: http.StatusNotFound}
		}
	}

	if buildErr != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, buildErr)
		return nil, nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}

	if testsErr != nil {
		lk.logErrorf(r, "finding tests for build '%s': %v", buildID, testsErr)
		return nil, nil, &apiError{Err: testsErr.Error(), code: http.StatusInternalServerError}
	}

	return build, tests, nil
}

func (lk *logKeeper) viewDBBuild(r *http.Request, buildID string) (*model.Build, []model.Test, *apiError) {
	build, err := model.FindBuildById(buildID)
	if err != nil {
		lk.logErrorf(r, "finding DB build '%s': %v", buildID, err)
		return nil, nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}

	tests, err := model.FindTestsForBuild(buildID)
	if err != nil {
		lk.logErrorf(r, "finding tests for DB build '%s': %v", buildID, err)
		return nil, nil, &apiError{Err: "finding tests for build", code: http.StatusInternalServerError}
	}

	return build, tests, nil
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}/all

func (lk *logKeeper) viewAllLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	if lobsterRedirect(r) {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/all", buildID), http.StatusFound)
		return
	}

	var (
		result     *logFetchResponse
		fetchError *apiError
	)
	if shouldRead, shouldFallback := shouldReadS3(r.FormValue("s3"), buildID); shouldRead {
		result, fetchError = lk.viewBucketLogs(r, buildID, "", shouldFallback)
	} else {
		result, fetchError = lk.viewAllDBLogs(r, buildID)
	}
	if fetchError != nil {
		lk.render.WriteJSON(w, fetchError.code, *fetchError)
		return
	}

	if len(r.FormValue("raw")) > 0 || r.Header.Get("Accept") == "text/plain" {
		for line := range result.logLines {
			_, err := w.Write([]byte(line.Data + "\n"))
			if err != nil {
				lk.logErrorf(r, "writing raw log lines from build '%s': %v", buildID, err)
				lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "rendering log lines"})
				return
			}
		}
		return
	} else {
		err := lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *model.LogLineItem
			BuildId  string
			Builder  string
			TestId   string
			TestName string
			Info     model.BuildInfo
		}{result.logLines, result.build.Id, result.build.Builder, "", "All logs", result.build.Info}, "base", "test.html")
		if err != nil {
			lk.logErrorf(r, "rendering template: %v", err)
		}
	}
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}/test/{test_id}

func (lk *logKeeper) viewTestLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	defer r.Body.Close()

	vars := mux.Vars(r)
	buildID := vars["build_id"]
	testID := vars["test_id"]

	if lobsterRedirect(r) {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/test/%s", buildID, testID), http.StatusFound)
		return
	}

	var (
		result     *logFetchResponse
		fetchError *apiError
	)
	if shouldRead, shouldFallback := shouldReadS3(r.FormValue("s3"), buildID); shouldRead {
		result, fetchError = lk.viewBucketLogs(r, buildID, testID, shouldFallback)
	} else {
		result, fetchError = lk.viewDBTestLogs(r, buildID, testID)
	}
	if fetchError != nil {
		lk.render.WriteJSON(w, fetchError.code, *fetchError)
		return
	}

	if len(r.FormValue("raw")) > 0 || r.Header.Get("Accept") == "text/plain" {
		emptyLog := true
		for line := range result.logLines {
			emptyLog = false
			_, err := w.Write([]byte(line.Data + "\n"))
			if err != nil {
				lk.logErrorf(r, "writing raw log lines from test '%s' for build '%s': %v", testID, buildID, err)
				lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "rendering log lines"})
				return
			}
		}
		if emptyLog {
			lk.render.WriteJSON(w, http.StatusOK, nil)
		}
	} else {
		err := lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *model.LogLineItem
			BuildId  string
			Builder  string
			TestId   string
			TestName string
			Info     model.TestInfo
		}{result.logLines, result.build.Id, result.build.Builder, result.test.Id.Hex(), result.test.Name, result.test.Info}, "base", "test.html")
		if err != nil {
			lk.logErrorf(r, "rendering template: %v", err)
		}
	}
}

func (lk *logKeeper) viewBucketLogs(r *http.Request, buildID string, testID string, shouldFallBack bool) (*logFetchResponse, *apiError) {
	var (
		wg          sync.WaitGroup
		build       *model.Build
		buildErr    error
		test        *model.Test
		testErr     error
		logLines    chan *model.LogLineItem
		logLinesErr error
	)

	wg.Add(3)
	go func() {
		defer recovery.LogStackTraceAndContinue("finding build from bucket")
		defer wg.Done()

		build, buildErr = lk.opts.Bucket.FindBuildByID(r.Context(), buildID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("finding test for build from bucket")
		defer wg.Done()

		if testID == "" {
			return
		}
		test, testErr = lk.opts.Bucket.FindTestByID(r.Context(), buildID, testID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("downloading log lines from bucket")
		defer wg.Done()

		logLines, logLinesErr = lk.opts.Bucket.DownloadLogLines(r.Context(), buildID, testID)
	}()
	wg.Wait()

	if pail.IsKeyNotFoundError(buildErr) {
		if shouldFallBack {
			if testID == "" {
				return lk.viewAllDBLogs(r, buildID)
			} else {
				return lk.viewDBTestLogs(r, buildID, testID)
			}
		} else {
			return nil, &apiError{Err: "finding build", code: http.StatusNotFound}
		}
	}
	if buildErr != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, buildErr)
		return nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if testID != "" && pail.IsKeyNotFoundError(testErr) {
		return nil, &apiError{Err: "test not found", code: http.StatusNotFound}
	}
	if testErr != nil {
		lk.logErrorf(r, "finding test '%s' for build '%s': %v", testID, buildID, testErr)
		return nil, &apiError{Err: "finding test", code: http.StatusInternalServerError}
	}
	if logLinesErr != nil {
		lk.logErrorf(r, "downloading logs for build '%s': %v", buildID, logLinesErr)
		return nil, &apiError{Err: "downloading logs", code: http.StatusInternalServerError}
	}

	return &logFetchResponse{
		logLines: logLines,
		build:    build,
		test:     test,
	}, nil
}

func (lk *logKeeper) viewAllDBLogs(r *http.Request, buildID string) (*logFetchResponse, *apiError) {
	build, err := model.FindBuildById(buildID)
	if err != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, err)
		return nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}

	logLines, err := model.AllLogs(build.Id)
	if err != nil {
		lk.logErrorf(r, "downloading all DB logs for build '%s': %v", buildID, err)
		return nil, &apiError{Err: "downloading all logs", code: http.StatusInternalServerError}
	}

	return &logFetchResponse{
		logLines: logLines,
		build:    build,
	}, nil
}

func (lk *logKeeper) viewDBTestLogs(r *http.Request, buildID string, testID string) (*logFetchResponse, *apiError) {
	build, err := model.FindBuildById(buildID)
	if err != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, err)
		return nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}

	test, err := model.FindTestByID(testID)
	if err != nil {
		lk.logErrorf(r, "finding test '%s' for build '%s': %v", testID, buildID, err)
		return nil, &apiError{Err: "finding test", code: http.StatusInternalServerError}
	}
	if test == nil {
		return nil, &apiError{Err: "test not found", code: http.StatusNotFound}
	}

	logLines, err := model.MergedTestLogs(test)
	if err != nil {
		lk.logErrorf(r, "finding logs for test '%s' in build '%s': %v", testID, buildID, err)
		return nil, &apiError{Err: err.Error(), code: http.StatusInternalServerError}
	}

	return &logFetchResponse{
		logLines: logLines,
		build:    build,
		test:     test,
	}, nil
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /status
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
		CleanupStatus   amboy.QueueStats `json:"cleanup_queue_stats"`
	}{
		Build:           BuildRevision,
		MaxRequestSize:  lk.opts.MaxRequestSize,
		BatchSize:       CleanupBatchSize,
		NumWorkers:      AmboyWorkers,
		DurationSeconds: AmboyInterval.Seconds(),
		CleanupStatus:   env.CleanupQueue().Stats(r.Context()),
	}

	resp.DB = true
	lk.render.WriteJSON(w, http.StatusOK, &resp)
}

///////////////////////////////////////////////////////////////////////////////
//
// Lobster

func lobsterRedirect(r *http.Request) bool {
	return len(r.FormValue("html")) == 0 && len(r.FormValue("raw")) == 0 && r.Header.Get("Accept") != "text/plain"
}

func (lk *logKeeper) viewInLobster(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Origin", "*")
	err := lk.render.StreamHTML(w, http.StatusOK, nil, "base", "lobster/build/index.html")
	if err != nil {
		lk.logErrorf(r, "Error rendering template: %v", err)
	}
}

///////////////////////////////////////////////////////////////////////////////
//
// Router

func (lk *logKeeper) NewRouter() *mux.Router {
	r := mux.NewRouter().StrictSlash(false)

	// Write methods.
	r.Path("/build/").Methods("POST").HandlerFunc(lk.createBuild)
	r.Path("/build").Methods("POST").HandlerFunc(lk.createBuild)
	r.Path("/build/{build_id}/test/").Methods("POST").HandlerFunc(lk.createTest)
	r.Path("/build/{build_id}/test").Methods("POST").HandlerFunc(lk.createTest)
	r.Path("/build/{build_id}/").Methods("POST").HandlerFunc(lk.appendGlobalLog)
	r.Path("/build/{build_id}").Methods("POST").HandlerFunc(lk.appendGlobalLog)
	r.Path("/build/{build_id}/test/{test_id}/").Methods("POST").HandlerFunc(lk.appendLog)
	r.Path("/build/{build_id}/test/{test_id}").Methods("POST").HandlerFunc(lk.appendLog)

	// Read methods.
	r.StrictSlash(true).Path("/build/{build_id}").Methods("GET").HandlerFunc(lk.viewBuild)
	r.StrictSlash(true).Path("/build/{build_id}/all").Methods("GET").HandlerFunc(lk.viewAllLogs)
	r.StrictSlash(true).Path("/build/{build_id}/test/{test_id}").Methods("GET").HandlerFunc(lk.viewTestLogs)
	r.PathPrefix("/lobster").Methods("GET").HandlerFunc(lk.viewInLobster)
	//r.Path("/{builder}/builds/{buildnum:[0-9]+}/").HandlerFunc(viewBuild)
	//r.Path("/{builder}/builds/{buildnum}/test/{test_phase}/{test_name}").HandlerFunc(app.MakeHandler(Name("view_test")))
	r.Path("/status").Methods("GET").HandlerFunc(lk.checkAppHealth)

	return r
}

///////////////////////////////////////////////////////////////////////////////
//
// Helper functions

func shouldWriteS3(explicitParam *bool, buildID string) bool {
	if explicitParam == nil {
		return featureswitch.WriteToS3Enabled(buildID)
	} else {
		return *explicitParam
	}
}

// Takes the s3 query string parameter and a build id, and returns
// whether we should try to read from S3, and whether we should 
// fall back to the database if the build is not in s3.
func shouldReadS3(explicitParam string, buildID string) (bool, bool) {
	parseResult, err := strconv.ParseBool(explicitParam)
	if err != nil {
		return featureswitch.ReadFromS3Enabled(buildID), true
	} else {
		return parseResult, false
	}
}
