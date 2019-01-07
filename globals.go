package logkeeper

import (
	"os"
	"time"
)

const (
	CleanupFactor    = 5
	CleanupBatchSize = 30000 * CleanupFactor

	AmboyInterval      = time.Minute * CleanupFactor
	AmboyWorkers       = 8
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
