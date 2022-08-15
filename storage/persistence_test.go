package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

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
		Phase:   "phase0",
		Command: "command0",
	}

	assert.NoError(t, storage.UploadTestMetadata(context.Background(), test))
	results, err := storage.Get(context.Background(), "/builds/5a75f537726934e4b62833ab6d5dca41/tests/62dba0159041307f697e6ccc/metadata.json")
	assert.NoError(t, err)
	contents, err := io.ReadAll(results)
	assert.NoError(t, err)

	expectedMetadata := `{"id":"62dba0159041307f697e6ccc","name":"test0","build_id":"5a75f537726934e4b62833ab6d5dca41","task_id":"t0","phase":"phase0", "command":"command0"}`
	assert.JSONEq(t, expectedMetadata, string(contents))
}

func verifyDataStorage(t *testing.T, storage Bucket, prefix string, expectedChunks []expectedChunk) {
	for _, expectedChunk := range expectedChunks {
		actualChunkStream, err := storage.Get(context.Background(), fmt.Sprintf("%s%s", prefix, expectedChunk.filename))
		require.NoError(t, err)

		actualChunkBody, err := io.ReadAll(actualChunkStream)
		require.NoError(t, err)
		assert.Equal(t, expectedChunk.body, string(actualChunkBody))
	}
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

	expectedChunks := []expectedChunk{
		newExpectedChunk("1000000000000000000_1000000002000000000_3", []string{
			"  0       1000000000000line0\n",
			"  0       1000000001000line1\n",
			"  0       1000000002000line2\n",
		}),
		newExpectedChunk("1000000003000000000_1000000005000000000_3", []string{
			"  0       1000000003000line3\n",
			"  0       1000000004000line4\n",
			"  0       1000000005000line5\n",
		}),
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

		verifyDataStorage(t, storage, fmt.Sprintf("/builds/%s/", buildID), expectedChunks)

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

		verifyDataStorage(t, storage, fmt.Sprintf("/builds/%s/tests/%s/", buildID, testID), expectedChunks)

		logsChannel, err := storage.GetTestLogLines(context.Background(), buildID, testID)
		require.NoError(t, err)

		result := []model.LogLineItem{}

		for item := range logsChannel {
			result = append(result, *item)
		}

		assert.Equal(t, expected, result)
	})
}
