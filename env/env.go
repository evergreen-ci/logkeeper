package env

import (
	"context"
	"sync"

	"github.com/mongodb/amboy"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
)

type environment struct {
	client *mongo.Client
	ctx    context.Context
	dbName string

	cleanupQueue amboy.Queue
	stats        *statsCache

	sync.RWMutex
}

var globalEnv *environment

func init() {
	globalEnv = &environment{}
}

func SetContext(ctx context.Context) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.ctx = ctx
}

func Context() context.Context {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.ctx
}

func Client() *mongo.Client {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.client
}

func SetClient(c *mongo.Client) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.client = c
}

func DB() *mongo.Database {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.client.Database(globalEnv.dbName)
}

func C(collectionName string) *mongo.Collection {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.client.Database(globalEnv.dbName).Collection(collectionName)
}

func SetDBName(name string) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.dbName = name
}

func SetCleanupQueue(q amboy.Queue) error {
	if !q.Info().Started {
		return errors.New("queue isn't started")
	}

	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.cleanupQueue = q
	return nil
}

func GetCleanupQueue() amboy.Queue {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.cleanupQueue
}

func SetStatsCache(s *statsCache) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.stats = s
}

func GetStatsCache() *statsCache {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.stats
}
