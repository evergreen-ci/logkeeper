package model

import (
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"reflect"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	// TestsCollection is the name of the tests collection in the database.
	TestsCollection = "tests"
)

// Test contains metadata about a test's logs.
type Test struct {
	Id        TestID     `bson:"_id"`
	BuildId   string     `bson:"build_id"`
	BuildName string     `bson:"build_name"`
	Name      string     `bson:"name"`
	Command   string     `bson:"command"`
	Started   time.Time  `bson:"started"`
	Ended     *time.Time `bson:"ended"`
	Info      TestInfo   `bson:"info"`
	Failed    bool       `bson:"failed,omitempty"`
	Phase     string     `bson:"phase"`
	Seq       int        `bson:"seq"`
}

// TestInfo contains additional metadata about a test.
type TestInfo struct {
	// TaskID is the ID of the task in Evergreen that generated this test.
	TaskID string `bson:"task_id"`
}

type TestID string

func NewTestID(startTime time.Time) TestID {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint64(buf[:8], uint64(startTime.UnixNano()))
	randNum := rand.New(rand.NewSource(startTime.UnixNano())).Uint32()
	binary.BigEndian.PutUint32(buf[8:], randNum)

	return TestID(hex.Dump(buf))
}

func (t *TestID) Timestamp() time.Time {
	if t == nil {
		return time.Time{}
	}

	bytes, err := hex.DecodeString(string(*t))
	if err != nil {
		return time.Time{}
	}

	nSecs := binary.BigEndian.Uint64([]byte(bytes))
	return time.Unix(0, int64(nSecs))
}

func (t *Test) SetBSON(raw bson.Raw) error {
	var id interface{}
	if err := raw.Unmarshal(&id); err != nil {
		return &bson.TypeError{
			Kind: raw.Kind,
			Type: reflect.TypeOf(t),
		}
	}
	switch v := id.(type) {
	case bson.ObjectId:
		t.Id = TestID(v.Hex())
	case string:
		t.Id = TestID(v)
	default:
		return &bson.TypeError{
			Kind: raw.Kind,
			Type: reflect.TypeOf(t),
		}
	}

	return nil
}

// Insert inserts the test into the test collection.
func (t *Test) Insert() error {
	db, closeSession := db.DB()
	defer closeSession()

	return db.C(TestsCollection).Insert(t)
}

// IncrementSequence increments the test's sequence number by the given count.
func (t *Test) IncrementSequence(count int) error {
	db, closeSession := db.DB()
	defer closeSession()

	change := mgo.Change{Update: bson.M{"$inc": bson.M{"seq": count}}, ReturnNew: true}
	_, err := db.C("tests").Find(bson.M{"_id": t.Id}).Apply(change, t)
	return errors.Wrap(err, "incrementing test sequence number")
}

// FindTestByID returns the test with the specified ID.
func FindTestByID(id string) (*Test, error) {
	db, closeSession := db.DB()
	defer closeSession()

	in := []interface{}{TestID(id)}
	if bson.IsObjectIdHex(id) {
		in = append(in, bson.ObjectIdHex(id))
	}

	test := &Test{}
	err := db.C(TestsCollection).Find(bson.M{
		"_id": bson.M{"$in": in},
	}).One(test)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return test, nil
}

// FindTestsForBuild returns all the tests that are part of the given build.
func FindTestsForBuild(buildID string) ([]Test, error) {
	db, closeSession := db.DB()
	defer closeSession()

	tests := []Test{}
	err := db.C(TestsCollection).Find(bson.M{"build_id": buildID}).Sort("started").All(&tests)
	if err != nil {
		return nil, err
	}
	return tests, nil
}

// RemoveTestsForBuild removes all tests that are part of the given build.
func RemoveTestsForBuild(buildID string) (int, error) {
	db, closeSession := db.DB()
	defer closeSession()

	info, err := db.C(TestsCollection).RemoveAll(bson.M{"build_id": buildID})
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

// GetExecutionWindow returns the extents of the test.
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
