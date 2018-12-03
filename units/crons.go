package units

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/sometimes"
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

	amboy.IntervalQueueOperation(ctx, remote, time.Hour, time.Now(), opts,
		amboy.GroupQueueOperationFactory(PopulateCleanupOldLogDataJobs()))

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs() amboy.QueueOperation {
	return func(queue amboy.Queue) error {
		startAt := time.Now()
		catcher := grip.NewBasicCatcher()

		tests, err := logkeeper.GetOldTests()
		if err != nil {
			return errors.WithStack(err)
		}

		for idx, test := range tests {
			catcher.Add(queue.Put(NewCleanupOldLogDataJob(test.Id, test.Info["taskID"])))

			grip.DebugWhen(sometimes.Percent(10), message.Fields{
				"message":    "adding decomission jobs",
				"index":      idx,
				"total":      len(tests),
				"errors":     catcher.HasErrors(),
				"num_errors": catcher.Len(),
			})
		}

		grip.Info(message.Fields{
			"message":    "completed adding cleanup job",
			"num":        len(tests),
			"errors":     catcher.HasErrors(),
			"num_errors": catcher.Len(),
			"dur_secs":   time.Since(startAt).Seconds(),
		})

		return catcher.Resolve()
	}
}
