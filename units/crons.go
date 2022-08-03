package units

import (
	"context"
	"fmt"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/logkeeper/model"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
)

func StartCrons(ctx context.Context, cleaupQueue amboy.Queue) error {
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
		"started":  cleaupQueue.Info(),
		"stats":    cleaupQueue.Stats(ctx),
	})

	amboy.IntervalQueueOperation(ctx, cleaupQueue, 10*time.Second, time.Now(), opts, PopulateCleanupOldLogDataJobs(ctx))

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs(ctx context.Context) amboy.QueueOperation {
	return func(ctx context.Context, queue amboy.Queue) error {
		grip.Info("starting streaming creation")
		startAt := time.Now()
		catcher := grip.NewBasicCatcher()

		seen := 0
		builds, errs := model.StreamingGetOldBuilds(ctx)
	addLoop:
		for {
			select {
			case <-ctx.Done():
				break addLoop
			case err := <-errs:
				catcher.Add(err)
				break addLoop
			case build := <-builds:
				catcher.Add(queue.Put(ctx, NewCleanupOldLogDataJob(build.Id, build.Info.TaskID)))
				seen++
				continue
			}
		}

		m := message.Fields{
			"message":    "completed adding cleanup job",
			"iters":      seen,
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
