package storage

import (
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
)

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
		assert.Equal(t, "builds/b0/tests/t0/1257894000000000000_1257894060000000000_1", key)
		newInfo := LogChunkInfo{}
		assert.NoError(t, newInfo.fromKey(key))
		assert.Equal(t, info, newInfo)
		parsedTestId, err := testIDFromKey(key)
		assert.NoError(t, err)
		assert.Equal(t, info.TestID, parsedTestId)
	})

	t.Run("WithoutTest", func(t *testing.T) {
		info := LogChunkInfo{
			BuildID:  "b0",
			NumLines: 1,
			Start:    time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC),
			End:      time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC),
		}
		key := info.key()
		assert.Equal(t, "builds/b0/1257894000000000000_1257894060000000000_1", key)
		newInfo := LogChunkInfo{}
		assert.NoError(t, newInfo.fromKey(key))
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

func TestBuildMetadataKey(t *testing.T) {
	metadata := buildMetadata{
		ID:       "b0",
		Builder:  "builder0",
		BuildNum: 1,
		TaskID:   "t0",
	}
	assert.Equal(t, "builds/b0/metadata.json", metadata.key())
}

func TestBuildMetadataJSON(t *testing.T) {
	metadata := buildMetadata{
		ID:       "b0",
		Builder:  "builder0",
		BuildNum: 1,
		TaskID:   "t0",
	}
	json, err := metadata.toJSON()
	assert.NoError(t, err)
	assert.Equal(t, `{"id":"b0","builder":"builder0","buildnum":1,"task_id":"t0"}`, string(json))
}

func TestTestMetadataKey(t *testing.T) {
	metadata := testMetadata{
		ID:      "test0",
		Name:    "name",
		BuildID: "build0",
		TaskID:  "t0",
		Phase:   "phase0",
		Command: "command0",
	}
	assert.Equal(t, "builds/build0/tests/test0/metadata.json", metadata.key())
}

func TestTestMetadataJSON(t *testing.T) {
	metadata := testMetadata{
		ID:      "test0",
		Name:    "name",
		BuildID: "build0",
		TaskID:  "t0",
		Phase:   "phase0",
		Command: "command0",
	}
	json, err := metadata.toJSON()
	assert.NoError(t, err)
	assert.Equal(t, `{"id":"test0","name":"name","build_id":"build0","task_id":"t0","phase":"phase0","command":"command0"}`, string(json))
}

func TestMakeLogLineString(t *testing.T) {
	result := makeLogLineStrings(model.LogLine{
		Msg:  "a\nb",
		Time: time.Unix(1661354966, 0),
	})
	assert.Equal(t, []string{"  0       1661354966000a\n", "  0       1661354966000b\n"}, result)
}
