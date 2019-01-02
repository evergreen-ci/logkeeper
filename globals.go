package logkeeper

import "time"

const (
	CleanupBatchSize = 1500

	AmboyInterval      = time.Minute
	AmboyWorkersPerApp = 8
	AmboyTargetNumJobs = CleanupBatchSize / 4

	AmboyDBName             = "amboy"
	AmboyMigrationQueueName = "logkeeper.etl"
)
