package units

import (
	"context"
	"fmt"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
)

func StartCrons(ctx context.Context, migration, remote, local amboy.Queue) error {
	if !logkeeper.IsLeader() {
		grip.Notice("leader file does not exist, not submitting jobs")
		return nil
	}

	opts := amboy.QueueOperationConfig{
		ContinueOnError: true,
		LogErrors:       false,
		DebugLogging:    false,
	}

	grip.Info(message.Fields{
		"message":  "starting background cron jobs",
		"state":    "not populated",
		"interval": logkeeper.AmboyInterval.String(),
		"opts":     opts,
		"started": message.Fields{
			"migration": migration.Started(),
			"remote":    remote.Started(),
			"local":     local.Started(),
		},
		"stats": message.Fields{
			"migration": migration.Stats(),
			"remote":    remote.Stats(),
			"local":     local.Stats(),
		},
	})

	amboy.IntervalQueueOperation(ctx, migration, 10*time.Second, time.Now(), opts, PopulateCleanupOldLogDataJobs(ctx))

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs(ctx context.Context) amboy.QueueOperation {
	var lastDuration time.Duration
	var lastCompleted time.Time

	const useStreamingMethod = true

	return func(queue amboy.Queue) error {
		startAt := time.Now()
		catcher := grip.NewBasicCatcher()

		var (
			err    error
			builds []logkeeper.LogKeeperBuild
			seen   int
		)

		if useStreamingMethod {
			grip.Info("starting streaming creation")
			builds, errs := logkeeper.StreamingGetOldBuilds(ctx)
		addLoop:
			for {
				select {
				case <-ctx.Done():
					break addLoop
				case err := <-errs:
					catcher.Add(err)
					break addLoop
				case build := <-builds:
					catcher.Add(queue.Put(NewCleanupOldLogDataJob(build.Id, build.Info["task_id"])))
					seen++
					continue
				}
			}
		} else {
			stats := queue.Stats()
			if stats.Pending == 0 || stats.Pending < logkeeper.CleanupBatchSize/5 || time.Since(lastCompleted) >= lastDuration {
				builds, err = logkeeper.GetOldBuilds(logkeeper.CleanupBatchSize)
				catcher.Add(err)
				lastDuration = time.Since(startAt)
			}

			for _, build := range builds {
				catcher.Add(queue.Put(NewCleanupOldLogDataJob(build.Id, build.Info["task_id"])))
			}
			lastCompleted = time.Now()
		}

		m := message.Fields{
			"message":    "completed adding cleanup job",
			"streaming":  useStreamingMethod,
			"num":        len(builds),
			"iters":      seen,
			"errors":     catcher.HasErrors(),
			"num_errors": catcher.Len(),
			"dur_secs":   time.Since(startAt).Seconds(),
			"queue":      fmt.Sprintf("%T", queue),
			"stats":      queue.Stats(),
		}

		if len(builds) > 0 {
			build := builds[len(builds)-1]
			m["last_started_at"] = build.Started.Format("2006-01-02.15:04:05")
		}

		if catcher.HasErrors() {
			m["err"] = catcher.Errors()[0].Error()
		}

		grip.Info(m)

		return catcher.Resolve()
	}
}

func PopulateStatsJobs() amboy.QueueOperation {
	return func(queue amboy.Queue) error {
		// round time to the minute by format
		ts := time.Now().Format("2006-01-02.15-04")

		return queue.Put(NewAmboyStatsCollector(ts))
	}
}
