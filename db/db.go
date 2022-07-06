package db

import (
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	mgo "gopkg.in/mgo.v2"
)

const defaultSocketTimeout = 90 * time.Second

// Session returns a copy of the global mgo session.
func Session() *mgo.Session {
	s := env.Session().Copy()
	s.SetSocketTimeout(defaultSocketTimeout)
	return s
}

// DB returns an mgo Database for the global DBName.
func DB() (*mgo.Database, func()) {
	ses := Session()
	return Session().DB(env.DBName()), ses.Close
}
