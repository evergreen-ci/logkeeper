package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/evergreen-ci/logkeeper/model"
)

const metadataFilename = "metadata.json"

func parseName(name string) (start time.Time, end time.Time, numLines int64, err error) {
	nameParts := strings.Split(name, "_")
	startNanos, err := strconv.ParseInt(nameParts[0], 10, 64)
	if err != nil {
		return
	}
	start = time.Unix(0, startNanos)

	endNanos, err := strconv.ParseInt(nameParts[1], 10, 64)
	if err != nil {
		return
	}
	end = time.Unix(0, endNanos)

	numLines, err = strconv.ParseInt(nameParts[2], 10, 64)
	if err != nil {
		return
	}
	return
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("/%s/", buildID)
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
	return fmt.Sprintf("%s/%s", buildPrefix(m.ID), metadataFilename)
}
