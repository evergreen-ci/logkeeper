package logkeeper

import (
	"context"
	"time"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
)

func StartBackgroundLogging(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			grip.Info(message.CollectSystemInfo())
			grip.Info(message.CollectGoStats())
		}
	}
}
