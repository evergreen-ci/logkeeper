package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/pkg/errors"
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

func buildPrefix(buildID string) string {
	return fmt.Sprintf("/builds/%s/", buildID)
}

func testPrefix(buildID, testID string) string {
	return fmt.Sprintf("/builds/%s/tests/%s/", buildID, testID)
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

func (m *buildMetadata) key() string {
	return fmt.Sprintf("%s%s", buildPrefix(m.ID), metadataFilename)
}

func (m *buildMetadata) toJSON() ([]byte, error) {
	metadataJSON, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling metadata")
	}

	return metadataJSON, nil
}