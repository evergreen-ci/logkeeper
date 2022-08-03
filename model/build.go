package model

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	// DeletePassedTestCutoff is the TTL for passed tests.
	DeletePassedTestCutoff = 30 * (24 * time.Hour)
	// BuildsCollection is the name of the builds collection in the database.
	BuildsCollection = "builds"
)

// Build contains metadata about a build.
type Build struct {
	Id       string    `bson:"_id"`
	Builder  string    `bson:"builder"`
	BuildNum int       `bson:"buildnum"`
	Started  time.Time `bson:"started"`
	Name     string    `bson:"name"`
	Info     BuildInfo `bson:"info"`
	Failed   bool      `bson:"failed"`
	Phases   []string  `bson:"phases"`
	Seq      int       `bson:"seq"`
}

// BuildInfo contains additional metadata about a build.
type BuildInfo struct {
	// TaskID is the ID of the task in Evergreen that generated this build.
	TaskID string `bson:"task_id" json:"task_id"`
}

// Insert inserts the build into the builds collection.
func (b *Build) Insert() error {
	db, closeSession := db.DB()
	defer closeSession()

	return db.C(BuildsCollection).Insert(b)
}

// FindBuildById returns the build with the given id.
func FindBuildById(id string) (*Build, error) {
	db, closeSession := db.DB()
	defer closeSession()

	build := &Build{}
	err := db.C(BuildsCollection).Find(bson.M{"_id": id}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

// FindBuildByBuilder returns the build corresponding to the builder and buildnum.
func FindBuildByBuilder(builder string, buildnum int) (*Build, error) {
	db, closeSession := db.DB()
	defer closeSession()

	build := &Build{}
	err := db.C(BuildsCollection).Find(bson.M{"builder": builder, "buildnum": buildnum}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

// UpdateFailedBuild sets the failed field for the build with the given id.
func UpdateFailedBuild(id string) error {
	db, closeSession := db.DB()
	defer closeSession()

	err := db.C(BuildsCollection).UpdateId(id, bson.M{"$set": bson.M{"failed": true}})
	return errors.Wrapf(err, "problem setting failed state on build %v", id)
}

// IncrementSequence increments the build's sequence number by the given count.
func (b *Build) IncrementSequence(count int) error {
	db, closeSession := db.DB()
	defer closeSession()

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": count}}, ReturnNew: true}
	_, err := db.C("builds").Find(bson.M{"_id": b.Id}).Apply(change, b)
	return errors.Wrapf(err, "incrementing sequence number for build '%s'", b.Id)
}

// StreamingGetOldBuilds returns a channel containing builds that are ready to be deleted
// and a channel for any errors encountered.
// The channels are closed when all the matching builds have been returned or we encounter an error.
func StreamingGetOldBuilds(ctx context.Context) (<-chan Build, <-chan error) {
	db, closeSession := db.DB()

	errOut := make(chan error)
	out := make(chan Build)
	db.Session.SetSocketTimeout(5 * time.Minute)
	go func() {
		defer closeSession()
		defer close(errOut)
		defer close(out)
		defer recovery.LogStackTraceAndContinue("streaming query")

		iter := db.C(BuildsCollection).Find(bson.M{
			"started": bson.M{"$lte": time.Now().Add(-DeletePassedTestCutoff)},
			"$or": []bson.M{
				{"failed": bson.M{"$exists": false}},
				{"failed": bson.M{"$eq": false}},
			},
			"$and": []bson.M{
				{"info.task_id": bson.M{"$exists": true}},
				{"info.task_id": bson.M{"$ne": ""}},
			},
		}).Iter()
		build := Build{}
		for iter.Next(&build) {
			out <- build
			build = Build{}

			if ctx.Err() != nil {
				return
			}
		}

		if err := iter.Err(); err != nil {
			errOut <- err
			return
		}
	}()

	return out, errOut
}

// RemoveBuild removes the build with the given ID from the database.
func RemoveBuild(buildID string) error {
	db, closeSession := db.DB()
	defer closeSession()

	return errors.Wrap(db.C(BuildsCollection).RemoveId(buildID), "deleting build record")
}
