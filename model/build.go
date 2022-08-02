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
	DeletePassedTestCutoff = 30 * (24 * time.Hour)
	BuildsCollection       = "builds"
)

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

type BuildInfo struct {
	TaskID string `bson:"task_id" json:"task_id"`
}

func (b *Build) Insert() error {
	db, closeSession := db.DB()
	defer closeSession()

	return db.C(BuildsCollection).Insert(b)
}

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

func UpdateFailedBuild(id string) error {
	db, closeSession := db.DB()
	defer closeSession()

	err := db.C(BuildsCollection).UpdateId(id, bson.M{"$set": bson.M{"failed": true}})
	return errors.Wrapf(err, "problem setting failed state on build %v", id)
}

func (b *Build) IncrementSequence(count int) error {
	db, closeSession := db.DB()
	defer closeSession()

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": count}}, ReturnNew: true}
	_, err := db.C("builds").Find(bson.M{"_id": b.Id}).Apply(change, b)
	return errors.Wrapf(err, "incrementing sequence number for build '%s'", b.Id)
}

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

func RemoveBuild(buildID string) error {
	db, closeSession := db.DB()
	defer closeSession()

	return errors.Wrap(db.C(BuildsCollection).RemoveId(buildID), "deleting build record")
}
