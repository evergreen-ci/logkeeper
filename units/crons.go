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

func StartCleanupCron(ctx context.Context, cleanup amboy.Queue) error {
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
			"cleanup": cleanup.Info().Started,
		},
		"stats": message.Fields{
			"cleanup": cleanup.Stats(ctx),
		},
	})

	amboy.IntervalQueueOperation(ctx, cleanup, 10*time.Second, time.Now(), opts, PopulateCleanupOldLogDataJobs())

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs() amboy.QueueOperation {
	return func(ctx context.Context, queue amboy.Queue) error {
		startAt := time.Now()
		catcher := grip.NewBasicCatcher()

		grip.Info("starting streaming creation")
		var buildCount int
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
				buildCount++
				continue
			}
		}

		m := message.Fields{
			"message":    "added cleanup jobs",
			"builds":     buildCount,
			"errors":     catcher.HasErrors(),
			"num_errors": catcher.Len(),
			"dur_secs":   time.Since(startAt).Seconds(),
			"queue":      fmt.Sprintf("%T", queue),
			"stats":      queue.Stats(ctx),
		}

		if catcher.HasErrors() {
			m["err"] = catcher.Errors()[0].Error()
		}

		grip.Info(m)

		return catcher.Resolve()
	}
}
