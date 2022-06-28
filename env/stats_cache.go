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
	logInterval        = 10 * time.Second
	bytesPerMB         = 1000000
)

var histogramDividers = []float64{0, 1, 10, 100, 1000, 10000, 100000, 1000000, 1000000000}

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

func (s *statsCache) BuildCreated() error {
	return s.enqueueChange(func(s *statsCache) { s.buildsCreated++ })
}

func (s *statsCache) TestCreated() error {
	return s.enqueueChange(func(s *statsCache) { s.testsCreated++ })
}

func (s *statsCache) LogAppended(numBytes int) error {
	return s.enqueueChange(func(s *statsCache) { s.logMBs = append(s.logMBs, float64(numBytes)/bytesPerMB) })
}

func (s *statsCache) BuildAccessed() error {
	return s.enqueueChange(func(s *statsCache) { s.buildsAccessed++ })
}

func (s *statsCache) TestLogsAccessed() error {
	return s.enqueueChange(func(s *statsCache) { s.testLogsAccessed++ })
}

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

func (s *statsCache) logStats() {
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
		stats["append_size_min"] = floats.Min(s.logMBs)
		stats["append_size_max"] = floats.Max(s.logMBs)
		stats["append_size_mean"] = stat.Mean(s.logMBs, nil)
		stats["append_size_stddev"] = stat.StdDev(s.logMBs, nil)
		stats["histogram"] = stat.Histogram(nil, histogramDividers, s.logMBs, nil)
	}
	grip.Info(stats)
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
			s.logStats()
			s.resetCache()
		case applyChange := <-s.changeChan:
			applyChange(s)
		}
	}
}

func NewCache(ctx context.Context) *statsCache {
	cache := statsCache{
		lastReset:  time.Now(),
		changeChan: make(chan func(*statsCache), statChanBufferSize),
	}
	go cache.loggerLoop(ctx)

	return &cache
}
