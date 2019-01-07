package logkeeper

import "time"

const (
	factor = 5

	CleanupBatchSize = 3000 * factor

	AmboyInterval      = time.Minute * factor
	AmboyWorkersPerApp = 16
	AmboyTargetNumJobs = CleanupBatchSize

	AmboyDBName             = "amboy"
	AmboyMigrationQueueName = "logkeeper.etl"
)
