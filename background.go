package logkeeper

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
)

func StartBackgroundLogging(ctx context.Context) {
	go func() {
		defer recovery.LogStackTraceAndContinue("background logging")

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		grip.Debug("starting stats collector")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				grip.Info(message.CollectSystemInfo())
				grip.Info(message.CollectBasicGoStats())

				if IsLeader() {
					grip.Info(message.Fields{
						"message": "amboy queue stats",
						"stats":   env.GetCleanupQueue().Stats(ctx),
					})
				}

			}
		}
	}()
}
