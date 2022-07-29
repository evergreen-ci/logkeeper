package model

import (
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	testsCollection = "tests"
)

type Test struct {
	Id        bson.ObjectId `bson:"_id"`
	BuildId   interface{}   `bson:"build_id"`
	BuildName string        `bson:"build_name"`
	Name      string        `bson:"name"`
	Command   string        `bson:"command"`
	Started   time.Time     `bson:"started"`
	Ended     *time.Time    `bson:"ended"`
	Info      TestInfo      `bson:"info"`
	Failed    bool          `bson:"failed,omitempty"`
	Phase     string        `bson:"phase"`
	Seq       int           `bson:"seq"`
}

type TestInfo struct {
	TaskID string `bson:"task_id"`
}

func (t *Test) Insert() error {
	db, closeSession := db.DB()
	defer closeSession()

	return db.C(testsCollection).Insert(t)
}

func (t *Test) IncrementSequence(count int) error {
	db, closeSession := db.DB()
	defer closeSession()

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": count}}, ReturnNew: true}
	_, err := db.C("tests").Find(bson.M{"_id": t.Id}).Apply(change, t)
	return errors.Wrap(err, "incrementing test sequence number")
}

func FindTest(id string) (*Test, error) {
	db, closeSession := db.DB()
	defer closeSession()

	if !bson.IsObjectIdHex(id) {
		return nil, nil
	}
	test := &Test{}

	err := db.C(testsCollection).Find(bson.M{"_id": bson.ObjectIdHex(id)}).One(test)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return test, nil
}

func FindTestsForBuild(buildID string) ([]Test, error) {
	db, closeSession := db.DB()
	defer closeSession()

	tests := []Test{}
	err := db.C(testsCollection).Find(bson.M{"build_id": buildID}).Sort("started").All(&tests)
	if err != nil {
		return nil, err
	}
	return tests, nil
}

func RemoveTestsForBuild(buildID string) (int, error) {
	db, closeSession := db.DB()
	defer closeSession()

	info, err := db.C(testsCollection).RemoveAll(bson.M{"build_id": buildID})
	if err != nil {
		return 0, errors.Wrapf(err, "deleting tests for build '%s'", buildID)
	}

	return info.Removed, nil
}

func (t *Test) findNext() (*Test, error) {
	db, closeSession := db.DB()
	defer closeSession()

	nextTest := &Test{}
	if err := db.C("tests").Find(bson.M{"build_id": t.BuildId, "started": bson.M{"$gt": t.Started}}).Sort("started").Limit(1).One(nextTest); err != nil {
		if err != mgo.ErrNotFound {
			return nil, err
		}
		return nil, nil
	}

	return nextTest, nil
}

func (t *Test) GetExecutionWindow() (time.Time, *time.Time, error) {
	var maxTime *time.Time
	nextTest, err := t.findNext()
	if err != nil {
		return time.Time{}, nil, errors.Wrap(err, "getting next test")
	}
	if nextTest != nil {
		maxTime = &nextTest.Started
	}

	return t.Started, maxTime, nil
}
