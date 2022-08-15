package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/utility"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2/bson"
)

const metadataFilename = "metadata.json"

func parseLogLineString(data string) (model.LogLineItem, error) {
	ts, err := strconv.ParseInt(strings.TrimSpace(data[3:23]), 10, 64)
	if err != nil {
		return model.LogLineItem{}, errors.Wrap(err, "parsing log line timestamp")
	}

	return model.LogLineItem{
		Timestamp: time.Unix(0, ts*1e6).UTC(),
		// We need to Trim the newline here because Logkeeper doesn't expect newlines to be included in the LogLineItem.
		Data: strings.TrimRight(data[23:], "\n"),
	}, nil
}

func makeLogLineString(logLine model.LogLine) string {
	return fmt.Sprintf("  0%20d%s\n", utility.UnixMilli(logLine.Time), logLine.Msg)
}

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
		info.BuildID = keyParts[2]
		info.TestID = keyParts[4]
		keyName = keyParts[5]
	} else {
		info.BuildID = keyParts[2]
		keyName = keyParts[3]
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

func (info *LogChunkInfo) fromLogChunk(buildID string, testID string, logChunk model.LogChunk) error {
	if len(logChunk) == 0 {
		return errors.New("log chunk must contain at least one line")
	}
	minTime := TimeRangeMax
	maxTime := TimeRangeMin
	for _, logLine := range logChunk {
		if logLine.Time.Before(minTime) {
			minTime = logLine.Time
		}
		if logLine.Time.After(maxTime) {
			maxTime = logLine.Time
		}
	}
	info.BuildID = buildID
	info.TestID = testID
	info.NumLines = len(logChunk)
	info.Start = minTime
	info.End = maxTime
	return nil
}

func testIdFromKey(path string) (string, error) {
	keyParts := strings.Split(path, "/")
	if strings.Contains(path, "/tests/") && len(keyParts) >= 5 {
		return keyParts[4], nil
	}
	return "", errors.Errorf("programmatic error: unexpected test ID prefix in path '%s'", path)
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("/builds/%s/", buildID)
}

func buildTestsPrefix(buildID string) string {
	return fmt.Sprintf("%stests/", buildPrefix(buildID))
}

func testPrefix(buildID, testID string) string {
	return fmt.Sprintf("%s%s/", buildTestsPrefix(buildID), testID)
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
	Phase   string `json:"phase"`
	Command string `json:"command"`
}

func newTestMetadata(t model.Test) testMetadata {
	return testMetadata{
		ID:      t.Id.Hex(),
		BuildID: t.BuildId,
		Name:    t.Name,
		TaskID:  t.Info.TaskID,
		Phase:   t.Phase,
		Command: t.Command,
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
		Phase:   m.Phase,
		Command: m.Command,
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
