//go:build test
// +build test

package testutil

import (
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/evergreen-ci/logkeeper/env"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func init() {
	connInfo := mgo.DialInfo{
		Addrs:   []string{"localhost"},
		Timeout: 5 * time.Second,
	}
	session, err := mgo.DialWithInfo(&connInfo)
	if err != nil {
		grip.EmergencyPanic(errors.Wrap(err, "can't connect to the db"))
	}

	if err = env.SetSession(session); err != nil {
		grip.EmergencyPanic(errors.Wrap(err, "setting session"))
	}

	env.SetDBName("logkeeper_test")
}

// ClearCollections clears all documents from all the specified collections,
// returning an error immediately if clearing any one of them fails.
func ClearCollections(collections ...string) error {
	db, closer := db.DB()
	defer closer()

	for _, collection := range collections {
		_, err := db.C(collection).RemoveAll(bson.M{})
		if err != nil {
			return errors.Wrapf(err, "clearing collection '%s'", collection)
		}
	}
	return nil
}
