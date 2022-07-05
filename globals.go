package logkeeper

import (
	"os"
	"time"
)

const (
	CleanupBatchSize = 10000
	QueueSizeCap     = 10 * CleanupBatchSize * 5

	DBName = "buildlogs"

	AmboyInterval      = time.Minute
	AmboyWorkers       = 32
	AmboyTargetNumJobs = CleanupBatchSize
	AmboyLeaderFile    = "/srv/logkeeper/amboy.leader"
)

func IsLeader() bool {
	if _, err := os.Stat(AmboyLeaderFile); !os.IsNotExist(err) {
		return true
	}

	return false
}
