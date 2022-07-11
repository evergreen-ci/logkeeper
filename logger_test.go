package logkeeper

import (
	"context"
	"testing"
	"time"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/send"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationLoggerLoop(t *testing.T) {
	defer grip.SetSender(grip.GetSender())

	t.Run("SingleDuration", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
		defer cancel()
		sender := send.NewMockSender("")
		grip.SetSender(sender)
		logger := NewLogger(ctx)

		logger.newDurations <- routeResponse{route: "test_route", duration: time.Second}
		require.Eventually(t, func() bool { return len(sender.Messages) >= 1 }, 2*loggerStatsInterval, loggerStatsInterval/2)
		msg := sender.Messages[0].Raw().(message.Fields)
		assert.Equal(t, "test_route", msg["route"])
		assert.Equal(t, 1, msg["count"])
		assert.EqualValues(t, 1000, msg["sum_ms"])
	})

	t.Run("MultipleDurations", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
		defer cancel()
		sender := send.NewMockSender("")
		grip.SetSender(sender)
		logger := NewLogger(ctx)

		logger.newDurations <- routeResponse{route: "test_route", duration: 0}
		logger.newDurations <- routeResponse{route: "test_route", duration: 5 * time.Second}
		logger.newDurations <- routeResponse{route: "test_route", duration: 10 * time.Second}
		require.Eventually(t, func() bool { return len(sender.Messages) >= 1 }, 2*loggerStatsInterval, loggerStatsInterval/2)
		msg := sender.Messages[0].Raw().(message.Fields)
		assert.Equal(t, "test_route", msg["route"])
		assert.Equal(t, 3, msg["count"])
		assert.EqualValues(t, 15000, msg["sum_ms"])
		assert.EqualValues(t, 10000, msg["max_ms"])
		assert.EqualValues(t, 0, msg["min_ms"])
		assert.EqualValues(t, 5000, msg["mean_ms"])
		assert.EqualValues(t, 5000, msg["std_dev"])
	})

	t.Run("MultipleRoutes", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
		defer cancel()
		sender := send.NewMockSender("")
		grip.SetSender(sender)
		logger := NewLogger(ctx)

		routes := []string{"r0", "r1"}
		logger.newDurations <- routeResponse{route: routes[0], duration: time.Second}
		logger.newDurations <- routeResponse{route: routes[1], duration: time.Second}

		require.Eventually(t, func() bool { return len(sender.Messages) >= 2 }, 2*loggerStatsInterval, loggerStatsInterval/2)
		for _, msg := range sender.Messages {
			assert.Contains(t, routes, msg.Raw().(message.Fields)["route"])
		}
	})
}

func TestCacheIsFull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
	defer cancel()

	defer grip.SetSender(grip.GetSender())
	sender := send.NewMockSender("")
	grip.SetSender(sender)
	logger := NewLogger(ctx)
	for i := 0; i < statsLimit+1; i++ {
		select {
		case <-ctx.Done():
			t.FailNow()
		case logger.newDurations <- routeResponse{}:
		}

	}

	require.Eventually(t, func() bool { return len(sender.Messages) > 0 }, loggerStatsInterval, loggerStatsInterval/10)
	assert.Equal(t, statsLimit, sender.Messages[0].Raw().(message.Fields)["count"])
}

func TestRecordDuration(t *testing.T) {
	logger := Logger{}
	for i := 0; i < statsLimit+1; i++ {
		logger.recordDuration(routeResponse{})
	}
	assert.Len(t, logger.statsByRoute, statsLimit)
	assert.True(t, logger.cacheIsFull)
}

func TestFlushStats(t *testing.T) {
	defer grip.SetSender(grip.GetSender())
	sender := send.NewMockSender("")
	grip.SetSender(sender)

	testStart := time.Now()
	routes := []string{"route0", "route1"}
	logger := Logger{
		statsByRoute: map[string][]float64{
			routes[0]: {1},
			routes[1]: {2},
		},
		lastReset:   testStart,
		cacheIsFull: true,
	}

	logger.flushStats()

	require.Len(t, sender.Messages, 2)
	for _, msg := range sender.Messages {
		assert.Contains(t, routes, msg.Raw().(message.Fields)["route"])
	}

	assert.Contains(t, logger.statsByRoute, routes[0])
	assert.Len(t, logger.statsByRoute[routes[0]], 0)
	assert.Contains(t, logger.statsByRoute, routes[1])
	assert.Len(t, logger.statsByRoute[routes[1]], 0)

	assert.Greater(t, logger.lastReset, testStart)
	assert.False(t, logger.cacheIsFull)
}
