package logkeeper

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

	changeChan chan func()
	lastReset  time.Time
}

func (s *statsCache) buildCreated() error {
	return s.enqueueChange(func() { s.buildsCreated++ })
}

func (s *statsCache) testCreated() error {
	return s.enqueueChange(func() { s.testsCreated++ })
}

func (s *statsCache) logAppended(numBytes int) error {
	return s.enqueueChange(func() { s.logMBs = append(s.logMBs, float64(numBytes)/bytesPerMB) })
}

func (s *statsCache) buildAccessed() error {
	return s.enqueueChange(func() { s.buildsAccessed++ })
}

func (s *statsCache) testLogAccessed() error {
	return s.enqueueChange(func() { s.testLogsAccessed++ })
}

func (s *statsCache) allLogsAccessed() error {
	return s.enqueueChange(func() { s.allBuildLogsAccessed++ })
}

func (s *statsCache) enqueueChange(change func()) error {
	select {
	case s.changeChan <- change:
		return nil
	default:
		return errors.New("stats cache is full")
	}
}

func (s *statsCache) logStats() {
	grip.Info(message.Fields{
		"message":            "upload stats",
		"interval_ms":        time.Since(s.lastReset).Milliseconds(),
		"num_builds_added":   s.buildsCreated,
		"num_tests_added":    s.testsCreated,
		"num_log_appends":    len(s.logMBs),
		"append_size_min":    floats.Min(s.logMBs),
		"append_size_max":    floats.Max(s.logMBs),
		"append_size_mean":   stat.Mean(s.logMBs, nil),
		"append_size_stddev": stat.StdDev(s.logMBs, nil),
		"histogram":          stat.Histogram(nil, histogramDividers, s.logMBs, nil),
	})

	grip.Info(message.Fields{
		"message":                     "download stats",
		"num_builds_accessed":         s.buildsAccessed,
		"num_all_build_logs_accessed": s.allBuildLogsAccessed,
		"num_test_logs_accessed":      s.testLogsAccessed,
	})
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
			applyChange()
		}
	}
}

func newCache(ctx context.Context) statsCache {
	cache := statsCache{
		lastReset:  time.Now(),
		changeChan: make(chan func(), statChanBufferSize),
	}
	go cache.loggerLoop(ctx)

	return cache
}
