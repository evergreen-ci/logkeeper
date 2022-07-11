package env

import (
	"context"
	"errors"
	"time"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/stat"
)

const (
	statChanBufferSize = 1000
	sizesLimit         = 100000
	logInterval        = 10 * time.Second
	bytesPerMB         = 1000000
)

var histogramDividers = []float64{0, 0.5, 1, 5, 10, 50}

type statsCache struct {
	buildsCreated int
	testsCreated  int
	logMBs        []float64

	buildsAccessed       int
	allBuildLogsAccessed int
	testLogsAccessed     int

	changeChan chan func(*statsCache)
	lastReset  time.Time
}

// BuildCreated records that a build has been created.
// Returns an error if events are enqueued faster than the cache can process them.
func (s *statsCache) BuildCreated() error {
	return s.enqueueChange(func(s *statsCache) { s.buildsCreated++ })
}

// TestCreated records that a test has been created.
// Returns an error if events are enqueued faster than the cache can process them.
func (s *statsCache) TestCreated() error {
	return s.enqueueChange(func(s *statsCache) { s.testsCreated++ })
}

// LogAppended records that a log has been appended.
// Returns an error if events are enqueued faster than the cache can process them.
func (s *statsCache) LogAppended(numBytes int) error {
	return s.enqueueChange(func(s *statsCache) { s.logMBs = append(s.logMBs, float64(numBytes)/bytesPerMB) })
}

// BuildAccessed records that a build has been accessed.
// Returns an error if events are enqueued faster than the cache can process them.
func (s *statsCache) BuildAccessed() error {
	return s.enqueueChange(func(s *statsCache) { s.buildsAccessed++ })
}

// TestLogsAccessed records that a test's logs have been accessed.
// Returns an error if events are enqueued faster than the cache can process them.
func (s *statsCache) TestLogsAccessed() error {
	return s.enqueueChange(func(s *statsCache) { s.testLogsAccessed++ })
}

// AllLogsAccessed records that all of a build's logs have been accessed.
// Returns an error if events are enqueued faster than the cache can process them.
func (s *statsCache) AllLogsAccessed() error {
	return s.enqueueChange(func(s *statsCache) { s.allBuildLogsAccessed++ })
}

func (s *statsCache) enqueueChange(change func(*statsCache)) error {
	select {
	case s.changeChan <- change:
		return nil
	default:
		return errors.New("incoming stats buffer is full")
	}
}

func (s *statsCache) flushStats() {
	stats := message.Fields{
		"message":                     "usage stats",
		"interval_ms":                 time.Since(s.lastReset).Milliseconds(),
		"num_builds_created":          s.buildsCreated,
		"num_tests_created":           s.testsCreated,
		"num_appends":                 len(s.logMBs),
		"num_builds_accessed":         s.buildsAccessed,
		"num_all_build_logs_accessed": s.allBuildLogsAccessed,
		"num_test_logs_accessed":      s.testLogsAccessed,
	}
	if len(s.logMBs) > 0 {
		stats["append_size_total"] = floats.Sum(s.logMBs)
		stats["append_size_min"] = floats.Min(s.logMBs)
		stats["append_size_max"] = floats.Max(s.logMBs)
		stats["append_size_mean"] = stat.Mean(s.logMBs, nil)
		stats["append_size_stddev"] = stat.StdDev(s.logMBs, nil)
		stats["histogram"] = stat.Histogram(nil, histogramDividers, s.logMBs, nil)
	}
	grip.Info(stats)

	s.resetCache()
}

func (s *statsCache) resetCache() {
	s.buildsCreated = 0
	s.testsCreated = 0
	s.logMBs = s.logMBs[:0]

	s.buildsAccessed = 0
	s.allBuildLogsAccessed = 0
	s.testLogsAccessed = 0

	s.lastReset = time.Now()
}

func (s *statsCache) loggerLoop(ctx context.Context) {
	defer func() {
		if err := recovery.HandlePanicWithError(recover(), nil, "stats cache logger"); err != nil {
			grip.Error(message.WrapError(err, message.Fields{
				"message": "panic in stats cache logger loop",
			}))
		}
	}()

	ticker := time.NewTicker(logInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flushStats()
		case applyChange := <-s.changeChan:
			applyChange(s)

			if len(s.logMBs) >= sizesLimit {
				s.flushStats()
				ticker.Reset(logInterval)
			}
		}
	}
}

// NewStatsCache returns an initialized stats cache and begins processing incoming events.
func NewStatsCache(ctx context.Context) *statsCache {
	cache := statsCache{
		lastReset:  time.Now(),
		changeChan: make(chan func(*statsCache), statChanBufferSize),
	}
	go cache.loggerLoop(ctx)

	return &cache
}