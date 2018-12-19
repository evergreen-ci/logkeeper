package db

import (
	"sync"
	"time"

	"github.com/pkg/errors"
	mgo "gopkg.in/mgo.v2"
)

type sessionCache struct {
	s      *mgo.Session
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

	return session.s.Copy()
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
