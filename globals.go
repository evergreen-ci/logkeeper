package logkeeper

import (
	"os"
	"time"
)

const (
	CleanupBatchSize = 10000
	QueueSizeCap     = 10 * CleanupBatchSize * 5

	AmboyInterval      = time.Minute
	AmboyWorkers       = 16
	AmboyTargetNumJobs = CleanupBatchSize

	AmboyDBName             = "amboy"
	AmboyMigrationQueueName = "logkeeper.etl"

	AmboyLeaderFile = "/srv/logkeeper/amboy.leader"
)

func IsLeader() bool {
	if _, err := os.Stat(AmboyLeaderFile); !os.IsNotExist(err) {
		return true
	}

	return false
}
