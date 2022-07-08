package db

import (
	"time"

	"github.com/evergreen-ci/logkeeper/env"
	mgo "gopkg.in/mgo.v2"
)

const defaultSocketTimeout = 90 * time.Second

// DB returns an mgo Database for the global DBName and the mgo session closer func.
func DB() (*mgo.Database, func()) {
	s := env.Session().Copy()
	s.SetSocketTimeout(defaultSocketTimeout)
	return s.DB(env.DBName()), s.Close
}
