package env

import (
	"context"
	"sync"

	"github.com/mongodb/amboy"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
)

type environment struct {
	ctx    context.Context
	client *mongo.Client
	dbName string

	cleanupQueue amboy.Queue
	stats        *statsCache

	sync.RWMutex
}

var globalEnv *environment

func init() {
	globalEnv = &environment{}
}

// SetContext caches a context to be available from the environment.
func SetContext(ctx context.Context) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.ctx = ctx
}

// Context returns the cached context from the environment.
func Context() context.Context {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.ctx
}

// SetClient caches a Mongo Client to be available from the environment.
func SetClient(c *mongo.Client) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.client = c
}

// Client returns the cached Mongo Client from the environment.
func Client() *mongo.Client {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.client
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
