package logkeeper

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/send"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseLoggerLoop(t *testing.T) {
	defer grip.SetSender(grip.GetSender())

	t.Run("SingleResponse", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
		defer cancel()
		sender := send.NewMockSender("")
		require.NoError(t, grip.SetSender(sender))
		logger := NewLogger(ctx)

		logger.newResponses <- routeResponse{route: "test_route", duration: time.Second}
		require.Eventually(t, func() bool { return len(sender.Messages) >= 1 }, 2*loggerStatsInterval, loggerStatsInterval/2)
		msg := sender.Messages[0].Raw().(message.Fields)
		assert.Equal(t, "test_route", msg["route"])
		assert.Equal(t, 1, msg["count"])
	})

	t.Run("MultipleResponses", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
		defer cancel()
		sender := send.NewMockSender("")
		require.NoError(t, grip.SetSender(sender))
		logger := NewLogger(ctx)

		logger.newResponses <- routeResponse{route: "test_route", duration: 0}
		logger.newResponses <- routeResponse{route: "test_route", duration: 5 * time.Second}
		logger.newResponses <- routeResponse{route: "test_route", duration: 10 * time.Second}
		require.Eventually(t, func() bool { return len(sender.Messages) >= 1 }, 2*loggerStatsInterval, loggerStatsInterval/2)
		msg := sender.Messages[0].Raw().(message.Fields)
		assert.Equal(t, "test_route", msg["route"])
		assert.Equal(t, 3, msg["count"])
	})

	t.Run("MultipleRoutes", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*loggerStatsInterval)
		defer cancel()
		sender := send.NewMockSender("")
		require.NoError(t, grip.SetSender(sender))
		logger := NewLogger(ctx)

		routes := []string{"r0", "r1"}
		logger.newResponses <- routeResponse{route: routes[0], duration: time.Second}
		logger.newResponses <- routeResponse{route: routes[1], duration: time.Second}

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
	require.NoError(t, grip.SetSender(sender))
	logger := NewLogger(ctx)
	for i := 0; i < statsLimit+1; i++ {
		select {
		case <-ctx.Done():
			t.FailNow()
		case logger.newResponses <- routeResponse{}:
		}

	}

	require.Eventually(t, func() bool { return len(sender.Messages) > 0 }, loggerStatsInterval, loggerStatsInterval/10)
	assert.Equal(t, statsLimit, sender.Messages[0].Raw().(message.Fields)["count"])
}

func TestRecordResponse(t *testing.T) {
	logger := Logger{statsByRoute: make(map[string]routeStats)}
	for i := 0; i < statsLimit; i++ {
		logger.recordResponse(routeResponse{route: "r0"})
	}
	require.Len(t, logger.statsByRoute, 1)
	assert.Len(t, logger.statsByRoute["r0"].durationMS, statsLimit)
	assert.True(t, logger.cacheIsFull)
}

func TestFlushStats(t *testing.T) {
	defer grip.SetSender(grip.GetSender())
	sender := send.NewMockSender("")
	require.NoError(t, grip.SetSender(sender))

	testStart := time.Now()
	routes := []string{"route0", "route1"}
	logger := Logger{
		statsByRoute: map[string]routeStats{
			routes[0]: {
				durationMS: []float64{1, 2},
				requestMB:  []float64{1, 2},
				responseMB: []float64{1, 2},
			},
			routes[1]: {
				durationMS: []float64{1, 2},
				requestMB:  []float64{1, 2},
				responseMB: []float64{1, 2},
			},
		},
		lastReset:   testStart,
		cacheIsFull: true,
	}

	logger.flushStats()

	require.Len(t, sender.Messages, 2)
	for _, msg := range sender.Messages {
		assert.Contains(t, routes, msg.Raw().(message.Fields)["route"])
	}

	require.Contains(t, logger.statsByRoute, routes[0])
	assert.Len(t, logger.statsByRoute[routes[0]].durationMS, 0)
	assert.Len(t, logger.statsByRoute[routes[0]].requestMB, 0)
	assert.Len(t, logger.statsByRoute[routes[0]].responseMB, 0)
	assert.Len(t, logger.statsByRoute[routes[0]].statusCounts, 0)

	require.Contains(t, logger.statsByRoute, routes[1])
	assert.Len(t, logger.statsByRoute[routes[1]].durationMS, 0)
	assert.Len(t, logger.statsByRoute[routes[1]].requestMB, 0)
	assert.Len(t, logger.statsByRoute[routes[1]].responseMB, 0)
	assert.Len(t, logger.statsByRoute[routes[1]].statusCounts, 0)

	assert.Greater(t, logger.lastReset, testStart)
	assert.False(t, logger.cacheIsFull)
}

func TestSliceStats(t *testing.T) {
	t.Run("ValidInput", func(t *testing.T) {
		sample := []float64{0, 5, 10}
		bins := []float64{0, 1, 5, 10, 20}
		stats := sliceStats(sample, bins)
		assert.EqualValues(t, stats["sum"], 15)
		assert.EqualValues(t, stats["min"], 0)
		assert.EqualValues(t, stats["max"], 10)
		assert.EqualValues(t, stats["mean"], 5)
		assert.EqualValues(t, stats["std_dev"], 5)
		assert.EqualValues(t, stats["histogram"], []float64{1, 0, 1, 1})
	})

	t.Run("EmptySample", func(t *testing.T) {
		bins := []float64{0, 1, 5, 10, 20}
		assert.Equal(t, message.Fields{}, sliceStats([]float64{}, bins))
	})

	t.Run("InvalidBins", func(t *testing.T) {
		sample := []float64{0, 5, 10}
		bins := []float64{0, 1, 5, 10}
		assert.Equal(t, message.Fields{}, sliceStats(sample, bins))
	})
}

func TestMakeMessage(t *testing.T) {
	stats := routeStats{
		durationMS:   []float64{1, 2, 3},
		responseMB:   []float64{1, 2, 3},
		requestMB:    []float64{1, 2, 3},
		statusCounts: map[int]int{http.StatusOK: 3},
	}

	msg := stats.makeMessage()
	assert.Equal(t, 3, msg["count"])
	serviceTimeMap, ok := msg["service_time_ms"].(message.Fields)
	require.True(t, ok)
	assert.EqualValues(t, 6, serviceTimeMap["sum"])

	responseSizesMap, ok := msg["response_sizes_mb"].(message.Fields)
	require.True(t, ok)
	assert.EqualValues(t, 6, responseSizesMap["sum"])

	requestSizesMap, ok := msg["request_sizes_mb"].(message.Fields)
	require.True(t, ok)
	assert.EqualValues(t, 6, requestSizesMap["sum"])

	statusCountMap, ok := msg["statuses"].(map[int]int)
	require.True(t, ok)
	assert.Equal(t, 3, statusCountMap[http.StatusOK])
}
