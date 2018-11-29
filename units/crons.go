package units

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/logkeeper/db"
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

	amboy.IntervalQueueOperation(ctx, remote, time.Minute, time.Now(), opts,
		amboy.GroupQueueOperationFactory(PopulateCleanupOldLogDataJobs()))

	amboy.IntervalQueueOperation(ctx, local, time.Minute, time.Now(), opts,
		amboy.GroupQueueOperationFactory(PopulateCleanupOldLogDataJobs()))

	return nil
}

// Queue Population Tasks

func PopulateCleanupOldLogDataJobs() amboy.QueueOperation {
	return func(queue amboy.Queue) error {
		catcher := grip.NewBasicCatcher()

		db := db.GetDatabase()
		tests, err := logkeeper.GetOldTests(db, time.Now())
		if err != nil {
			return errors.WithStack(err)
		}
		for _, test := range *tests {
			catcher.Add(queue.Put(NewCleanupOldLogDataJob(test.Id, test.Info["taskID"])))
		}

		return catcher.Resolve()
	}
}
