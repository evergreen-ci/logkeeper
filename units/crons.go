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

func StartCrons(ctx context.Context, migration, local amboy.Queue) error {
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
			"migration": migration.Info().Started,
			"local":     local.Info().Started,
		},
		"stats": message.Fields{
			"migration": migration.Stats(ctx),
			"local":     local.Stats(ctx),
		},
	})

	amboy.IntervalQueueOperation(ctx, migration, 10*time.Second, time.Now(), opts, PopulateCleanupOldLogDataJobs())

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs() amboy.QueueOperation {
	var lastDuration time.Duration
	var lastCompleted time.Time

	const useStreamingMethod = true

	return func(ctx context.Context, queue amboy.Queue) error {
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
					catcher.Add(queue.Put(ctx, NewCleanupOldLogDataJob(build.Id, build.Info["task_id"])))
					seen++
					continue
				}
			}
		} else {
			stats := queue.Stats(ctx)
			if stats.Pending == 0 || stats.Pending < logkeeper.CleanupBatchSize/5 || time.Since(lastCompleted) >= lastDuration {
				builds, err = logkeeper.GetOldBuilds(logkeeper.CleanupBatchSize)
				catcher.Add(err)
				lastDuration = time.Since(startAt)
			}

			for _, build := range builds {
				catcher.Add(queue.Put(ctx, NewCleanupOldLogDataJob(build.Id, build.Info["task_id"])))
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
			"stats":      queue.Stats(ctx),
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
