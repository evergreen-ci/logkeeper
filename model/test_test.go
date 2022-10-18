package model

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func TestNewTestID(t *testing.T) {
	assert.True(t, strings.HasPrefix(NewTestID(time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)), "1174efedab186000"))
}

func TestTestID(t *testing.T) {
	t.Run("ObjectID", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 1, time.UTC)
		objectID := bson.NewObjectIdWithTime(startTime)
		newID := objectID.Hex()
		assert.True(t, startTime.Equal(testIDTimestamp(newID).Add(time.Nanosecond)))
	})

	t.Run("TestID", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 1, time.UTC)
		newID := NewTestID(startTime)
		assert.True(t, startTime.Equal(testIDTimestamp(newID)))
	})
}

func TestUploadTestMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer testutil.SetBucket(t, "")()
	test := Test{
		ID:        "62dba0159041307f697e6ccc",
		Name:      "test0",
		BuildID:   "5a75f537726934e4b62833ab6d5dca41",
		TaskID:    "t0",
		Execution: "0",
		Phase:     "phase0",
		Command:   "command0",
	}
	expectedData, err := test.toJSON()
	require.NoError(t, err)
	require.NoError(t, test.UploadTestMetadata(ctx))

	r, err := env.Bucket().Get(ctx, "/builds/5a75f537726934e4b62833ab6d5dca41/tests/62dba0159041307f697e6ccc/metadata.json")
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

func TestTestKey(t *testing.T) {
	test := Test{
		ID:        "test0",
		Name:      "name",
		BuildID:   "build0",
		TaskID:    "t0",
		Execution: "0",
		Phase:     "phase0",
		Command:   "command0",
	}
	assert.Equal(t, "builds/build0/tests/test0/metadata.json", test.key())
}

func TestTestToJSON(t *testing.T) {
	test := Test{
		ID:        "test0",
		Name:      "name",
		BuildID:   "build0",
		TaskID:    "t0",
		Execution: "0",
		Phase:     "phase0",
		Command:   "command0",
	}
	data, err := test.toJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"test0","name":"name","build_id":"build0","task_id":"t0","task_execution":"0","phase":"phase0","command":"command0"}`, string(data))
}

func TestCheckTestMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer testutil.SetBucket(t, "../testdata/simple")()

	for _, test := range []struct {
		name     string
		buildID  string
		testID   string
		expected bool
	}{
		{
			name:     "BuildExists",
			buildID:  "5a75f537726934e4b62833ab6d5dca41",
			expected: true,
		},
		{
			name:     "TestExists",
			buildID:  "5a75f537726934e4b62833ab6d5dca41",
			testID:   "17046404de18d0000000000000000000",
			expected: true,
		},
		{
			name:     "BuildDNE",
			buildID:  "DNE",
			expected: false,
		},
		{
			name:     "BuildExistsTestDNE",
			buildID:  "5a75f537726934e4b62833ab6d5dca41",
			testID:   "DNE",
			expected: false,
		},
		{
			name:     "BuildDNETestDNE",
			buildID:  "DNE",
			testID:   "DNE",
			expected: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := CheckTestMetadata(ctx, test.buildID, test.testID)
			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestFindTestByID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer testutil.SetBucket(t, "../testdata/simple")()
	t.Run("Exists", func(t *testing.T) {
		expected := &Test{
			ID:        "17046404de18d0000000000000000000",
			BuildID:   "5a75f537726934e4b62833ab6d5dca41",
			Name:      "geo_max:CheckReplOplogs",
			TaskID:    "mongodb_mongo_master_enterprise_rhel_80_64_bit_multiversion_all_feature_flags_retryable_writes_downgrade_last_continuous_2_enterprise_f98b3361fbab4e02683325cc0e6ebaa69d6af1df_22_07_22_11_24_37",
			Execution: "0",
			Phase:     "phase0",
			Command:   "command0",
		}
		actual, err := FindTestByID(ctx, "5a75f537726934e4b62833ab6d5dca41", "17046404de18d0000000000000000000")
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	})
	t.Run("BuildDNE", func(t *testing.T) {
		test, err := FindTestByID(ctx, "DNE", "17046404de18d0000000000000000000")
		require.NoError(t, err)
		assert.Nil(t, test)
	})
	t.Run("TestDNE", func(t *testing.T) {
		test, err := FindTestByID(ctx, "5a75f537726934e4b62833ab6d5dca41", "DNE")
		require.NoError(t, err)
		assert.Nil(t, test)
	})
}

func TestFindTestsForBuild(t *testing.T) {
	defer testutil.SetBucket(t, "../testdata/between")()

	expected := []Test{
		{
			ID:        "0de0b6b3bf4ac6400000000000000000",
			BuildID:   "5a75f537726934e4b62833ab6d5dca41",
			Name:      "geo_max:CheckReplOplogs",
			TaskID:    "Task",
			Execution: "0",
			Command:   "command0",
			Phase:     "phase0",
		},
		{
			ID:        "0de0b6b3cb3688400000000000000000",
			BuildID:   "5a75f537726934e4b62833ab6d5dca41",
			Name:      "geo_max:CheckReplOplogs2",
			TaskID:    "Task",
			Execution: "1",
			Command:   "command1",
			Phase:     "phase1",
		},
	}
	testResponse, err := FindTestsForBuild(context.Background(), "5a75f537726934e4b62833ab6d5dca41")
	require.NoError(t, err)
	assert.Equal(t, expected, testResponse)
}

func TestTestExecutionWindow(t *testing.T) {
	t.Run("NoLaterTest", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		allTestIDs := []string{
			NewTestID(startTime),
		}
		tr, err := testExecutionWindow(allTestIDs, allTestIDs[0])
		assert.NoError(t, err)
		assert.True(t, tr.StartAt.Equal(startTime))
		assert.True(t, tr.EndAt.Equal(TimeRangeMax))
	})

	t.Run("LaterTest", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		allTestIDs := []string{
			NewTestID(startTime),
			NewTestID(startTime.Add(time.Hour)),
		}
		tr, err := testExecutionWindow(allTestIDs, allTestIDs[0])
		assert.NoError(t, err)
		assert.True(t, tr.StartAt.Equal(startTime))
		assert.True(t, tr.EndAt.Equal(startTime.Add(time.Hour)))
	})

	t.Run("NanosecondsTruncated", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 1000001, time.UTC)
		allTestIDs := []string{
			NewTestID(startTime),
		}
		tr, err := testExecutionWindow(allTestIDs, allTestIDs[0])
		assert.NoError(t, err)
		assert.True(t, tr.StartAt.Equal(time.Date(2009, time.November, 10, 23, 0, 0, 1000000, time.UTC)))
		assert.True(t, tr.EndAt.Equal(TimeRangeMax))
	})

	t.Run("NoTestID", func(t *testing.T) {
		allTestIDs := []string{
			NewTestID(time.Time{}),
		}
		tr, err := testExecutionWindow(allTestIDs, "")
		assert.NoError(t, err)
		assert.True(t, tr.StartAt.Equal(TimeRangeMin))
		assert.True(t, tr.EndAt.Equal(TimeRangeMax))
	})

	t.Run("NonExistentTestID", func(t *testing.T) {
		allTestIDs := []string{
			NewTestID(time.Time{}),
		}
		_, err := testExecutionWindow(allTestIDs, "DNE")
		assert.Error(t, err)
	})
}

func TestParseTestIDs(t *testing.T) {
	for name, testCase := range map[string]struct {
		keys          []string
		expectedIDs   []string
		errorExpected bool
	}{
		"EmptyList": {
			keys:        []string{},
			expectedIDs: []string{},
		},
		"NoMetadata": {
			keys:        []string{"key1", "key2"},
			expectedIDs: []string{},
		},
		"MalformedMetadata": {
			keys:          []string{"asdfgh/tests/0de0b6b3Bf4ac6400000000000000000/metadata.json"},
			errorExpected: true,
		},
		"MetadataAndLogChunk": {
			keys: []string{
				"builds/asdfgh/tests/0de0b6b3Bf4ac6400000000000000000/metadata.json",
				"builds/asdfgh/tests/0de0b6b3Bf4ac6400000000000000000/1000000000301000000_1000000000302000000_2",
			},
			expectedIDs: []string{"0de0b6b3Bf4ac6400000000000000000"},
		},
		"Sorted": {
			keys: []string{
				"builds/asdfgh/tests/0de0b6b3cb3688400000000000000000/metadata.json",
				"builds/asdfgh/tests/0de0b6b3Bf4ac6400000000000000000/metadata.json",
			},
			expectedIDs: []string{"0de0b6b3Bf4ac6400000000000000000", "0de0b6b3cb3688400000000000000000"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			testIDs, err := parseTestIDs(testCase.keys)
			if testCase.errorExpected {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, testCase.expectedIDs, testIDs)
			}
		})
	}
}
