package logkeeper

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/render"
	"github.com/evergreen-ci/utility"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
)

const (
	corsOriginsEnvVariable = "LK_CORS_ORIGINS"
	evergreenEnvVariable   = "LK_EVERGREEN_ORIGIN"
	parsleyEnvVariable     = "LK_PARSLEY_ORIGIN"
	maxLogBytes            = 4 * bytesPerMB // 4 MB
)

var corsOrigins []string

func init() {
	origins := os.Getenv(corsOriginsEnvVariable)
	if origins == "" {
		corsOrigins = []string{}
		return
	}
	corsOrigins = strings.Split(origins, ",")
}

func addCORSHeaders(w http.ResponseWriter, r *http.Request) {
	requester := r.Header.Get("Origin")
	// Check if requester is in CORS origins list.
	if utility.StringMatchesAnyRegex(requester, corsOrigins) {
		w.Header().Add("Access-Control-Allow-Origin", requester)
		w.Header().Add("Access-Control-Allow-Credentials", "true")
	} else {
		// Maintain backwards compatibility with the old CORS header.
		w.Header().Add("Access-Control-Allow-Origin", "*")
	}
}

type apiError struct {
	Err     string `json:"err"`
	MaxSize int    `json:"max_size,omitempty"`
	code    int
}

type createdResponse struct {
	ID  string `json:"id,omitempty"`
	URI string `json:"uri"`
}

type logFetchResponse struct {
	logLines chan *model.LogLineItem
	build    *model.Build
	test     *model.Test
}

// logkeeper serves the Logkeeper REST API.
type logkeeper struct {
	render *render.Render
	opts   LogkeeperOptions
}

// LogkeeperOptions represents the set of options for creating a new Logkeeper
// REST service.
type LogkeeperOptions struct {
	// URL is the base URL to append to relative paths.
	URL string
	// MaxRequestSize is the maximum allowable request size.
	MaxRequestSize int
}

// Logkeeper returns a new Logkeeper REST service with the given options.
func NewLogkeeper(opts LogkeeperOptions) *logkeeper {
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

	return &logkeeper{render, opts}
}

// checkContentLength returns an API error if the content length specified by
// the client is larger than the maximum request size. Clients are allowed to
// *not* specify a request size, in which case the HTTP library sets the
// content legnth to -1.
func (lk *logkeeper) checkContentLength(r *http.Request) *apiError {
	if int(r.ContentLength) > lk.opts.MaxRequestSize {
		return &apiError{
			Err: fmt.Sprintf("content length %d over maximum",
				r.ContentLength),
			MaxSize: lk.opts.MaxRequestSize,
			code:    http.StatusRequestEntityTooLarge,
		}
	}

	return nil
}

func (lk *logkeeper) logErrorf(r *http.Request, format string, v ...interface{}) {
	err := fmt.Sprintf(format, v...)
	grip.Error(message.Fields{
		"request": getCtxRequestId(r),
		"error":   err,
	})
}

func (lk *logkeeper) logWarningf(r *http.Request, format string, v ...interface{}) {
	err := fmt.Sprintf(format, v...)
	grip.Warning(message.Fields{
		"request": getCtxRequestId(r),
		"error":   err,
	})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build

func (lk *logkeeper) createBuild(w http.ResponseWriter, r *http.Request) {
	if err := lk.checkContentLength(r); err != nil {
		lk.logErrorf(r, "content length limit exceeded for create build: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	payload := struct {
		Builder       string `json:"builder"`
		BuildNum      int    `json:"buildnum"`
		TaskID        string `json:"task_id"`
		TaskExecution int    `json:"execution"`
	}{}
	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &payload); err != nil {
		lk.logErrorf(r, "bad request to create build: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	id, err := model.NewBuildID(payload.Builder, payload.BuildNum)
	if err != nil {
		lk.logErrorf(r, "creating new build ID: %v", err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "creating new build ID"})
		return
	}

	status := http.StatusOK
	exists, err := model.CheckBuildMetadata(r.Context(), id)
	if err != nil {
		lk.logErrorf(r, "checking metadata in build '%s': %v", id, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding build"})
		return
	}
	if !exists {
		build := model.Build{
			ID:            id,
			Builder:       payload.Builder,
			BuildNum:      payload.BuildNum,
			TaskID:        payload.TaskID,
			TaskExecution: payload.TaskExecution,
		}
		if err = build.UploadMetadata(r.Context()); err != nil {
			lk.logErrorf(r, "uploading build metadata: %v", err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "uploading build metadata"})
			return
		}
		status = http.StatusCreated
	}

	lk.render.WriteJSON(w, status, createdResponse{
		ID:  id,
		URI: fmt.Sprintf("%v/build/%v", lk.opts.URL, id),
	})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build/{build_id}/test

func (lk *logkeeper) createTest(w http.ResponseWriter, r *http.Request) {
	buildID := mux.Vars(r)["build_id"]

	if err := lk.checkContentLength(r); err != nil {
		lk.logErrorf(r, "content length limit exceeded for create test: %s", err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	payload := struct {
		TestFilename  string `json:"test_filename"`
		Command       string `json:"command"`
		Phase         string `json:"phase"`
		TaskID        string `json:"task_id"`
		TaskExecution int    `json:"execution"`
	}{}
	if err := readJSON(r.Body, lk.opts.MaxRequestSize, &payload); err != nil {
		lk.logErrorf(r, "bad request to create test for build '%s': %s", buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	exists, err := model.CheckBuildMetadata(r.Context(), buildID)
	if err != nil {
		lk.logErrorf(r, "checking metadata in build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding build"})
		return
	}
	if !exists {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "build not found"})
		return
	}

	test := model.Test{
		ID:            model.NewTestID(time.Now()),
		Name:          payload.TestFilename,
		BuildID:       buildID,
		TaskID:        payload.TaskID,
		TaskExecution: payload.TaskExecution,
		Phase:         payload.Phase,
		Command:       payload.Command,
	}
	if err = test.UploadTestMetadata(r.Context()); err != nil {
		lk.logErrorf(r, "uploading test metadata for build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "uploading test metadata"})
		return
	}

	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{
		ID:  test.ID,
		URI: fmt.Sprintf("%s/build/%s/test/%s", lk.opts.URL, buildID, test.ID),
	})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build/{build_id}

func (lk *logkeeper) appendGlobalLog(w http.ResponseWriter, r *http.Request) {
	buildID := mux.Vars(r)["build_id"]

	if err := lk.checkContentLength(r); err != nil {
		lk.logWarningf(r, "content length limit exceeded for append log lines to build '%s': %s", buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	exists, err := model.CheckBuildMetadata(r.Context(), buildID)
	if err != nil {
		lk.logErrorf(r, "checking metadata in build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding build"})
		return
	}
	if !exists {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "build not found"})
		return
	}

	lines, err := model.UnmarshalLogJSON(&LimitedReader{R: r.Body, N: lk.opts.MaxRequestSize})
	if err != nil {
		lk.logErrorf(r, "bad request to append log lines to build '%s': %s", buildID, err)
		if errors.Cause(err) == ErrReadSizeLimitExceeded {
			lk.render.WriteJSON(w, http.StatusRequestEntityTooLarge, err)
		} else {
			lk.render.WriteJSON(w, http.StatusBadRequest, err)
		}
		return
	}

	if err = model.InsertLogLines(r.Context(), buildID, "", lines, maxLogBytes); err != nil {
		lk.logErrorf(r, "appending log lines to build '%s': %v", buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "appending log lines"})
		return
	}

	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{
		ID:  "",
		URI: fmt.Sprintf("%s/build/%s", lk.opts.URL, buildID),
	})
}

///////////////////////////////////////////////////////////////////////////////
//
// POST /build/{build_id}/test/{test_id}

func (lk *logkeeper) appendTestLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	buildID := vars["build_id"]
	testID := vars["test_id"]

	if err := lk.checkContentLength(r); err != nil {
		lk.logWarningf(r, "content length limit exceeded for append log lines to test '%s' for build '%s': %s", testID, buildID, err.Err)
		lk.render.WriteJSON(w, err.code, err)
		return
	}

	exists, err := model.CheckTestMetadata(r.Context(), buildID, testID)
	if err != nil {
		lk.logErrorf(r, "checking metadata of test '%s' for build '%s': %v", testID, buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "finding test"})
		return
	}
	if !exists {
		lk.render.WriteJSON(w, http.StatusNotFound, apiError{Err: "test not found"})
		return
	}

	lines, err := model.UnmarshalLogJSON(&LimitedReader{R: r.Body, N: lk.opts.MaxRequestSize})
	if err != nil {
		lk.logErrorf(r, "bad request to append log to test '%s' for build '%s': %s", testID, buildID, err)
		if errors.Cause(err) == ErrReadSizeLimitExceeded {
			lk.render.WriteJSON(w, http.StatusRequestEntityTooLarge, err)
		} else {
			lk.render.WriteJSON(w, http.StatusBadRequest, err)
		}
		return
	}
	if len(lines) == 0 {
		lk.render.WriteJSON(w, http.StatusOK, "")
		return
	}

	if err = model.InsertLogLines(r.Context(), buildID, testID, lines, maxLogBytes); err != nil {
		lk.logErrorf(r, "appending log lines to test '%s' for build '%s': %v", testID, buildID, err)
		lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "appending log lines"})
	}

	lk.render.WriteJSON(w, http.StatusCreated, createdResponse{
		ID:  "",
		URI: fmt.Sprintf("%s/build/%s/test/%s", lk.opts.URL, buildID, testID),
	})
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}

func (lk *logkeeper) viewBuild(w http.ResponseWriter, r *http.Request) {
	addCORSHeaders(w, r)

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	var (
		build    *model.Build
		tests    []model.Test
		fetchErr *apiError
	)
	build, tests, fetchErr = lk.viewBucketBuild(r, buildID)
	if fetchErr != nil {
		lk.render.WriteJSON(w, fetchErr.code, *fetchErr)
		return
	}

	if r.FormValue("metadata") == "true" {
		payload := struct {
			model.Build
			Tests []model.Test `json:"tests"`
		}{*build, tests}
		lk.render.WriteJSON(w, http.StatusOK, payload)
		return
	}

	lk.render.WriteHTML(w, http.StatusOK, struct {
		Build        *model.Build
		Tests        []model.Test
		EvergreenURL string
		ParsleyURL   string
	}{build, tests, os.Getenv(evergreenEnvVariable), os.Getenv(parsleyEnvVariable)}, "base", "build.html")
}

func (lk *logkeeper) viewBucketBuild(r *http.Request, buildID string) (*model.Build, []model.Test, *apiError) {
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

		build, buildErr = model.FindBuildByID(r.Context(), buildID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("finding test for build from bucket")
		defer wg.Done()

		tests, testsErr = model.FindTestsForBuild(r.Context(), buildID)
	}()
	wg.Wait()

	if buildErr != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, buildErr)
		return nil, nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}

	if testsErr != nil {
		lk.logErrorf(r, "finding tests for build '%s': %v", buildID, testsErr)
		return nil, nil, &apiError{Err: testsErr.Error(), code: http.StatusInternalServerError}
	}

	return build, tests, nil
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}/all

func (lk *logkeeper) viewAllLogs(w http.ResponseWriter, r *http.Request) {
	addCORSHeaders(w, r)

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	if lobsterRedirect(r) {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/all", buildID), http.StatusFound)
		return
	}

	resp, fetchErr := lk.viewBucketLogs(r, buildID, "")
	if fetchErr != nil {
		lk.render.WriteJSON(w, fetchErr.code, *fetchErr)
		return
	}

	if r.FormValue("metadata") == "true" {
		lk.render.WriteJSON(w, http.StatusOK, resp.build)
		return
	}

	if len(r.FormValue("raw")) > 0 || r.Header.Get("Accept") == "text/plain" {
		if err := writeRawLines(w, resp); err != nil {
			lk.logErrorf(r, "writing raw log lines from build '%s': %v", buildID, err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "rendering log lines"})
		}
		return
	} else {
		err := lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines chan *model.LogLineItem
			BuildID  string
			Builder  string
			TestID   string
			TestName string
			TaskID   string
		}{resp.logLines, resp.build.ID, resp.build.Builder, "", "All logs", resp.build.TaskID}, "base", "test.html")
		if err != nil {
			lk.logErrorf(r, "rendering template: %v", err)
		}
	}
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}/test/{test_id}

func (lk *logkeeper) viewTestLogs(w http.ResponseWriter, r *http.Request) {
	addCORSHeaders(w, r)

	vars := mux.Vars(r)
	buildID := vars["build_id"]
	testID := vars["test_id"]

	if lobsterRedirect(r) {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/test/%s", buildID, testID), http.StatusFound)
		return
	}

	resp, fetchErr := lk.viewBucketLogs(r, buildID, testID)
	if fetchErr != nil {
		lk.render.WriteJSON(w, fetchErr.code, *fetchErr)
		return
	}

	if r.FormValue("metadata") == "true" {
		lk.render.WriteJSON(w, http.StatusOK, resp.test)
		return
	}

	if len(r.FormValue("raw")) > 0 || r.Header.Get("Accept") == "text/plain" {
		if err := writeRawLines(w, resp); err != nil {
			lk.logErrorf(r, "writing raw log lines from test '%s' for build '%s': %v", testID, buildID, err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "rendering log lines"})
		}
	} else {
		err := lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines      chan *model.LogLineItem
			BuildID       string
			Builder       string
			TestID        string
			TestName      string
			TaskID        string
			TaskExecution int
		}{resp.logLines, resp.build.ID, resp.build.Builder, resp.test.ID, resp.test.Name, resp.test.TaskID, resp.build.TaskExecution}, "base", "test.html")
		if err != nil {
			lk.logErrorf(r, "rendering template: %v", err)
		}
	}
}

func (lk *logkeeper) viewBucketLogs(r *http.Request, buildID string, testID string) (*logFetchResponse, *apiError) {
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

		build, buildErr = model.FindBuildByID(r.Context(), buildID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("finding test for build from bucket")
		defer wg.Done()

		if testID == "" {
			return
		}
		test, testErr = model.FindTestByID(r.Context(), buildID, testID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("downloading log lines from bucket")
		defer wg.Done()

		logLines, logLinesErr = model.DownloadLogLines(r.Context(), buildID, testID)
	}()
	wg.Wait()

	if buildErr != nil {
		lk.logErrorf(r, "finding build '%s': %v", buildID, buildErr)
		return nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}
	if testErr != nil {
		lk.logErrorf(r, "finding test '%s' for build '%s': %v", testID, buildID, testErr)
		return nil, &apiError{Err: "finding test", code: http.StatusInternalServerError}
	}
	if testID != "" && test == nil {
		return nil, &apiError{Err: "test not found", code: http.StatusNotFound}
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

func writeRawLines(w http.ResponseWriter, resp *logFetchResponse) error {
	var (
		numLines    int
		totalSize   int
		maxLineSize int
		minLineSize = maxLogBytes + len("\n")
	)

	var hasLines bool
	for line := range resp.logLines {
		hasLines = true

		lineData := []byte(line.Data + "\n")
		_, err := w.Write(lineData)
		if err != nil {
			return err
		}

		lineSize := len(lineData)
		if lineSize > maxLineSize {
			maxLineSize = lineSize
		}
		if lineSize < minLineSize {
			minLineSize = lineSize
		}
		numLines++
		totalSize += lineSize
	}

	avgLineSize := float64(totalSize) / float64(numLines)
	if !hasLines {
		// Set average line size to 0 since it will be NaN when there
		// are no lines.
		avgLineSize = 0
		// Set the min line size to 0 since the initial value is the
		// max line size allowed.
		minLineSize = 0
	}
	msg := message.Fields{
		"message":             "requested log size stats",
		"build_id":            resp.build.ID,
		"task_id":             resp.build.TaskID,
		"task_execution":      resp.build.TaskExecution,
		"total_size_mb":       float64(totalSize) / bytesPerMB,
		"num_lines":           numLines,
		"max_line_size_bytes": maxLineSize,
		"min_line_size_bytes": minLineSize,
		"avg_line_size_bytes": avgLineSize,
	}
	if resp.test != nil {
		msg["test_id"] = resp.test.ID
		msg["test_name"] = resp.test.Name
	}
	grip.Info(msg)

	return nil
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /status

func (lk *logkeeper) checkAppHealth(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		Build          string `json:"build_id"`
		MaxRequestSize int    `json:"maxRequestSize"`
	}{
		Build:          BuildRevision,
		MaxRequestSize: lk.opts.MaxRequestSize,
	}

	lk.render.WriteJSON(w, http.StatusOK, &resp)
}

///////////////////////////////////////////////////////////////////////////////
//
// Lobster

func lobsterRedirect(r *http.Request) bool {
	return len(r.FormValue("html")) == 0 && len(r.FormValue("raw")) == 0 && r.Header.Get("Accept") != "text/plain" && r.FormValue("metadata") != "true"
}

func (lk *logkeeper) viewInLobster(w http.ResponseWriter, r *http.Request) {
	addCORSHeaders(w, r)

	err := lk.render.StreamHTML(w, http.StatusOK, nil, "base", "lobster/build/index.html")
	if err != nil {
		lk.logErrorf(r, "Error rendering template: %v", err)
	}
}

///////////////////////////////////////////////////////////////////////////////
//
// Router

func (lk *logkeeper) NewRouter() *mux.Router {
	r := mux.NewRouter().StrictSlash(false)

	// Write methods.
	r.Path("/build/").Methods("POST").HandlerFunc(lk.createBuild)
	r.Path("/build").Methods("POST").HandlerFunc(lk.createBuild)
	r.Path("/build/{build_id}/test/").Methods("POST").HandlerFunc(lk.createTest)
	r.Path("/build/{build_id}/test").Methods("POST").HandlerFunc(lk.createTest)
	r.Path("/build/{build_id}/").Methods("POST").HandlerFunc(lk.appendGlobalLog)
	r.Path("/build/{build_id}").Methods("POST").HandlerFunc(lk.appendGlobalLog)
	r.Path("/build/{build_id}/test/{test_id}/").Methods("POST").HandlerFunc(lk.appendTestLog)
	r.Path("/build/{build_id}/test/{test_id}").Methods("POST").HandlerFunc(lk.appendTestLog)

	// Read methods.
	r.StrictSlash(true).Path("/build/{build_id}").Methods("GET").HandlerFunc(lk.viewBuild)
	r.StrictSlash(true).Path("/build/{build_id}/all").Methods("GET").Handler(handlers.CompressHandler(http.HandlerFunc(lk.viewAllLogs)))
	r.StrictSlash(true).Path("/build/{build_id}/test/{test_id}").Methods("GET").Handler(handlers.CompressHandler(http.HandlerFunc(lk.viewTestLogs)))
	r.PathPrefix("/lobster").Methods("GET").HandlerFunc(lk.viewInLobster)
	r.Path("/status").Methods("GET").HandlerFunc(lk.checkAppHealth)

	return r
}
