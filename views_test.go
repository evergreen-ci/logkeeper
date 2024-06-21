package logkeeper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMaxReqSize = 10 * 1024 * 1024

func TestAddCORSHeaders(t *testing.T) {
	prev := corsOrigins
	corsOrigins = []string{"views-*"}
	defer func() {
		corsOrigins = prev
	}()

	t.Run("RequesterInCORSOriginsList", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("", "/", nil)
		r.Header.Add("Origin", "views-test")

		addCORSHeaders(w, r)
		assert.Equal(t, "views-test", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	})
	t.Run("RequesterNotInCORSOriginsList", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("", "/", nil)
		r.Header.Add("Origin", "test")

		addCORSHeaders(w, r)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	})
	t.Run("NoRequester", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("", "/", nil)

		addCORSHeaders(w, r)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	})
}

func TestViewBuild(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/simple")()

	buildID := "5a75f537726934e4b62833ab6d5dca41"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	lk := NewLogkeeper(
		LogkeeperOptions{
			URL:            "https://logkeeper.com",
			MaxRequestSize: testMaxReqSize,
		},
	)
	for _, test := range []struct {
		name               string
		buildID            string
		params             string
		expectedStatusCode int
		test               func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:               "BuildDNE",
			buildID:            "DNE",
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name:               "BuildWithTests",
			buildID:            buildID,
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				build, err := model.FindBuildByID(ctx, tracer, buildID)
				require.NoError(t, err)
				tests, err := model.FindTestsForBuild(ctx, tracer, buildID)
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				require.NoError(t, lk.render.HTML(expectedOut, struct {
					Build        *model.Build
					Tests        []model.Test
					EvergreenURL string
					ParsleyURL   string
				}{build, tests, "", ""}, "base", "build.html"))
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
		{
			name:               "Metadata",
			buildID:            buildID,
			params:             "metadata=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				build, err := model.FindBuildByID(ctx, tracer, buildID)
				require.NoError(t, err)
				tests, err := model.FindTestsForBuild(ctx, tracer, buildID)
				require.NoError(t, err)

				expectedOut, err := json.MarshalIndent(struct {
					model.Build
					Tests []model.Test `json:"tests"`
				}{*build, tests}, "", "  ")
				require.NoError(t, err)
				assert.Equal(t, expectedOut, resp.Body.Bytes())
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := doReq(t, lk.NewRouter(), http.MethodGet, nil, fmt.Sprintf("%s/build/%s?%s", lk.opts.URL, test.buildID, test.params), nil)
			require.Equal(t, test.expectedStatusCode, resp.Code)
			checkCORSHeader(t, resp.Header())
			test.test(t, resp)
		})
	}
}

func TestViewAllLogs(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/simple")()

	buildID := "5a75f537726934e4b62833ab6d5dca41"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	lk := NewLogkeeper(
		LogkeeperOptions{
			URL:            "https://logkeeper.com",
			MaxRequestSize: testMaxReqSize,
		},
	)
	for _, test := range []struct {
		name               string
		headers            map[string]string
		buildID            string
		params             string
		expectedStatusCode int
		test               func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:               "BuildDNE",
			buildID:            "DNE",
			params:             "raw=true",
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name:               "LobsterRedirect",
			buildID:            buildID,
			expectedStatusCode: http.StatusFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				assert.Equal(t, fmt.Sprintf("/lobster/build/%s/all", buildID), resp.Header().Get("Location"))
			},
		},
		{
			name:               "RawLogsQueryParam",
			buildID:            buildID,
			params:             "raw=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				lines, err := model.DownloadLogLines(ctx, tracer, buildID, "")
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				for line := range lines {
					_, err := expectedOut.WriteString(line.Data + "\n")
					require.NoError(t, err)
				}
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
		{
			name:               "RawLogsHeader",
			buildID:            buildID,
			headers:            map[string]string{"Accept": "text/plain"},
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				lines, err := model.DownloadLogLines(ctx, tracer, buildID, "")
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				for line := range lines {
					_, err := expectedOut.WriteString(line.Data + "\n")
					require.NoError(t, err)
				}
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
		{
			name:               "HTMLLogsQueryParam",
			buildID:            buildID,
			params:             "html=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				build, err := model.FindBuildByID(ctx, tracer, buildID)
				require.NoError(t, err)
				lines, err := model.DownloadLogLines(ctx, tracer, buildID, "")
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				require.NoError(t, lk.render.HTML(expectedOut, struct {
					LogLines      chan *model.LogLineItem
					BuildID       string
					Builder       string
					TestID        string
					TestName      string
					TaskID        string
					TaskExecution int
				}{lines, build.ID, build.Builder, "", "All logs", build.TaskID, build.TaskExecution}, "base", "test.html"))
				respBytes := resp.Body.Bytes()
				assert.Equal(t, expectedOut.Bytes(), respBytes)
			},
		},
		{
			name:               "Metadata",
			buildID:            buildID,
			params:             "metadata=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				build, err := model.FindBuildByID(ctx, tracer, buildID)
				require.NoError(t, err)

				expectedOut, err := json.MarshalIndent(build, "", "  ")
				require.NoError(t, err)
				assert.Equal(t, expectedOut, resp.Body.Bytes())
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := doReq(t, lk.NewRouter(), http.MethodGet, test.headers, fmt.Sprintf("%s/build/%s/all?%s", lk.opts.URL, test.buildID, test.params), nil)
			require.Equal(t, test.expectedStatusCode, resp.Code)
			checkCORSHeader(t, resp.Header())
			test.test(t, resp)
		})
	}
}

func TestViewTestLogs(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/simple")()

	buildID := "5a75f537726934e4b62833ab6d5dca41"
	testID := "17046404de18d0000000000000000000"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	lk := NewLogkeeper(
		LogkeeperOptions{
			URL:            "https://logkeeper.com",
			MaxRequestSize: testMaxReqSize,
		},
	)
	for _, test := range []struct {
		name               string
		headers            map[string]string
		buildID            string
		testID             string
		params             string
		expectedStatusCode int
		test               func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:               "BuildDNE",
			buildID:            "DNE",
			testID:             testID,
			params:             "raw=true",
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name:               "TestDNE",
			buildID:            buildID,
			testID:             "DNE",
			params:             "raw=true",
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name:               "LobsterRedirect",
			buildID:            buildID,
			testID:             testID,
			expectedStatusCode: http.StatusFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				assert.Equal(t, fmt.Sprintf("/lobster/build/%s/test/%s", buildID, testID), resp.Header().Get("Location"))
			},
		},
		{
			name:               "RawLogsQueryParam",
			buildID:            buildID,
			testID:             testID,
			params:             "raw=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				lines, err := model.DownloadLogLines(ctx, tracer, buildID, testID)
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				for line := range lines {
					_, err := expectedOut.WriteString(line.Data + "\n")
					require.NoError(t, err)
				}
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
		{
			name:               "RawLogsHeader",
			buildID:            buildID,
			testID:             testID,
			headers:            map[string]string{"Accept": "text/plain"},
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				lines, err := model.DownloadLogLines(ctx, tracer, buildID, testID)
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				for line := range lines {
					_, err := expectedOut.WriteString(line.Data + "\n")
					require.NoError(t, err)
				}
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
		{
			name:               "HTMLLogsQueryParam",
			buildID:            buildID,
			testID:             testID,
			params:             "html=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				build, err := model.FindBuildByID(ctx, tracer, buildID)
				require.NoError(t, err)
				test, err := model.FindTestByID(ctx, tracer, buildID, testID)
				require.NoError(t, err)
				lines, err := model.DownloadLogLines(ctx, tracer, buildID, testID)
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				require.NoError(t, lk.render.HTML(expectedOut, struct {
					LogLines      chan *model.LogLineItem
					BuildID       string
					Builder       string
					TestID        string
					TestName      string
					TaskID        string
					TaskExecution int
				}{lines, build.ID, build.Builder, test.ID, test.Name, test.TaskID, test.TaskExecution}, "base", "test.html"))
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
		{
			name:               "Metadata",
			buildID:            buildID,
			testID:             testID,
			params:             "metadata=true",
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				test, err := model.FindTestByID(ctx, tracer, buildID, testID)
				require.NoError(t, err)

				expectedOut, err := json.MarshalIndent(test, "", "  ")
				require.NoError(t, err)
				assert.Equal(t, expectedOut, resp.Body.Bytes())
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := doReq(t, lk.NewRouter(), http.MethodGet, test.headers, fmt.Sprintf("%s/build/%s/test/%s?%s", lk.opts.URL, test.buildID, test.testID, test.params), nil)
			require.Equal(t, test.expectedStatusCode, resp.Code)
			checkCORSHeader(t, resp.Header())
			test.test(t, resp)
		})
	}
}

func doReq(t *testing.T, handler http.Handler, method string, headers map[string]string, url string, body interface{}) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, url, r)
	for key, val := range headers {
		req.Header.Add(key, val)
	}
	req.Header.Add("Origin", "views-test")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func checkCORSHeader(t *testing.T, header http.Header) {
	assert.Equal(t, "*", header.Get("Access-Control-Allow-Origin"))
}
