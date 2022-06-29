package env

import (
	"testing"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/send"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

func TestLoggerLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*logInterval)
	defer cancel()
	defer grip.SetSender(grip.GetSender())

	cache := NewStatsCache(ctx)
	for testName, testCase := range map[string]struct {
		mutator func() error
		field   string
	}{
		"BuildCreated":    {mutator: cache.BuildCreated, field: "num_builds_created"},
		"TestCreated":     {mutator: cache.TestCreated, field: "num_tests_created"},
		"Append":          {mutator: func() error { return cache.LogAppended(5) }, field: "num_appends"},
		"BuildsAccessed":  {mutator: cache.BuildAccessed, field: "num_builds_accessed"},
		"TestLogAccessed": {mutator: cache.TestLogsAccessed, field: "num_test_logs_accessed"},
		"AllLogsAccessed": {mutator: cache.AllLogsAccessed, field: "num_all_build_logs_accessed"},
	} {
		t.Run(testName, func(t *testing.T) {
			sender := send.NewMockSender("")
			grip.SetSender(sender)

			assert.NoError(t, testCase.mutator())

			require.Eventually(t, func() bool { return len(sender.Messages) > 0 }, 2*logInterval, logInterval/2)
			msg := sender.Messages[0].Raw().(message.Fields)
			for _, field := range []string{
				"num_builds_created",
				"num_tests_created",
				"num_appends",
				"num_builds_accessed",
				"num_all_build_logs_accessed",
				"num_test_logs_accessed",
			} {
				if field == testCase.field {
					assert.Equal(t, 1, msg[field])
				} else {
					assert.Equal(t, 0, msg[field])
				}
			}
		})
	}
}

func TestChannelFull(t *testing.T) {
	defer grip.SetSender(grip.GetSender())
	sender := send.NewMockSender("")
	grip.SetSender(sender)

	cache := statsCache{changeChan: make(chan func(*statsCache), 5)}
	for x := 0; x < 5; x++ {
		cache.BuildCreated()
	}
	assert.Len(t, sender.Messages, 0)

	assert.Error(t, cache.BuildCreated())
}

func TestLogSizeStats(t *testing.T) {
	defer grip.SetSender(grip.GetSender())

	t.Run("WithValues", func(t *testing.T) {
		sender := send.NewMockSender("")
		grip.SetSender(sender)

		cache := statsCache{
			logMBs: []float64{0, 15, 30},
		}
		cache.logStats()

		require.Len(t, sender.Messages, 1)
		msg := sender.Messages[0].Raw().(message.Fields)
		assert.EqualValues(t, 45, msg["append_size_total"])
		assert.EqualValues(t, 0, msg["append_size_min"])
		assert.EqualValues(t, 30, msg["append_size_max"])
		assert.EqualValues(t, 15, msg["append_size_mean"])
		assert.EqualValues(t, 15, msg["append_size_stddev"])
		assert.Equal(t, []float64{1, 0, 0, 0, 2}, msg["histogram"])
	})
	t.Run("WithoutValues", func(t *testing.T) {
		sender := send.NewMockSender("")
		grip.SetSender(sender)

		cache := statsCache{}
		cache.logStats()
		require.Len(t, sender.Messages, 1)
		_, ok := sender.Messages[0].Raw().(message.Fields)["append_size_min"]
		assert.False(t, ok)
	})
}