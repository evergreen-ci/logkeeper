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
)

func TestUploadBuildMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storage := makeTestStorage(t, "")
	build := Build{
		ID:       "5a75f537726934e4b62833ab6d5dca41",
		Builder:  "builder0",
		BuildNum: 1,
		TaskID:   "t0",
	}
	expectedData, err := build.toJSON()
	require.NoError(t, err)
	require.NoError(t, storage.UploadBuildMetadata(ctx, build))

	r, err := storage.Get(ctx, "/builds/5a75f537726934e4b62833ab6d5dca41/metadata.json")
	require.NoError(t, err)
	defer r.Close()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

func TestUploadTestMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storage := makeTestStorage(t, "")
	test := Test{
		ID:      string(model.TestID("62dba0159041307f697e6ccc")),
		Name:    "test0",
		BuildID: "5a75f537726934e4b62833ab6d5dca41",
		TaskID:  "t0",
		Phase:   "phase0",
		Command: "command0",
	}
	expectedData, err := test.toJSON()
	require.NoError(t, err)
	require.NoError(t, storage.UploadTestMetadata(ctx, test))

	r, err := storage.Get(ctx, "/builds/5a75f537726934e4b62833ab6d5dca41/tests/62dba0159041307f697e6ccc/metadata.json")
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

func TestInsertLogChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		require.NoError(t, storage.InsertLogChunks(ctx, buildID, "", uploadChunks))
		verifyDataStorage(t, storage, fmt.Sprintf("/builds/%s/", buildID), expectedChunks)

		logsChannel, err := storage.DownloadLogLines(ctx, buildID, "")
		require.NoError(t, err)
		result := []model.LogLineItem{}
		for item := range logsChannel {
			result = append(result, *item)
		}
		assert.Equal(t, expected, result)
	})
	t.Run("Test", func(t *testing.T) {
		storage := makeTestStorage(t, "nolines")
		testID := "DE0B6B3A764000000000000"
		require.NoError(t, storage.UploadTestMetadata(ctx, Test{
			ID:      string(model.TestID(testID)),
			BuildID: "5a75f537726934e4b62833ab6d5dca41",
		}))
		require.NoError(t, storage.InsertLogChunks(context.Background(), buildID, testID, uploadChunks))

		verifyDataStorage(t, storage, fmt.Sprintf("/builds/%s/tests/%s/", buildID, testID), expectedChunks)

		logsChannel, err := storage.DownloadLogLines(context.Background(), buildID, testID)
		require.NoError(t, err)
		result := []model.LogLineItem{}
		for item := range logsChannel {
			result = append(result, *item)
		}
		assert.Equal(t, expected, result)
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

func verifyDataStorage(t *testing.T, storage Bucket, prefix string, expectedChunks []expectedChunk) {
	for _, expectedChunk := range expectedChunks {
		actualChunkStream, err := storage.Get(context.Background(), fmt.Sprintf("%s%s", prefix, expectedChunk.filename))
		require.NoError(t, err)

		actualChunkBody, err := io.ReadAll(actualChunkStream)
		require.NoError(t, err)
		assert.Equal(t, expectedChunk.body, string(actualChunkBody))
	}
}
