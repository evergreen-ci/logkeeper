package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2/bson"
)

const metadataFilename = "metadata.json"

// LogChunkInfo describes a chunk of log lines stored in pail-backed offline
// storage.
type LogChunkInfo struct {
	BuildID  string
	TestID   string
	NumLines int
	Start    time.Time
	End      time.Time
}

func (info *LogChunkInfo) key() string {
	var prefix string
	if info.TestID != "" {
		prefix = testPrefix(info.BuildID, info.TestID)
	} else {
		prefix = buildPrefix(info.BuildID)
	}
	return fmt.Sprintf("%s%d_%d_%d", prefix, info.Start.UnixNano(), info.End.UnixNano(), info.NumLines)
}

func (info *LogChunkInfo) fromKey(path string) error {
	var keyName string
	keyParts := strings.Split(path, "/")
	if strings.Contains(path, "/tests/") {
		info.BuildID = keyParts[1]
		info.TestID = keyParts[3]
		keyName = keyParts[4]
	} else {
		info.BuildID = keyParts[1]
		keyName = keyParts[2]
	}

	nameParts := strings.Split(keyName, "_")
	startNanos, err := strconv.ParseInt(nameParts[0], 10, 64)
	if err != nil {
		return errors.Wrap(err, "parsing start time")
	}
	info.Start = time.Unix(0, startNanos).UTC()

	endNanos, err := strconv.ParseInt(nameParts[1], 10, 64)
	if err != nil {
		return errors.Wrap(err, "parsing end time")
	}
	info.End = time.Unix(0, endNanos).UTC()

	numLines, err := strconv.ParseInt(nameParts[2], 10, 64)
	if err != nil {
		return errors.Wrap(err, "parsing num lines")
	}
	info.NumLines = int(numLines)

	return nil
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("/%s/", buildID)
}

func testPrefix(buildID, testID string) string {
	return fmt.Sprintf("/%s/tests/%s/", buildID, testID)
}

type buildMetadata struct {
	ID       string `json:"id"`
	Builder  string `json:"builder"`
	BuildNum int    `json:"buildnum"`
	TaskID   string `json:"task_id"`
}

func newBuildMetadata(b model.Build) buildMetadata {
	return buildMetadata{
		ID:       b.Id,
		Builder:  b.Builder,
		BuildNum: b.BuildNum,
		TaskID:   b.Info.TaskID,
	}
}

func (m *buildMetadata) toBuild() model.Build {
	return model.Build{
		Id:       m.ID,
		Builder:  m.Builder,
		BuildNum: m.BuildNum,
		Info: model.BuildInfo{
			TaskID: m.TaskID,
		},
	}
}

func (m *buildMetadata) key() string {
	return metadataKeyForBuildId(m.ID)
}

func metadataKeyForBuildId(id string) string {
	return fmt.Sprintf("%s%s", buildPrefix(id), metadataFilename)
}

func (m *buildMetadata) toJSON() ([]byte, error) {
	metadataJSON, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling metadata")
	}

	return metadataJSON, nil
}

type testMetadata struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BuildID string `json:"build_id"`
	TaskID  string `json:"task_id"`
}

func newTestMetadata(t model.Test) testMetadata {
	return testMetadata{
		ID:      t.Id.Hex(),
		BuildID: t.BuildId,
		Name:    t.Name,
		TaskID:  t.Info.TaskID,
	}
}

func (m *testMetadata) toTest() model.Test {
	return model.Test{
		Id:      bson.ObjectIdHex(m.ID),
		BuildId: m.BuildID,
		Name:    m.Name,
		Info: model.TestInfo{
			TaskID: m.TaskID,
		},
	}
}

func (m *testMetadata) key() string {
	return metadataKeyForTest(m.BuildID, m.ID)
}

func metadataKeyForTest(buildId string, testId string) string {
	return fmt.Sprintf("%s%s", testPrefix(buildId, testId), metadataFilename)
}

func (m *testMetadata) toJSON() ([]byte, error) {
	metadataJSON, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling metadata")
	}

	return metadataJSON, nil
}

func (m *testMetadata) fromJSON(data []byte) error {
	return errors.Wrap(json.Unmarshal(data, m), "unmarshalling metadata")
}
