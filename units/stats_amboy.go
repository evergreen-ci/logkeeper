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
	"github.com/mongodb/amboy/queue"
	"github.com/mongodb/amboy/registry"
	"github.com/mongodb/amboy/reporting"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
)

const (
	amboyStatsCollectorJobName = "amboy-stats-collector"
	enableExtendedRemoteStats  = false
)

func init() {
	registry.AddJobType(amboyStatsCollectorJobName,
		func() amboy.Job { return makeAmboyStatsCollector() })
}

type amboyStatsCollector struct {
	job.Base `bson:"job_base" json:"job_base" yaml:"job_base"`
}

// NewAmboyStatsCollector reports the status of only the remote queue
// registered in the evergreen service Environment.
func NewAmboyStatsCollector(id string) amboy.Job {
	j := makeAmboyStatsCollector()
	j.SetID(fmt.Sprintf("%s-%s", amboyStatsCollectorJobName, id))
	return j
}

func makeAmboyStatsCollector() *amboyStatsCollector {
	j := &amboyStatsCollector{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    amboyStatsCollectorJobName,
				Version: 0,
			},
		},
	}

	j.SetDependency(dependency.NewAlways())
	return j
}

func (j *amboyStatsCollector) Run(ctx context.Context) {
	defer j.MarkComplete()

	queue := db.GetMigrationQueue()
	if queue != nil && queue.Started() {
		grip.Info(message.Fields{
			"message": "amboy queue stats",
			"name":    logkeeper.AmboyMigrationQueueName,
			"stats":   queue.Stats(),
		})

		if enableExtendedRemoteStats {
			j.AddError(j.collectExtendedRemoteStats(ctx, logkeeper.AmboyMigrationQueueName))
		}
	}

	grip.Warning(message.Fields{
		"op":   "amboy queue stats",
		"name": logkeeper.AmboyMigrationQueueName,
		"nil":  queue == nil,
	})
}

func (j *amboyStatsCollector) collectExtendedRemoteStats(ctx context.Context, name string) error {
	opts := queue.DefaultMongoDBOptions()
	opts.DB = logkeeper.AmboyDBName
	opts.Priority = true
	opts.CheckWaitUntil = true

	session := db.GetSession()
	defer session.Close()

	reporter, err := reporting.MakeDBQueueState(name, opts, session)
	if err != nil {
		return err
	}

	r := message.Fields{
		"message": "amboy queue report",
		"name":    name,
	}

	pending, err := reporter.JobStatus(ctx, reporting.Pending)
	j.AddError(err)
	if pending != nil {
		r["pending"] = pending
	}
	inprog, err := reporter.JobStatus(ctx, reporting.InProgress)
	j.AddError(err)
	if inprog != nil {
		r["inprog"] = inprog
	}
	stale, err := reporter.JobStatus(ctx, reporting.Stale)
	j.AddError(err)
	if stale != nil {
		r["stale"] = stale
	}

	recentErrors, err := reporter.RecentErrors(ctx, time.Minute, reporting.StatsOnly)
	j.AddError(err)
	if recentErrors != nil {
		r["errors"] = recentErrors
	}

	grip.InfoWhen(len(r) > 2, r)
	return nil
}
