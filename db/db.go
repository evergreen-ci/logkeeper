package db

import (
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	mgo "gopkg.in/mgo.v2"
)

const defaultSocketTimeout = 90 * time.Second

func Session() *mgo.Session {
	s := env.Session().Copy()
	s.SetSocketTimeout(defaultSocketTimeout)
	return s
}

func DB() (*mgo.Database, func()) {
	ses := env.Session()
	return ses.DB(env.DBName()), ses.Close
}
