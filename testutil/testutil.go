package testutil

import (
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/evergreen-ci/logkeeper/env"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func InitDB() error {
	connInfo := mgo.DialInfo{
		Addrs:   []string{"localhost"},
		Timeout: 5 * time.Second,
	}
	session, err := mgo.DialWithInfo(&connInfo)
	if err != nil {
		return errors.Wrap(err, "connecting to db")
	}

	if err = env.SetSession(session); err != nil {
		return errors.Wrap(err, "setting session")
	}

	env.SetDBName("logkeeper_test")

	return nil
}

// ClearCollections clears all documents from all the specified collections,
// returning an error immediately if clearing any one of them fails.
func ClearCollections(collections ...string) error {
	db, closer := db.DB()
	defer closer()

	for _, collection := range collections {
		_, err := db.C(collection).RemoveAll(bson.M{})
		if err != nil {
			return errors.Wrapf(err, "clearign collection '%s'", collection)
		}
	}
	return nil
}
