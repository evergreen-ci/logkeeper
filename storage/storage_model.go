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
)

const metadataFilename = "metadata.json"

// Build describes metadata of a build stored in pail-backed offline storage.
type Build struct {
	ID       string `json:"id"`
	Builder  string `json:"builder"`
	BuildNum int    `json:"buildnum"`
	TaskID   string `json:"task_id"`
}

func (b Build) export() *model.Build {
	return &model.Build{
		Id:       b.ID,
		Builder:  b.Builder,
		BuildNum: b.BuildNum,
		Info: model.BuildInfo{
			TaskID: b.TaskID,
		},
	}
}

func (b Build) key() string {
	return metadataKeyForBuild(b.ID)
}

func (b *Build) toJSON() ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling build metadata")
	}

	return data, nil
}

// Test describes metadata of a test stored in pail-backed offline storage.
type Test struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BuildID string `json:"build_id"`
	TaskID  string `json:"task_id"`
	Phase   string `json:"phase"`
	Command string `json:"command"`
}

func (t Test) export() *model.Test {
	return &model.Test{
		Id:      model.TestID(t.ID),
		BuildId: t.BuildID,
		Name:    t.Name,
		Info: model.TestInfo{
			TaskID: t.TaskID,
		},
		Phase:   t.Phase,
		Command: t.Command,
	}
}

func (t Test) key() string {
	return metadataKeyForTest(t.BuildID, t.ID)
}

func (t *Test) toJSON() ([]byte, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return nil, errors.Wrap(err, "marshalling test metadata")
	}

	return data, nil
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("builds/%s/", buildID)
}

func buildTestsPrefix(buildID string) string {
	return fmt.Sprintf("%stests/", buildPrefix(buildID))
}

func testPrefix(buildID, testID string) string {
	return fmt.Sprintf("%s%s/", buildTestsPrefix(buildID), testID)
}

func metadataKeyForBuild(id string) string {
	return fmt.Sprintf("%s%s", buildPrefix(id), metadataFilename)
}

func metadataKeyForTest(buildID string, testID string) string {
	return fmt.Sprintf("%s%s", testPrefix(buildID, testID), metadataFilename)
}

func testIDFromKey(path string) (string, error) {
	keyParts := strings.Split(path, "/")
	if strings.Contains(path, "/tests/") && len(keyParts) >= 5 {
		return keyParts[3], nil
	}
	return "", errors.Errorf("programmatic error: unexpected test ID prefix in path '%s'", path)
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
		if len(keyParts) < 5 {
			return errors.Errorf("invalid chunk key '%s'", path)
		}
		info.BuildID = keyParts[1]
		info.TestID = keyParts[3]
		keyName = keyParts[4]
	} else {
		if len(keyParts) < 3 {
			return errors.Errorf("invalid chunk key '%s'", path)
		}
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

func parseLogLineString(data string) (model.LogLineItem, error) {
	ts, err := strconv.ParseInt(strings.TrimSpace(data[3:23]), 10, 64)
	if err != nil {
		return model.LogLineItem{}, errors.Wrap(err, "parsing log line timestamp")
	}

	return model.LogLineItem{
		Timestamp: time.Unix(0, ts*1e6).UTC(),
		// We need to Trim the newline here because Logkeeper doesn't
		// expect newlines to be included in the LogLineItem.
		Data: strings.TrimRight(data[23:], "\n"),
	}, nil
}

func makeLogLineStrings(logLine model.LogLine) []string {
	singleLines := strings.Split(logLine.Msg, "\n")
	logLines := make([]string, 0, len(singleLines))
	for _, line := range singleLines {
		logLines = append(logLines, fmt.Sprintf("  0%20d%s\n", utility.UnixMilli(logLine.Time), line))
	}
	return logLines
}
