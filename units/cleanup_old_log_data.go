package units

import (
	"context"
	"fmt"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/dependency"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

const (
	cleanupJobsName = "cleanup-old-log-data-job"
)

func init() {
	registry.AddJobType(cleanupJobsName,
		func() amboy.Job { return makeCleanupOldLogDataJob() })
}

type cleanupOldLogDataJob struct {
	job.Base `bson:"job_base" json:"job_base" yaml:"job_base"`
}

func NewCleanupOldLogDataJob(id string) amboy.Job {
	j := makeCleanupOldLogDataJob()
	j.SetID(fmt.Sprintf("%s.%s", cleanupJobsName, id))
	return j
}

func makeCleanupOldLogDataJob() *cleanupOldLogDataJob {
	j := &cleanupOldLogDataJob{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    cleanupJobsName,
				Version: 0,
			},
		},
	}
	j.SetDependency(dependency.NewAlways())
	return j
}
func (j *cleanupOldLogDataJob) Run(ctx context.Context) {
	defer j.MarkComplete()
	db := db.GetDatabase()
	err := logkeeper.CleanupOldLogsTestsAndBuilds(db)
	if err != nil {
		j.AddError(errors.Wrap(err, "error cleaning up old logs"))
	}
}

func PopulateCleanupOldLogDataJobs() amboy.QueueOperation {
	return func(queue amboy.Queue) error {
		catcher := grip.NewBasicCatcher()
		ts := time.Now().String()
		err := queue.Put(NewCleanupOldLogDataJob(ts))
		catcher.Add(err)

		return catcher.Resolve()
	}
}
