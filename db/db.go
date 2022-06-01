package db

import (
	"context"
	"sync"
	"time"

	"github.com/mongodb/amboy"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo/mongo"
)

type sessionCache struct {
	client *mongo.Client
	ctx    context.Context
	dbName string

	mq amboy.Queue

	sync.RWMutex
}

var session *sessionCache

const defaultSocketTimeout = 90 * time.Second

func init() {
	session = &sessionCache{}
}

func SetContext(ctx context.Context) {
	session.Lock()
	defer session.Unlock()

	session.ctx = ctx
}

func Context() context.Context {
	session.RLock()
	defer session.RUnlock()

	return session.ctx
}

func Client() *mongo.Client {
	session.RLock()
	defer session.RUnlock()

	return session.client
}

func SetClient(c *mongo.Client) {
	session.Lock()
	defer session.Unlock()

	session.client = c
}

func DB() *mongo.Database {
	session.RLock()
	defer session.RUnlock()

	return session.client.Database(session.dbName)
}

func C(collectionName string) *mongo.Collection {
	session.RLock()
	defer session.RUnlock()

	return session.client.Database(session.dbName).Collection(collectionName)
}

func SetDBName(name string) {
	session.Lock()
	defer session.Unlock()

	session.dbName = name
}

func SetMigrationQueue(q amboy.Queue) error {
	if !q.Started() {
		return errors.New("queue isn't started")
	}

	session.Lock()
	defer session.Unlock()

	session.mq = q
	return nil
}

func GetMigrationQueue() amboy.Queue {
	session.RLock()
	defer session.RUnlock()

	return session.mq
}
