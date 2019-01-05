package logkeeper

import "time"

const (
	CleanupBatchSize = 2000

	AmboyInterval      = time.Minute
	AmboyWorkersPerApp = 8
	AmboyTargetNumJobs = CleanupBatchSize / 5

	AmboyDBName             = "amboy"
	AmboyMigrationQueueName = "logkeeper.etl"
)