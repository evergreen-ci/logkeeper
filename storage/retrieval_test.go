package storage

import (
	"context"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindBuildByID(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer cleanTestStorage(t)

	expected := &model.Build{
		Id:       "5a75f537726934e4b62833ab6d5dca41",
		Builder:  "MCI_enterprise-rhel_job0",
		BuildNum: 157865445,
		Info: model.BuildInfo{
			TaskID: "mongodb_mongo_master_enterprise_f98b3361fbab4e02683325cc0e6ebaa69d6af1df_22_07_22_11_24_37",
		},
	}

	actual, err := storage.FindBuildByID(context.Background(), "5a75f537726934e4b62833ab6d5dca41")
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

func TestFindTestByID(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer cleanTestStorage(t)

	expected := &model.Test{
		Id:      model.TestID("17046404DE18D00000000000"),
		BuildId: "5a75f537726934e4b62833ab6d5dca41",
		Name:    "geo_max:CheckReplOplogs",
		Info: model.TestInfo{
			TaskID: "mongodb_mongo_master_enterprise_rhel_80_64_bit_multiversion_all_feature_flags_retryable_writes_downgrade_last_continuous_2_enterprise_f98b3361fbab4e02683325cc0e6ebaa69d6af1df_22_07_22_11_24_37",
		},
		Phase:   "phase0",
		Command: "command0",
	}

	actual, err := storage.FindTestByID(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "17046404DE18D00000000000")
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

func TestFindTestsForBuild(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/between")
	defer cleanTestStorage(t)

	expected := []model.Test{
		{
			Id:      model.TestID("0DE0B6B3BF4AC64000000000"),
			BuildId: "5a75f537726934e4b62833ab6d5dca41",
			Name:    "geo_max:CheckReplOplogs",
			Info: model.TestInfo{
				TaskID: "Task",
			},
			Command: "command0",
			Phase:   "phase0",
		},
		{
			Id:      model.TestID("0DE0B6B3CB36884000000000"),
			BuildId: "5a75f537726934e4b62833ab6d5dca41",
			Name:    "geo_max:CheckReplOplogs2",
			Info: model.TestInfo{
				TaskID: "Task",
			},
			Command: "command1",
			Phase:   "phase1",
		},
	}
	testResponse, err := storage.FindTestsForBuild(context.Background(), "5a75f537726934e4b62833ab6d5dca41")
	require.NoError(t, err)
	assert.Equal(t, expected, testResponse)
}

func TestDownloadLogLines(t *testing.T) {
	for _, test := range []struct {
		name          string
		storagePath   string
		buildID       string
		testID        string
		expectedLines []string
	}{
		{
			name:        "BuildLogsDNE",
			storagePath: "../testdata/simple",
			buildID:     "DNE",
		},
		{
			name:        "TestLogsDNE",
			storagePath: "../testdata/overlapping",
			buildID:     "5a75f537726934e4b62833ab6d5dca41",
			testID:      "DNE",
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
			testID:      "17046404DE18D00000000000",
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
			testID:      "0DE0B6B3BF4AC64000000000",
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
			testID:      "0DE0B6B3BF3B840000000000",
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
			testID:      "0DE0B6B3BF3B840000000000",
			expectedLines: []string{
				"Log401",
				"Log402",
				"Test Log403",
				"Test Log404",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			storage := makeTestStorage(t, test.storagePath)
			defer cleanTestStorage(t)

			logLines, err := storage.DownloadLogLines(context.Background(), test.buildID, test.testID)
			require.NoError(t, err)

			var lines []string
			for item := range logLines {
				lines = append(lines, item.Data)
			}
			assert.Equal(t, test.expectedLines, lines)
		})
	}
}

func TestGetExecutionWindow(t *testing.T) {
	t.Run("NoLaterTest", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		allTestIDs := []model.TestID{
			model.NewTestID(startTime),
		}
		tr := testExecutionWindow(allTestIDs, string(allTestIDs[0]))
		assert.True(t, tr.StartAt.Equal(startTime))
		assert.True(t, tr.EndAt.Equal(TimeRangeMax))
	})

	t.Run("LaterTest", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		allTestIDs := []model.TestID{
			model.NewTestID(startTime),
			model.NewTestID(startTime.Add(time.Hour)),
		}
		tr := testExecutionWindow(allTestIDs, string(allTestIDs[0]))
		assert.True(t, tr.StartAt.Equal(startTime))
		assert.True(t, tr.EndAt.Equal(startTime.Add(time.Hour)))
	})

	t.Run("NoTestID", func(t *testing.T) {
		allTestIDs := []model.TestID{
			model.NewTestID(time.Time{}),
		}
		tr := testExecutionWindow(allTestIDs, "")
		assert.True(t, tr.StartAt.Equal(TimeRangeMin))
		assert.True(t, tr.EndAt.Equal(TimeRangeMax))
	})

	t.Run("NonExistentTestID", func(t *testing.T) {
		allTestIDs := []model.TestID{
			model.NewTestID(time.Time{}),
		}
		tr := testExecutionWindow(allTestIDs, "DNE")
		assert.True(t, tr.StartAt.Equal(TimeRangeMin))
		assert.True(t, tr.EndAt.Equal(TimeRangeMax))
	})
}
