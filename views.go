package logkeeper

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/render"
	"github.com/evergreen-ci/utility"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	otelTrace "go.opentelemetry.io/otel/trace"
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

type logFetchResponse struct {
	logLines chan *model.LogLineItem
	build    *model.Build
	test     *model.Test
}

type closerOp struct {
	name     string
	closerFn func(ctx context.Context) error
}

// logkeeper serves the Logkeeper REST API.
type logkeeper struct {
	render  *render.Render
	opts    LogkeeperOptions
	tracer  otelTrace.Tracer
	closers []closerOp
}

// LogkeeperOptions represents the set of options for creating a new Logkeeper
// REST service.
type LogkeeperOptions struct {
	// URL is the base URL to append to relative paths.
	URL string
	// MaxRequestSize is the maximum allowable request size.
	MaxRequestSize int
}

// NewLogkeeper returns a new Logkeeper REST service with the given options.
func NewLogkeeper(opts LogkeeperOptions) *logkeeper {
	r := render.New(render.Options{
		Directory: "templates",
		HtmlFuncs: template.FuncMap{
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
	tracer := otel.GetTracerProvider().Tracer("github.com/evergreen-ci/logkeeper/logkeeper")
	return &logkeeper{render: r, opts: opts, tracer: tracer}
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

// To avoid expensive computations, check that span was sampled
// before setting any attributes.
func recordAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := otelTrace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

func logErrorf(ctx context.Context, format string, v ...interface{}) {
	err := fmt.Errorf(format, v...)
	grip.Error(message.WrapError(err, message.Fields{
		"request": getCtxRequestId(ctx),
	}))
	recordError(ctx, err)
}

func logWarningf(ctx context.Context, format string, v ...interface{}) {
	err := fmt.Errorf(format, v...)
	grip.Warning(message.WrapError(err, message.Fields{
		"request": getCtxRequestId(ctx),
	}))
}

func recordError(ctx context.Context, err error) {
	span := otelTrace.SpanFromContext(ctx)
	span.SetStatus(codes.Error, err.Error())
	span.RecordError(err)
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}

func (lk *logkeeper) viewBuild(w http.ResponseWriter, r *http.Request) {
	ctx, span := lk.tracer.Start(r.Context(), "ViewBuild")
	defer span.End()
	addCORSHeaders(w, r)

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	recordAttributes(ctx, attribute.String("evergreen.build_id", buildID))

	var (
		build    *model.Build
		tests    []model.Test
		fetchErr *apiError
	)
	build, tests, fetchErr = lk.viewBucketBuild(ctx, buildID)
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

func (lk *logkeeper) viewBucketBuild(ctx context.Context, buildID string) (*model.Build, []model.Test, *apiError) {
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

		build, buildErr = model.FindBuildByID(ctx, lk.tracer, buildID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("finding test for build from bucket")
		defer wg.Done()

		tests, testsErr = model.FindTestsForBuild(ctx, lk.tracer, buildID)
	}()
	wg.Wait()

	if buildErr != nil {
		logErrorf(ctx, "finding build '%s': %v", buildID, buildErr)
		return nil, nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}

	if testsErr != nil {
		logErrorf(ctx, "finding tests for build '%s': %v", buildID, testsErr)
		return nil, nil, &apiError{Err: testsErr.Error(), code: http.StatusInternalServerError}
	}

	return build, tests, nil
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}/all

func (lk *logkeeper) viewAllLogs(w http.ResponseWriter, r *http.Request) {
	ctx, span := lk.tracer.Start(r.Context(), "ViewAllLogs")
	defer span.End()
	addCORSHeaders(w, r)

	vars := mux.Vars(r)
	buildID := vars["build_id"]

	recordAttributes(ctx, attribute.String("evergreen.build_id", buildID))

	if lobsterRedirect(r) {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/all", buildID), http.StatusFound)
		return
	}

	resp, fetchErr := lk.viewBucketLogs(ctx, buildID, "")
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
			logErrorf(ctx, "writing raw log lines from build '%s': %v", buildID, err)
			lk.render.WriteJSON(w, http.StatusInternalServerError, apiError{Err: "rendering log lines"})
		}
		return
	} else {
		err := lk.render.StreamHTML(w, http.StatusOK, struct {
			LogLines      chan *model.LogLineItem
			BuildID       string
			Builder       string
			TestID        string
			TestName      string
			TaskID        string
			TaskExecution int
		}{resp.logLines, resp.build.ID, resp.build.Builder, "", "All logs", resp.build.TaskID, resp.build.TaskExecution}, "base", "test.html")
		if err != nil {
			logErrorf(ctx, "rendering template: %v", err)
		}
	}
}

///////////////////////////////////////////////////////////////////////////////
//
// GET /build/{build_id}/test/{test_id}

func (lk *logkeeper) viewTestLogs(w http.ResponseWriter, r *http.Request) {
	ctx, span := lk.tracer.Start(r.Context(), "ViewTestLogs")
	defer span.End()
	addCORSHeaders(w, r)

	vars := mux.Vars(r)
	buildID := vars["build_id"]
	testID := vars["test_id"]

	recordAttributes(
		ctx,
		attribute.String("evergreen.build_id", buildID),
		attribute.String("evergreen.test_id", testID),
	)

	if lobsterRedirect(r) {
		http.Redirect(w, r, fmt.Sprintf("/lobster/build/%s/test/%s", buildID, testID), http.StatusFound)
		return
	}

	resp, fetchErr := lk.viewBucketLogs(ctx, buildID, testID)
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
			logErrorf(ctx, "writing raw log lines from test '%s' for build '%s': %v", testID, buildID, err)
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
		}{resp.logLines, resp.build.ID, resp.build.Builder, resp.test.ID, resp.test.Name, resp.test.TaskID, resp.test.TaskExecution}, "base", "test.html")
		if err != nil {
			logErrorf(ctx, "rendering template: %v", err)
		}
	}
}

func (lk *logkeeper) viewBucketLogs(ctx context.Context, buildID string, testID string) (*logFetchResponse, *apiError) {
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

		build, buildErr = model.FindBuildByID(ctx, lk.tracer, buildID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("finding test for build from bucket")
		defer wg.Done()

		if testID == "" {
			return
		}
		test, testErr = model.FindTestByID(ctx, lk.tracer, buildID, testID)
	}()
	go func() {
		defer recovery.LogStackTraceAndContinue("downloading log lines from bucket")
		defer wg.Done()

		logLines, logLinesErr = model.DownloadLogLines(ctx, lk.tracer, buildID, testID)
	}()
	wg.Wait()

	if buildErr != nil {
		logErrorf(ctx, "finding build '%s': %v", buildID, buildErr)
		return nil, &apiError{Err: "finding build", code: http.StatusInternalServerError}
	}
	if build == nil {
		return nil, &apiError{Err: "build not found", code: http.StatusNotFound}
	}
	if testErr != nil {
		logErrorf(ctx, "finding test '%s' for build '%s': %v", testID, buildID, testErr)
		return nil, &apiError{Err: "finding test", code: http.StatusInternalServerError}
	}
	if testID != "" && test == nil {
		return nil, &apiError{Err: "test not found", code: http.StatusNotFound}
	}
	if logLinesErr != nil {
		logErrorf(ctx, "downloading logs for build '%s': %v", buildID, logLinesErr)
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
	_, span := lk.tracer.Start(r.Context(), "CheckAppHealth")
	defer span.End()
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
	ctx, span := lk.tracer.Start(r.Context(), "ViewInLobster")
	defer span.End()
	addCORSHeaders(w, r)

	err := lk.render.StreamHTML(w, http.StatusOK, nil, "base", "lobster/build/index.html")
	if err != nil {
		logErrorf(ctx, "Error rendering template: %v", err)
	}
}

///////////////////////////////////////////////////////////////////////////////
//
// Router

func (lk *logkeeper) NewRouter() *mux.Router {
	r := mux.NewRouter().StrictSlash(false)
	r.Use(otelmux.Middleware("logkeeper"))

	// Read methods.
	r.StrictSlash(true).Path("/build/{build_id}").Methods("GET").HandlerFunc(lk.viewBuild)
	r.StrictSlash(true).Path("/build/{build_id}/all").Methods("GET").Handler(handlers.CompressHandler(http.HandlerFunc(lk.viewAllLogs)))
	r.StrictSlash(true).Path("/build/{build_id}/test/{test_id}").Methods("GET").Handler(handlers.CompressHandler(http.HandlerFunc(lk.viewTestLogs)))
	r.PathPrefix("/lobster").Methods("GET").HandlerFunc(lk.viewInLobster)
	r.Path("/status").Methods("GET").HandlerFunc(lk.checkAppHealth)

	return r
}
