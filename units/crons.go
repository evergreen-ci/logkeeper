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
			err   error
			tests []logkeeper.Test
		)

		if useStreamingMethod {
			grip.Info("starting streaming creation")
			tests, errs := logkeeper.StreamingGetOldTests(ctx)
		addLoop:
			for {
				select {
				case <-ctx.Done():
					break addLoop
				case err := <-errs:
					catcher.Add(err)
					break addLoop
				case test := <-tests:
					catcher.Add(queue.Put(NewCleanupOldLogDataJob(test.BuildId, test.Info["task_id"], test.Id.Hex())))
					continue
				}
			}
		} else {
			stats := queue.Stats()
			if stats.Pending == 0 || stats.Pending < logkeeper.CleanupBatchSize/5 || time.Since(lastCompleted) >= lastDuration {
				tests, err = logkeeper.GetOldTests(logkeeper.CleanupBatchSize)
				catcher.Add(err)
				lastDuration = time.Since(startAt)
			}

			for _, test := range tests {
				catcher.Add(queue.Put(NewCleanupOldLogDataJob(test.BuildId, test.Info["task_id"], test.Id.Hex())))
			}
			lastCompleted = time.Now()
		}

		m := message.Fields{
			"message":    "completed adding cleanup job",
			"streaming":  useStreamingMethod,
			"num":        len(tests),
			"errors":     catcher.HasErrors(),
			"limit":      logkeeper.CleanupBatchSize,
			"num_errors": catcher.Len(),
			"dur_secs":   time.Since(startAt).Seconds(),
			"queue":      fmt.Sprintf("%T", queue),
			"stats":      queue.Stats(),
		}

		if len(tests) > 0 {
			test := tests[len(tests)-1]
			m["last_started_at"] = test.Started.Format("2006-01-02.15:04:05")
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
