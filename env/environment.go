package env

import (
	"sync"

	"github.com/mongodb/amboy"
	"github.com/pkg/errors"
	mgo "gopkg.in/mgo.v2"
)

type environment struct {
	dbSession    *mgo.Session
	dbName       string
	cleanupQueue amboy.Queue
	stats        *statsCache

	sync.RWMutex
}

var globalEnv *environment

func init() {
	globalEnv = &environment{}
}

// SetSession caches a mgo session to be available from the environment.
func SetSession(s *mgo.Session) error {
	if s == nil {
		return errors.New("cannot set a nil session")
	}

	globalEnv.Lock()
	defer globalEnv.Unlock()
	globalEnv.dbSession = s

	return nil
}

// Session returns the cached mgo session from the environment.
func Session() *mgo.Session {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.dbSession
}

// SetDBName caches a DB name to be available from the environment.
func SetDBName(name string) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.dbName = name
}

// DBName returns the cached DB name from the environment.
func DBName() string {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.dbName
}

// SetStatsCache caches a stats cache to be available from the environment.
func SetStatsCache(s *statsCache) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.stats = s
}

// StatsCache returns the cached stats cache from the environment.
func StatsCache() *statsCache {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.stats
}

// SetCleanupQueue caches the cleanup queue to be available from the environment.
func SetCleanupQueue(q amboy.Queue) error {
	if !q.Info().Started {
		return errors.New("queue isn't started")
	}

	globalEnv.Lock()
	defer globalEnv.Unlock()
	globalEnv.cleanupQueue = q
	return nil
}

// CleanupQueue returns the cached cleanup queue from the environment.
func CleanupQueue() amboy.Queue {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.cleanupQueue
}
