package storage

import (
	"context"
	"io"
	"testing"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
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
	assert.Equal(t, expectedMetadata, string(contents))
}
