package storage

import (
	"context"
	"testing"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func TestGetTestLogLines(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer cleanTestStorage(t)
	channel, err := storage.GetTestLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "62dba0159041307f697e6ccc")
	require.NoError(t, err)

	// We should have the one additional intersecting line from the global logs and an additional one after
	const expectedCount = 13
	lines := []string{}

	for item := range channel {
		lines = append(lines, item.Data)
	}

	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, "I am a global log within the test start/stop ranges.", lines[2])
}

func TestGetTestLogLinesInBetween(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/between")
	defer cleanTestStorage(t)
	channel, err := storage.GetTestLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "62dba0159041307f697e6ccc")
	require.NoError(t, err)

	const expectedCount = 4
	expectedLines := []string{
		"Test Log401",
		"Test Log402",
		// We should include the test logs and global logs that are before the next test
		"Log501",
		"Log502",
	}
	lines := []string{}
	for item := range channel {
		lines = append(lines, item.Data)
	}

	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, expectedLines, lines)
}

func TestGetTestLogLinesOverlapping(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/overlapping")
	defer cleanTestStorage(t)
	channel, err := storage.GetTestLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "62dba0159041307f697e6ccc")
	require.NoError(t, err)

	// We should have all global logs that overlap our test and all logs after, since there is
	// not a next test
	const expectedCount = 35
	expectedLines := []string{
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
	}
	lines := []string{}
	for item := range channel {
		lines = append(lines, item.Data)
	}

	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, expectedLines, lines)
}

func TestGetAllLogLinesOverlapping(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/overlapping")
	defer cleanTestStorage(t)
	channel, err := storage.GetAllLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41")
	require.NoError(t, err)

	const expectedCount = 40
	expectedLines := []string{
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
	}
	lines := []string{}
	for item := range channel {
		lines = append(lines, item.Data)
	}
	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, expectedLines, lines)
}

func TestFindBuildById(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer cleanTestStorage(t)

	expected := model.Build{
		Id:       "5a75f537726934e4b62833ab6d5dca41",
		Builder:  "MCI_enterprise-rhel_job0",
		BuildNum: 157865445,
		Info: model.BuildInfo{
			TaskID: "mongodb_mongo_master_enterprise_f98b3361fbab4e02683325cc0e6ebaa69d6af1df_22_07_22_11_24_37",
		},
	}
	buildResponse, err := storage.FindBuildByID(context.Background(), "5a75f537726934e4b62833ab6d5dca41")
	require.NoError(t, err)
	assert.Equal(t, &expected, buildResponse)
}

func TestFindTestById(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer cleanTestStorage(t)

	expected := model.Test{
		Id:      bson.ObjectIdHex("62dba0159041307f697e6ccc"),
		BuildId: "5a75f537726934e4b62833ab6d5dca41",
		Name:    "geo_max:CheckReplOplogs",
		Info: model.TestInfo{
			TaskID: "mongodb_mongo_master_enterprise_rhel_80_64_bit_multiversion_all_feature_flags_retryable_writes_downgrade_last_continuous_2_enterprise_f98b3361fbab4e02683325cc0e6ebaa69d6af1df_22_07_22_11_24_37",
		},
		Phase:   "phase0",
		Command: "command0",
	}
	testResponse, err := storage.FindTestByID(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "62dba0159041307f697e6ccc")
	require.NoError(t, err)
	assert.Equal(t, &expected, testResponse)
}
