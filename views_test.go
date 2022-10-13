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
	"time"

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
		r.Header.Add("Origin", "test")

		addCORSHeaders(w, r)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
	})
}

func TestCreateBuild(t *testing.T) {
	defer testutil.SetBucket(t, "")()

	type payload struct {
		Builder  string `json:"builder"`
		BuildNum int    `json:"buildnum"`
		TaskID   string `json:"task_id"`
	}
	for _, test := range []struct {
		name               string
		lk                 *logkeeper
		input              interface{}
		expectedStatusCode int
		setup              func(*testing.T)
		test               func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "ExceedsMaxRequestSize",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: 10,
			}),
			input: &payload{
				Builder:  "builder",
				BuildNum: 10,
				TaskID:   "id",
			},
			expectedStatusCode: http.StatusRequestEntityTooLarge,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Equal(t, 10, out.MaxSize)
			},
		},
		{
			name: "InvalidPayload",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			input:              "not JSON",
			expectedStatusCode: http.StatusBadRequest,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "NewBuild",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			input: &payload{
				Builder:  "builder",
				BuildNum: 10,
				TaskID:   "id",
			},
			expectedStatusCode: http.StatusCreated,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				expectedID, err := model.NewBuildID("builder", 10)
				require.NoError(t, err)

				var out createdResponse
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				require.Equal(t, expectedID, out.ID)
				assert.Equal(t, fmt.Sprintf("https://logkeeper.com/build/%s", expectedID), out.URI)

				build, err := model.FindBuildByID(context.TODO(), expectedID)
				require.NoError(t, err)
				assert.Equal(t, expectedID, build.ID)
				assert.Equal(t, "builder", build.Builder)
				assert.Equal(t, 10, build.BuildNum)
				assert.Equal(t, "id", build.TaskID)
			},
		},
		{
			name: "ExistingBuild",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			input: &payload{
				Builder:  "existing",
				BuildNum: 150,
				TaskID:   "id",
			},
			expectedStatusCode: http.StatusOK,
			setup: func(t *testing.T) {
				id, err := model.NewBuildID("existing", 150)
				require.NoError(t, err)
				build := model.Build{
					ID:       id,
					Builder:  "existing",
					BuildNum: 150,
					TaskID:   "id",
				}
				require.NoError(t, build.UploadMetadata(context.TODO()))
			},
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				expectedID, err := model.NewBuildID("existing", 150)
				require.NoError(t, err)

				var out createdResponse
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.Equal(t, expectedID, out.ID)
				assert.Equal(t, fmt.Sprintf("https://logkeeper.com/build/%s", expectedID), out.URI)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.setup != nil {
				test.setup(t)
			}

			resp := doReq(t, test.lk.NewRouter(), http.MethodPost, nil, fmt.Sprintf("%s/build", test.lk.opts.URL), test.input)
			assert.Equal(t, test.expectedStatusCode, resp.Code)
			test.test(t, resp)
		})
	}
}

func TestCreateTest(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/simple")()

	buildID := "5a75f537726934e4b62833ab6d5dca41"
	type payload struct {
		TestFilename string `json:"test_filename"`
		Command      string `json:"command"`
		Phase        string `json:"phase"`
		TaskID       string `json:"task_id"`
	}
	for _, test := range []struct {
		name               string
		lk                 *logkeeper
		buildID            string
		input              interface{}
		expectedStatusCode int
		test               func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "ExceedsMaxRequestSize",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: 10,
			}),
			buildID: buildID,
			input: &payload{
				TestFilename: "test",
				Command:      "command",
				Phase:        "phase",
				TaskID:       "task",
			},
			expectedStatusCode: http.StatusRequestEntityTooLarge,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Equal(t, 10, out.MaxSize)
			},
		},
		{
			name: "InvalidPayload",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            buildID,
			input:              "not JSON",
			expectedStatusCode: http.StatusBadRequest,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "BuildDNE",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID: "DNE",
			input: &payload{
				TestFilename: "test",
				Command:      "command",
				Phase:        "phase",
				TaskID:       "task",
			},
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "NewTest",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID: buildID,
			input: &payload{
				TestFilename: "test",
				Command:      "command",
				Phase:        "phase",
				TaskID:       "id",
			},
			expectedStatusCode: http.StatusCreated,
			test: func(t *testing.T, resp *httptest.ResponseRecorder) {
				var out createdResponse
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				require.NotEmpty(t, out.ID)
				assert.Equal(t, fmt.Sprintf("https://logkeeper.com/build/%s/test/%s", buildID, out.ID), out.URI)

				test, err := model.FindTestByID(context.TODO(), buildID, out.ID)
				require.NoError(t, err)
				assert.Equal(t, out.ID, test.ID)
				assert.Equal(t, "test", test.Name)
				assert.Equal(t, buildID, test.BuildID)
				assert.Equal(t, "id", test.TaskID)
				assert.Equal(t, "phase", test.Phase)
				assert.Equal(t, "command", test.Command)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := doReq(t, test.lk.NewRouter(), http.MethodPost, nil, fmt.Sprintf("%s/build/%s/test", test.lk.opts.URL, test.buildID), test.input)
			assert.Equal(t, test.expectedStatusCode, resp.Code)
			test.test(t, resp)
		})
	}
}

func TestAppendGlobalLog(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/nolines")()

	now := time.Now().UTC()
	buildID := "5a75f537726934e4b62833ab6d5dca41"
	type payload [][]interface{}
	for _, test := range []struct {
		name               string
		lk                 *logkeeper
		buildID            string
		chunks             []interface{}
		expectedStatusCode int
		test               func(*testing.T, *httptest.ResponseRecorder, []interface{})
	}{
		{
			name: "ExceedsMaxRequestSize",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: 10,
			}),
			buildID:            buildID,
			chunks:             []interface{}{payload{{now.Unix(), "Global log line."}}},
			expectedStatusCode: http.StatusRequestEntityTooLarge,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Equal(t, 10, out.MaxSize)
			},
		},
		{
			name: "InvalidPayload",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            buildID,
			chunks:             []interface{}{"invalid"},
			expectedStatusCode: http.StatusBadRequest,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "BuildDNE",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            "DNE",
			chunks:             []interface{}{payload{{now.Unix(), "Global log line."}}},
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "EmptyLines",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            buildID,
			chunks:             []interface{}{payload{}},
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				assert.Equal(t, []byte("\"\""), resp.Body.Bytes())

				lines, err := model.DownloadLogLines(context.TODO(), buildID, "")
				require.NoError(t, err)
				var lineCount int
				for _ = range lines {
					lineCount++
				}
				assert.Zero(t, lineCount)
			},
		},
		{
			name: "MultipleChunks",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID: buildID,
			chunks: []interface{}{
				payload{
					{now.Add(-20 * time.Second).Unix(), "Global log line 0"},
					{now.Add(-15 * time.Second).Unix(), "Global log line 1"},
					{now.Add(-10 * time.Second).Unix(), "Global log line 2"},
				},
				payload{
					{now.Add(-5 * time.Second).Unix(), "Global log line 3"},
					{now.Unix(), "Global log line 4"},
				},
			},
			expectedStatusCode: http.StatusCreated,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, chunks []interface{}) {
				var out createdResponse
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				require.Empty(t, out.ID)
				assert.Equal(t, fmt.Sprintf("https://logkeeper.com/build/%s", buildID), out.URI)

				var expectedLines []model.LogLineItem
				for i := range chunks {
					chunk, ok := chunks[i].(payload)
					require.True(t, ok)
					for _, line := range chunk {
						expectedLines = append(expectedLines, model.LogLineItem{
							Timestamp: time.Unix(line[0].(int64), 0).UTC(),
							Data:      line[1].(string),
							Global:    true,
						})
					}
				}
				lines, err := model.DownloadLogLines(context.TODO(), buildID, "")
				require.NoError(t, err)
				var storedLines []model.LogLineItem
				for line := range lines {
					storedLines = append(storedLines, *line)
				}
				assert.Equal(t, expectedLines, storedLines)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for i, chunk := range test.chunks {
				resp := doReq(t, test.lk.NewRouter(), http.MethodPost, nil, fmt.Sprintf("%s/build/%s", test.lk.opts.URL, test.buildID), chunk)
				require.Equal(t, test.expectedStatusCode, resp.Code)
				test.test(t, resp, test.chunks[0:i+1])
			}
		})
	}
}

func TestAppendTestLog(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/nolines")()

	now := time.Now().UTC()
	buildID := "5a75f537726934e4b62833ab6d5dca41"
	testID := "de0b6b3a764000000000000"
	type payload [][]interface{}
	for _, test := range []struct {
		name               string
		lk                 *logkeeper
		buildID            string
		testID             string
		chunks             []interface{}
		expectedStatusCode int
		test               func(*testing.T, *httptest.ResponseRecorder, []interface{})
	}{
		{
			name: "ExceedsMaxRequestSize",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: 10,
			}),
			buildID:            buildID,
			testID:             testID,
			chunks:             []interface{}{payload{{now.Unix(), "Test log line."}}},
			expectedStatusCode: http.StatusRequestEntityTooLarge,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Equal(t, 10, out.MaxSize)
			},
		},
		{
			name: "InvalidPayload",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            buildID,
			testID:             testID,
			chunks:             []interface{}{"invalid"},
			expectedStatusCode: http.StatusBadRequest,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "BuildDNE",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            "DNE",
			testID:             testID,
			chunks:             []interface{}{payload{{now.Unix(), "Test log line."}}},
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "TestDNE",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            buildID,
			testID:             "DNE",
			chunks:             []interface{}{payload{{now.Unix(), "Test log line."}}},
			expectedStatusCode: http.StatusNotFound,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				var out apiError
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				assert.NotEmpty(t, out.Err)
				assert.Zero(t, out.MaxSize)
			},
		},
		{
			name: "EmptyLines",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID:            buildID,
			testID:             testID,
			chunks:             []interface{}{payload{}},
			expectedStatusCode: http.StatusOK,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, _ []interface{}) {
				assert.Equal(t, []byte("\"\""), resp.Body.Bytes())

				lines, err := model.DownloadLogLines(context.TODO(), buildID, testID)
				require.NoError(t, err)
				var lineCount int
				for _ = range lines {
					lineCount++
				}
				assert.Zero(t, lineCount)
			},
		},
		{
			name: "MultipleChunks",
			lk: NewLogkeeper(NewLogkeeperOptions{
				URL:            "https://logkeeper.com",
				MaxRequestSize: testMaxReqSize,
			}),
			buildID: buildID,
			testID:  testID,
			chunks: []interface{}{
				payload{
					{now.Add(-20 * time.Second).Unix(), "Test log line 0"},
					{now.Add(-15 * time.Second).Unix(), "Test log line 1"},
					{now.Add(-10 * time.Second).Unix(), "Test log line 2"},
				},
				payload{
					{now.Add(-5 * time.Second).Unix(), "Test log line 3"},
					{now.Unix(), "Test log line 4"},
				},
			},
			expectedStatusCode: http.StatusCreated,
			test: func(t *testing.T, resp *httptest.ResponseRecorder, chunks []interface{}) {
				var out createdResponse
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
				require.Empty(t, out.ID)
				assert.Equal(t, fmt.Sprintf("https://logkeeper.com/build/%s/test/%s", buildID, testID), out.URI)

				var expectedLines []model.LogLineItem
				for i := range chunks {
					chunk, ok := chunks[i].(payload)
					require.True(t, ok)
					for _, line := range chunk {
						expectedLines = append(expectedLines, model.LogLineItem{
							Timestamp: time.Unix(line[0].(int64), 0).UTC(),
							Data:      line[1].(string),
						})
					}
				}
				lines, err := model.DownloadLogLines(context.TODO(), buildID, testID)
				require.NoError(t, err)
				var storedLines []model.LogLineItem
				for line := range lines {
					storedLines = append(storedLines, *line)
				}
				assert.Equal(t, expectedLines, storedLines)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for i, chunk := range test.chunks {
				resp := doReq(t, test.lk.NewRouter(), http.MethodPost, nil, fmt.Sprintf("%s/build/%s/test/%s", test.lk.opts.URL, test.buildID, test.testID), chunk)
				require.Equal(t, test.expectedStatusCode, resp.Code)
				test.test(t, resp, test.chunks[0:i+1])
			}
		})
	}
}

func TestViewBuild(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/simple")()

	buildID := "5a75f537726934e4b62833ab6d5dca41"
	lk := NewLogkeeper(NewLogkeeperOptions{
		URL:            "https://logkeeper.com",
		MaxRequestSize: testMaxReqSize,
	})
	for _, test := range []struct {
		name               string
		buildID            string
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
				build, err := model.FindBuildByID(context.TODO(), buildID)
				require.NoError(t, err)
				tests, err := model.FindTestsForBuild(context.TODO(), buildID)
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				require.NoError(t, lk.render.HTML(expectedOut, struct {
					Build *model.Build
					Tests []model.Test
				}{build, tests}, "base", "build.html"))
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := doReq(t, lk.NewRouter(), http.MethodGet, nil, fmt.Sprintf("%s/build/%s", lk.opts.URL, test.buildID), nil)
			require.Equal(t, test.expectedStatusCode, resp.Code)
			checkCORSHeader(t, resp.Header())
			test.test(t, resp)
		})
	}
}

func TestViewAllLogs(t *testing.T) {
	defer testutil.SetBucket(t, "testdata/simple")()

	buildID := "5a75f537726934e4b62833ab6d5dca41"
	lk := NewLogkeeper(NewLogkeeperOptions{
		URL:            "https://logkeeper.com",
		MaxRequestSize: testMaxReqSize,
	})
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
				lines, err := model.DownloadLogLines(context.TODO(), buildID, "")
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
				lines, err := model.DownloadLogLines(context.TODO(), buildID, "")
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
				build, err := model.FindBuildByID(context.TODO(), buildID)
				require.NoError(t, err)
				lines, err := model.DownloadLogLines(context.TODO(), buildID, "")
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				require.NoError(t, lk.render.HTML(expectedOut, struct {
					LogLines chan *model.LogLineItem
					BuildID  string
					Builder  string
					TestID   string
					TestName string
					TaskID   string
				}{lines, build.ID, build.Builder, "", "All logs", build.TaskID}, "base", "test.html"))
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
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
	lk := NewLogkeeper(NewLogkeeperOptions{
		URL:            "https://logkeeper.com",
		MaxRequestSize: testMaxReqSize,
	})
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
				lines, err := model.DownloadLogLines(context.TODO(), buildID, testID)
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
				lines, err := model.DownloadLogLines(context.TODO(), buildID, testID)
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
				build, err := model.FindBuildByID(context.TODO(), buildID)
				require.NoError(t, err)
				test, err := model.FindTestByID(context.TODO(), buildID, testID)
				lines, err := model.DownloadLogLines(context.TODO(), buildID, testID)
				require.NoError(t, err)

				expectedOut := &bytes.Buffer{}
				require.NoError(t, lk.render.HTML(expectedOut, struct {
					LogLines chan *model.LogLineItem
					BuildID  string
					Builder  string
					TestID   string
					TestName string
					TaskID   string
				}{lines, build.ID, build.Builder, test.ID, test.Name, test.TaskID}, "base", "test.html"))
				assert.Equal(t, expectedOut.Bytes(), resp.Body.Bytes())
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
