package storage

import (
	"fmt"
	"time"
)

// LogChunkInfo describes a chunk of log lines stored in pail-backed offline
// storage.
type LogChunkInfo struct {
	BuildID  string
	TestID   string
	NumLines int
	Start    time.Time
	End      time.Time
}

func (info *LogChunkInfo) Key() string {
	if info.TestID != "" {
		return fmt.Sprintf("/%s/tests/%s/%d_%d_%d", info.BuildID, info.TestID, info.Start.UnixNano(), info.End.UnixNano(), info.NumLines)
	} else {
		return fmt.Sprintf("/%s/%d_%d_%d", info.BuildID, info.Start.UnixNano(), info.End.UnixNano(), info.NumLines)
	}
}
