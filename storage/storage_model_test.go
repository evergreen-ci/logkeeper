package storage

import (
	"testing"
	"time"

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
		assert.Equal(t, "/b0/tests/t0/1257894000000000000_1257894060000000000_1", key)
		newInfo := LogChunkInfo{}
		assert.NoError(t, newInfo.fromKey(key))
		assert.Equal(t, info, newInfo)
	})

	t.Run("WithoutTest", func(t *testing.T) {
		info := LogChunkInfo{
			BuildID:  "b0",
			NumLines: 1,
			Start:    time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC),
			End:      time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC),
		}
		key := info.key()
		assert.Equal(t, "/b0/1257894000000000000_1257894060000000000_1", key)
		newInfo := LogChunkInfo{}
		assert.NoError(t, newInfo.fromKey(key))
		assert.Equal(t, info, newInfo)
	})

}

func TestBuildMetadataKey(t *testing.T) {
	metadata := buildMetadata{
		ID:       "b0",
		Builder:  "builder0",
		BuildNum: 1,
		TaskID:   "t0",
	}
	assert.Equal(t, "/b0/metadata.json", metadata.key())
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
