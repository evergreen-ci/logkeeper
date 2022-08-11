package storage

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func TestUploadBuildMetadata(t *testing.T) {
	storage := makeTestStorage(t, "")
	defer cleanTestStorage(t)

	build := model.Build{
		Id:       "5a75f537726934e4b62833ab6d5dca41",
		Builder:  "builder0",
		BuildNum: 1,
		Info:     model.BuildInfo{TaskID: "t0"},
	}

	assert.NoError(t, storage.UploadBuildMetadata(context.Background(), build))
	results, err := storage.Get(context.Background(), "/builds/5a75f537726934e4b62833ab6d5dca41/metadata.json")
	assert.NoError(t, err)
	contents, err := io.ReadAll(results)
	assert.NoError(t, err)

	expectedMetadata := `{"id":"5a75f537726934e4b62833ab6d5dca41","builder":"builder0","buildnum":1,"task_id":"t0"}`
	assert.JSONEq(t, expectedMetadata, string(contents))
}

func TestUploadTestMetadata(t *testing.T) {
	storage := makeTestStorage(t, "")
	defer cleanTestStorage(t)

	test := model.Test{
		Id:      bson.ObjectIdHex("62dba0159041307f697e6ccc"),
		BuildId: "5a75f537726934e4b62833ab6d5dca41",
		Name:    "test0",
		Info:    model.TestInfo{TaskID: "t0"},
	}

	assert.NoError(t, storage.UploadTestMetadata(context.Background(), test))
	results, err := storage.Get(context.Background(), "/builds/5a75f537726934e4b62833ab6d5dca41/tests/62dba0159041307f697e6ccc/metadata.json")
	assert.NoError(t, err)
	contents, err := io.ReadAll(results)
	assert.NoError(t, err)

	expectedMetadata := `{"id":"62dba0159041307f697e6ccc","name":"test0","build_id":"5a75f537726934e4b62833ab6d5dca41","task_id":"t0"}`
	assert.JSONEq(t, expectedMetadata, string(contents))
}

func TestInsertLogChunks(t *testing.T) {
	uploadChunks := []model.LogChunk{
		{
			{
				Time: time.Unix(1000000000, 0),
				Msg:  "line0",
			},
			{
				Time: time.Unix(1000000001, 0),
				Msg:  "line1",
			},
			{
				Time: time.Unix(1000000002, 0),
				Msg:  "line2",
			},
		},
		{
			{
				Time: time.Unix(1000000003, 0),
				Msg:  "line3",
			},
			{
				Time: time.Unix(1000000004, 0),
				Msg:  "line4",
			},
			{
				Time: time.Unix(1000000005, 0),
				Msg:  "line5",
			},
		},
	}

	expected := []model.LogLineItem{
		{
			LineNum:   0,
			Timestamp: time.Unix(1000000000, 0).UTC(),
			Data:      "line0",
		},
		{
			LineNum:   0,
			Timestamp: time.Unix(1000000001, 0).UTC(),
			Data:      "line1",
		},
		{
			LineNum:   0,
			Timestamp: time.Unix(1000000002, 0).UTC(),
			Data:      "line2",
		},
		{
			LineNum:   0,
			Timestamp: time.Unix(1000000003, 0).UTC(),
			Data:      "line3",
		},
		{
			LineNum:   0,
			Timestamp: time.Unix(1000000004, 0).UTC(),
			Data:      "line4",
		},
		{
			LineNum:   0,
			Timestamp: time.Unix(1000000005, 0).UTC(),
			Data:      "line5",
		},
	}
	buildID := "5a75f537726934e4b62833ab6d5dca41"

	t.Run("Global", func(t *testing.T) {
		storage := makeTestStorage(t, "nolines")
		defer cleanTestStorage(t)

		err := storage.InsertLogChunks(context.Background(), buildID, "", uploadChunks)
		require.NoError(t, err)

		logsChannel, err := storage.GetAllLogLines(context.Background(), buildID)
		require.NoError(t, err)

		result := []model.LogLineItem{}

		for item := range logsChannel {
			result = append(result, *item)
		}

		assert.Equal(t, expected, result)
	})

	t.Run("Test", func(t *testing.T) {
		storage := makeTestStorage(t, "nolines")
		defer cleanTestStorage(t)

		testID := "62dba0159041307f697e6ccc"

		err := storage.InsertLogChunks(context.Background(), buildID, testID, uploadChunks)
		require.NoError(t, err)

		logsChannel, err := storage.GetTestLogLines(context.Background(), buildID, testID)
		require.NoError(t, err)

		result := []model.LogLineItem{}

		for item := range logsChannel {
			result = append(result, *item)
		}

		assert.Equal(t, expected, result)
	})
}
