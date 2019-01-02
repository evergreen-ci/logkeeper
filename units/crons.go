package units

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
)

func StartCrons(ctx context.Context, remote, local amboy.Queue) error {
	opts := amboy.QueueOperationConfig{
		ContinueOnError: true,
		LogErrors:       false,
		DebugLogging:    false,
	}

	grip.Info(message.Fields{
		"message": "starting background cron jobs",
		"state":   "not populated",
		"opts":    opts,
	})

	amboy.IntervalQueueOperation(ctx, remote, logkeeper.AmboyInterval, time.Now(), opts,
		amboy.GroupQueueOperationFactory(
			PopulateCleanupOldLogDataJobs(),
			PopulateStatsJobs(),
		),
	)

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs() amboy.QueueOperation {
	return func(queue amboy.Queue) error {
		startAt := time.Now()
		catcher := grip.NewBasicCatcher()

		tests, err := logkeeper.GetOldTests(logkeeper.CleanupBatchSize)
		if err != nil {
			return errors.WithStack(err)
		}

		for _, test := range tests {
			catcher.Add(queue.Put(NewCleanupOldLogDataJob(test.BuildId, test.Info["task_id"])))
		}

		grip.Info(message.Fields{
			"message":    "completed adding cleanup job",
			"num":        len(tests),
			"errors":     catcher.HasErrors(),
			"limit":      logkeeper.CleanupBatchSize,
			"num_errors": catcher.Len(),
			"dur_secs":   time.Since(startAt).Seconds(),
		})

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
