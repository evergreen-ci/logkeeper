package units

import (
	"context"

	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
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

	// amboy.IntervalQueueOperation(ctx, remote, time.Minute, time.Now(), opts, amboy.GroupQueueOperation())
	// amboy.IntervalQueueOperation(ctx, local, time.Minute, time.Now(), opts, amboy.GroupQueueOperation())

	return nil
}
