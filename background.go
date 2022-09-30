package logkeeper

import (
	"context"
	"time"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
)

const backgroundLoggingInterval = 15 * time.Second

func BackgroundLogging(ctx context.Context) {
	ticker := time.NewTicker(backgroundLoggingInterval)
	defer ticker.Stop()
	grip.Debug("starting stats collector")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			grip.Info(message.CollectSystemInfo())
			grip.Info(message.CollectBasicGoStats())
		}
	}
}
