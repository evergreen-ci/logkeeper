package storage

import (
	"context"
	"io"
	"testing"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/stretchr/testify/assert"
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
