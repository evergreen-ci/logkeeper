package models

import (
	"fmt"
	"time"
)

// LogChunkInfo describes a chunk of log lines stored in pail-backed offline
// storage.
type LogChunkInfo struct {
	BuildId  string
	TestId   string
	NumLines int
	Start    time.Time
	End      time.Time
}

func (info *LogChunkInfo) Key() string {
	if info.TestId != "" {
		return fmt.Sprintf("/%s/tests/%s/%d_%d_%d", info.BuildId, info.TestId, info.Start.UnixNano(), info.End.UnixNano(), info.NumLines)
	} else {
		return fmt.Sprintf("/%s/%d_%d_%d", info.BuildId, info.Start.UnixNano(), info.End.UnixNano(), info.NumLines)
	}
}
