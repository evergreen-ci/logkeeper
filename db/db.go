package db

import (
	"sync"
	"time"

	"github.com/mongodb/amboy"
	"github.com/pkg/errors"
	mgo "gopkg.in/mgo.v2"
)

type sessionCache struct {
	s      *mgo.Session
	mq     amboy.Queue
	rq     amboy.Queue
	dbName string
	sync.RWMutex
}

var session *sessionCache

const defaultSocketTimeout = 90 * time.Second

func init() {
	session = &sessionCache{}
}

func GetSession() *mgo.Session {
	session.RLock()
	defer session.RUnlock()

	if session.s == nil {
		panic("no database connection")
	}

	s := session.s.Copy()
	s.SetSocketTimeout(defaultSocketTimeout)
	return s
}

func SetSession(s *mgo.Session) error {
	session.Lock()
	defer session.Unlock()

	if s == nil {
		return errors.New("cannot set a nil session")
	}

	s.SetSocketTimeout(defaultSocketTimeout)
	session.s = s

	return nil
}

func GetDatabase() (*mgo.Database, func()) {
	session.RLock()
	defer session.RUnlock()

	ses := GetSession()
	return ses.DB(session.dbName), ses.Close
}

func SetDatabase(name string) {
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

func SetQueue(q amboy.Queue) error {
	if !q.Started() {
		return errors.New("queue isn't started")
	}

	session.Lock()
	defer session.Unlock()

	session.rq = q
	return nil
}

func GetQueue() amboy.Queue {
	session.RLock()
	defer session.RUnlock()

	return session.rq
}
