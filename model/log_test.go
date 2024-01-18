package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go.opentelemetry.io/otel"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/evergreen-ci/utility"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalLogJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	t.Run("NoInput", func(t *testing.T) {
		lines, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader(""))
		assert.Error(t, err)
		require.Len(t, lines, 0)
	})

	t.Run("EmptyLines", func(t *testing.T) {
		lines, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader("[]"))
		assert.NoError(t, err)
		require.Len(t, lines, 0)
	})

	t.Run("WellFormedLines", func(t *testing.T) {
		logLineJSON := "[[1257894000, \"message0\"],[1257894001, \"message1\"]]"
		lines, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader(logLineJSON))
		assert.NoError(t, err)
		require.Len(t, lines, 2)
		assert.Equal(t, "message0", lines[0].Data)
		assert.True(t, lines[0].Timestamp.Equal(time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)))
		assert.Equal(t, "message1", lines[1].Data)
		assert.True(t, lines[1].Timestamp.Equal(time.Date(2009, time.November, 10, 23, 0, 1, 0, time.UTC)))
	})

	t.Run("MalformedJSON", func(t *testing.T) {
		logLineJSON := "[[1257894000, \"message0\"]}"
		_, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader(logLineJSON))
		assert.Error(t, err)
	})

	t.Run("UnexpectedTimestampType", func(t *testing.T) {
		logLineJSON := "[[\"not a date\", \"message0\"]]"
		_, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader(logLineJSON))
		assert.Error(t, err)
	})

	t.Run("UnexpectedDataType", func(t *testing.T) {
		logLineJSON := "[[1257894000, true]]"
		_, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader(logLineJSON))
		assert.Error(t, err)
	})

	t.Run("UnexpectedExtraArray", func(t *testing.T) {
		logLineJSON := "[[1257894000, \"message0\"]], [\"unexpected\"]"
		_, err := UnmarshalLogJSON(ctx, tracer, strings.NewReader(logLineJSON))
		assert.Error(t, err)
	})
}

func TestLogChunkInfoKey(t *testing.T) {
	t.Run("WithTest", func(t *testing.T) {
		info := LogChunkInfo{
			BuildID:  "b0",
			TestID:   "t0",
			NumLines: 1,
			Start:    time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC),
			End:      time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC),
		}
		key := info.key()
		require.Equal(t, "builds/b0/tests/t0/1257894000000000000_1257894060000000000_1", key)

		newInfo := LogChunkInfo{}
		require.NoError(t, newInfo.fromKey(key))
		assert.Equal(t, info, newInfo)

		parsedTestID, err := testIDFromKey(key)
		require.NoError(t, err)
		assert.Equal(t, info.TestID, parsedTestID)
	})
	t.Run("WithoutTest", func(t *testing.T) {
		info := LogChunkInfo{
			BuildID:  "b0",
			NumLines: 1,
			Start:    time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC),
			End:      time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC),
		}
		key := info.key()
		require.Equal(t, "builds/b0/1257894000000000000_1257894060000000000_1", key)

		newInfo := LogChunkInfo{}
		require.NoError(t, newInfo.fromKey(key))
		assert.Equal(t, info, newInfo)

		_, err := testIDFromKey(key)
		assert.Error(t, err)
	})
}

func TestFromKey(t *testing.T) {
	t.Run("InvalidKey", func(t *testing.T) {
		newInfo := LogChunkInfo{}
		assert.NotPanics(t, func() {
			err := newInfo.fromKey("asdf")
			assert.Error(t, err)
		})

	})
}

func TestMakeLogLineString(t *testing.T) {
	result := makeLogLineStrings(LogLineItem{
		Data:      "a\nb",
		Timestamp: time.Unix(1661354966, 0),
	})
	assert.Equal(t, []string{"  0       1661354966000a\n", "  0       1661354966000b\n"}, result)
}

func TestDownloadLogLines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	for _, test := range []struct {
		name          string
		storagePath   string
		buildID       string
		testID        string
		expectedLines []string
		errorExpected bool
	}{
		{
			name:          "BuildLogsDNE",
			storagePath:   "../testdata/simple",
			buildID:       "DNE",
			errorExpected: true,
		},
		{
			name:          "TestLogsDNE",
			storagePath:   "../testdata/overlapping",
			buildID:       "5a75f537726934e4b62833ab6d5dca41",
			testID:        "DNE",
			errorExpected: true,
			expectedLines: []string{
				"Log300",
				"Log320",
				"Log340",
				"Log360",
				"Log380",
				"Log400",
				"Log420",
				"Log440",
				"Log460",
				"Log500",
				"Log501",
				"Log520",
				"Log540",
				"Log560",
				"Log580",
				"Log810",
				"Log820",
				"Log840",
				"Log860",
				"Log900",
			},
		},
		{
			name:        "TestLogsSingleTest",
			storagePath: "../testdata/simple",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			testID:      "17046404de18d0000000000000000000",
			expectedLines: []string{
				"First Test Log Line",
				"[js_test:geo_max:CheckReplOplogs] New session started with sessionID: {  \"id\" : UUID(\"4983fd5c-898a-4435-8523-2aef47ce91f3\") } and options: {  \"causalConsistency\" : false }",
				"I am a global log within the test start/stop ranges.",
				"[js_test:geo_max:CheckReplOplogs] Recreating replica set from config {",
				"[js_test:geo_max:CheckReplOplogs] \\t\"_id\" : \"rs\",",
				"[js_test:geo_max:CheckReplOplogs] \\t\"version\" : 5,",
				"[js_test:geo_max:CheckReplOplogs] \\t\"term\" : 3,",
				"[js_test:geo_max:CheckReplOplogs] \\t\"members\" : [",
				"[js_test:geo_max:CheckReplOplogs] \\t\\t{",
				"[js_test:geo_max:CheckReplOplogs] \\t\\t\\t\"_id\" : 0,",
				"[js_test:geo_max:CheckReplOplogs] \\t\\t\\t\"host\" : \"localhost:20000\",",
				"Last Test Log Line",
				"[j0:n1] {\"t\":{\"$date\":\"2022-07-23T07:15:35.740+00:00\"},\"s\":\"D2\", \"c\":\"REPL_HB\",  \"id\":4615618, \"ctx\":\"ReplCoord-9\",\"msg\":\"Scheduling heartbeat\",\"attr\":{\"target\":\"localhost:20000\",\"when\":{\"$date\":\"2022-07-23T07:15:37.740Z\"}}}",
			},
		},
		{
			name:        "TestLogsBetweenMultpleTests",
			storagePath: "../testdata/between",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			testID:      "0de0b6b3bf4ac6400000000000000000",
			expectedLines: []string{
				"Test Log401",
				"Test Log402",
				"Log501",
				"Log502",
			},
		},
		{
			name:        "TestLogsWithOverlappingGlobalLogs",
			storagePath: "../testdata/overlapping",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			testID:      "0de0b6b3bf3b84000000000000000000",
			expectedLines: []string{
				"Test Log400",
				"Log400",
				"Test Log420",
				"Log420",
				"Test Log440",
				"Log440",
				"Test Log460",
				"Log460",
				"Test Log480",
				"Log500",
				"Test Log500",
				"Log501",
				"Test Log520",
				"Log520",
				"Test Log540",
				"Log540",
				"Test Log560",
				"Log560",
				"Log580",
				"Test Log600",
				"Test Log601",
				"Test Log620",
				"Test Log640",
				"Test Log660",
				"Test Log680",
				"Test Log700",
				"Test Log720",
				"Test Log740",
				"Test Log760",
				"Test Log800",
				"Log810",
				"Log820",
				"Log840",
				"Log860",
				"Log900",
			},
		},
		{
			name:        "AllLogs",
			storagePath: "../testdata/overlapping",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			expectedLines: []string{
				"Log300",
				"Log320",
				"Log340",
				"Log360",
				"Log380",
				"Test Log400",
				"Log400",
				"Test Log420",
				"Log420",
				"Test Log440",
				"Log440",
				"Test Log460",
				"Log460",
				"Test Log480",
				"Log500",
				"Test Log500",
				"Log501",
				"Test Log520",
				"Log520",
				"Test Log540",
				"Log540",
				"Test Log560",
				"Log560",
				"Log580",
				"Test Log600",
				"Test Log601",
				"Test Log620",
				"Test Log640",
				"Test Log660",
				"Test Log680",
				"Test Log700",
				"Test Log720",
				"Test Log740",
				"Test Log760",
				"Test Log800",
				"Log810",
				"Log820",
				"Log840",
				"Log860",
				"Log900",
			},
		},
		{
			name:        "TestLogsStartAfterBuildLogs",
			storagePath: "../testdata/delayed",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			testID:      "0de0b6b3bf3b84000000000000000000",
			expectedLines: []string{
				"Log401",
				"Log402",
				"Test Log403",
				"Test Log404",
			},
		},
		{
			name:        "TestWithNanosecondPrecision",
			storagePath: "../testdata/precision",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			testID:      "17046404de28123f0000000000000000",
			expectedLines: []string{
				"First Test Log Line",
				"Global log within the test start/stop ranges",
				"Middle Test Log Line",
				"Last Test Log Line",
				"Global log after test logging ends",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer testutil.SetBucket(t, test.storagePath)()

			logLines, err := DownloadLogLines(ctx, tracer, test.buildID, test.testID)
			if test.errorExpected {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)

				var lines []string
				for item := range logLines {
					lines = append(lines, item.Data)
				}
				assert.Equal(t, test.expectedLines, lines)
			}
		})
	}
}

func TestInsertLogLines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop

	testLines := []LogLineItem{
		{
			Timestamp: time.Unix(1000000000, 0).UTC(),
			Data:      "line0",
		},
		{
			Timestamp: time.Unix(1000000001, 0).UTC(),
			Data:      "line1",
		},
		{
			Timestamp: time.Unix(1000000002, 0).UTC(),
			Data:      "line2",
		},
		{
			Timestamp: time.Unix(1000000003, 0).UTC(),
			Data:      "line3",
		},
		{
			Timestamp: time.Unix(1000000004, 0).UTC(),
			Data:      "line4",
		},
		{
			Timestamp: time.Unix(1000000005, 0).UTC(),
			Data:      "line5",
		},
	}

	globalLines := []LogLineItem{
		{
			Timestamp: time.Unix(1000000000, 0).UTC(),
			Data:      "line0",
			Global:    true,
		},
		{
			Timestamp: time.Unix(1000000001, 0).UTC(),
			Data:      "line1",
			Global:    true,
		},
		{
			Timestamp: time.Unix(1000000002, 0).UTC(),
			Data:      "line2",
			Global:    true,
		},
		{
			Timestamp: time.Unix(1000000003, 0).UTC(),
			Data:      "line3",
			Global:    true,
		},
		{
			Timestamp: time.Unix(1000000004, 0).UTC(),
			Data:      "line4",
			Global:    true,
		},
		{
			Timestamp: time.Unix(1000000005, 0).UTC(),
			Data:      "line5",
			Global:    true,
		},
	}
	expectedStorage := newExpectedChunk("1000000000000000000_1000000005000000000_6", []string{
		"  0       1000000000000line0\n",
		"  0       1000000001000line1\n",
		"  0       1000000002000line2\n",
		"  0       1000000003000line3\n",
		"  0       1000000004000line4\n",
		"  0       1000000005000line5\n",
	})

	buildID := "5a75f537726934e4b62833ab6d5dca41"

	t.Run("Global", func(t *testing.T) {
		defer testutil.SetBucket(t, "nolines")()
		require.NoError(t, InsertLogLines(ctx, tracer, buildID, "", globalLines, 4*1024*1024))
		verifyDataStorage(t, fmt.Sprintf("/builds/%s/", buildID), expectedStorage)

		logsChannel, err := DownloadLogLines(ctx, tracer, buildID, "")
		require.NoError(t, err)
		var result []LogLineItem
		for item := range logsChannel {
			result = append(result, *item)
		}

		assert.Equal(t, globalLines, result)
	})
	t.Run("Test", func(t *testing.T) {
		defer testutil.SetBucket(t, "nolines")()
		testID := "DE0B6B3A764000000000000"
		require.NoError(t, (&Test{
			ID:      testID,
			BuildID: "5a75f537726934e4b62833ab6d5dca41",
		}).UploadTestMetadata(ctx, tracer))
		require.NoError(t, InsertLogLines(ctx, tracer, buildID, testID, testLines, 4*1024*1024))

		verifyDataStorage(t, fmt.Sprintf("/builds/%s/tests/%s/", buildID, testID), expectedStorage)

		logsChannel, err := DownloadLogLines(ctx, tracer, buildID, testID)
		require.NoError(t, err)
		var result []LogLineItem
		for item := range logsChannel {
			result = append(result, *item)
		}
		assert.Equal(t, testLines, result)
	})
}

type expectedChunk struct {
	filename string
	body     string
}

func newExpectedChunk(filename string, lines []string) expectedChunk {
	return expectedChunk{
		filename: filename,
		body:     strings.Join(lines, ""),
	}
}

func verifyDataStorage(t *testing.T, prefix string, expectedStorage expectedChunk) {
	actualChunkStream, err := env.Bucket().Get(context.Background(), fmt.Sprintf("%s%s", prefix, expectedStorage.filename))
	require.NoError(t, err)

	actualChunkBody, err := io.ReadAll(actualChunkStream)
	require.NoError(t, err)
	assert.Equal(t, expectedStorage.body, string(actualChunkBody))
}

func benchmarkReadLogJSON(lineCount, lineSize int, b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	sampleJSON := makeJSONSample(lineCount, lineSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalLogJSON(ctx, tracer, bytes.NewReader(sampleJSON))
		if err != nil {
			b.Fatalf("unmarshal encountered error: '%s'", err)
		}
	}
}

func makeJSONSample(lineCount, lineSize int) []byte {
	var sample [][]interface{}
	startTime := time.Date(2009, time.November, 10, 23, 0, 0, 1, time.UTC)
	for i := 0; i < lineCount; i++ {
		sample = append(sample, []interface{}{
			utility.ToPythonTime(startTime.Add(time.Duration(i) * time.Second)),
			utility.MakeRandomString(lineSize),
		})
	}

	jsonSample, _ := json.Marshal(sample)

	return jsonSample
}

func BenchmarkReadLogJSONShort(b *testing.B)                  { benchmarkReadLogJSON(100, 100, b) }
func BenchmarkReadLogJSONFewLongLines(b *testing.B)           { benchmarkReadLogJSON(100, 100000, b) }
func BenchmarkReadLogJSONManyShortLines(b *testing.B)         { benchmarkReadLogJSON(100000, 100, b) }
func BenchmarkReadLogJSONMaxLogSizeAverageLines(b *testing.B) { benchmarkReadLogJSON(32*1024, 1024, b) }
func BenchmarkReadLogJSONMaxLogSizeMaxLineSize(b *testing.B) {
	benchmarkReadLogJSON(8, 4*1024*1024, b)
}
