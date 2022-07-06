package env

import (
	"sync"

	"github.com/mongodb/amboy"
	"github.com/pkg/errors"
	mgo "gopkg.in/mgo.v2"
)

type environment struct {
	dbSession    *mgo.Session
	cleanupQueue amboy.Queue
	dbName       string

	sync.RWMutex
}

var globalEnv *environment

func init() {
	globalEnv = &environment{}
}

func SetSession(s *mgo.Session) error {
	if s == nil {
		return errors.New("cannot set a nil session")
	}

	globalEnv.Lock()
	defer globalEnv.Unlock()
	globalEnv.dbSession = s

	return nil
}

func Session() *mgo.Session {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.dbSession
}

func SetDBName(name string) {
	globalEnv.Lock()
	defer globalEnv.Unlock()

	globalEnv.dbName = name
}

func DBName() string {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.dbName
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

func CleanupQueue() amboy.Queue {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.cleanupQueue
}
