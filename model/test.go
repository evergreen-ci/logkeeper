package model

import (
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/evergreen-ci/logkeeper/featureswitch"
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

// NewTestID returns a new TestID with it's timestamp set to startTime.
// The ID is an ObjectID with its timestamp replaced with a nanosecond timestamp.
// It is represented as a hex string of 16 bytes. The first 8 bytes are the timestamp
// and replace the first 4 bytes of an ObjectID. The remaining 8 bytes are the rest of
// the ObjectID.
func NewTestID(startTime time.Time) TestID {
	objectID := bson.NewObjectId()
	if !featureswitch.NewTestIDEnabled(objectID.Hex()) {
		return TestID(objectID.Hex())
	}

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(startTime.UnixNano()))
	buf = append(buf, []byte(objectID)[4:]...)

	return TestID(hex.EncodeToString(buf))
}

// Timestamp returns the timestamp encoded in the TestID. If the TestID is wrapping
// a legacy ObjectID then the timestamp will have second precision while if the TestID
// is a new ID it will have nanosecond precision.
func (t *TestID) Timestamp() time.Time {
	if t == nil {
		return time.Time{}
	}

	if bson.IsObjectIdHex(string(*t)) {
		return bson.ObjectIdHex(string(*t)).Time()
	}

	bytes, err := hex.DecodeString(string(*t))
	if err != nil {
		return time.Time{}
	}

	nSecs := binary.BigEndian.Uint64([]byte(bytes))
	return time.Unix(0, int64(nSecs))
}

// GetBSON implements the bson.Getter interface.
// When a TestID is marshalled to BSON the driver will marshal the output
// of this function instead of the struct.
func (t TestID) GetBSON() (interface{}, error) {
	if bson.IsObjectIdHex((string(t))) {
		return bson.ObjectIdHex(string(t)), nil
	}

	return string(t), nil
}

// SetBSON implements the bson.Setter interface.
// When a TestID is unmarshalled from BSON the driver will call this function to
// unmarshal into the TestID.
func (t *TestID) SetBSON(raw bson.Raw) error {
	var id interface{}
	if err := raw.Unmarshal(&id); err != nil {
		return &bson.TypeError{
			Kind: raw.Kind,
			Type: reflect.TypeOf(t),
		}
	}
	switch v := id.(type) {
	case bson.ObjectId:
		*t = TestID(v.Hex())
	case string:
		*t = TestID(v)
	default:
		return &bson.TypeError{
			Kind: raw.Kind,
			Type: reflect.TypeOf(t),
		}
	}

	return nil
}

func (t *TestID) toTestIDAliasPtr() *testIDAlias {
	if t == nil {
		return nil
	}

	alias := testIDAlias(*t)
	return &alias
}

// testIDAlias aliases the TestID so it can implement the bson.Getter interface with a pointer receiver.
// This is necessary for the Log type which references a pointer to a TestID. If the pointer is nil
// a call to the GetBSON method with a value receiver will panic.
type testIDAlias TestID

// GetBSON implements the bson.Getter interface.
// When a testIDAlias is marshalled to BSON the driver will marshal the output
// of this function instead of the struct.
func (t *testIDAlias) GetBSON() (interface{}, error) {
	if t == nil {
		return nil, nil
	}

	return TestID(*t).GetBSON()
}

// SetBSON implements the bson.Setter interface.
// When a testIDAlias is unmarshalled from BSON the driver will call this function to
// unmarshal into the TestID.
func (t *testIDAlias) SetBSON(raw bson.Raw) error {
	var id interface{}
	if err := raw.Unmarshal(&id); err != nil {
		return &bson.TypeError{
			Kind: raw.Kind,
			Type: reflect.TypeOf(t),
		}
	}

	var testID TestID
	err := testID.SetBSON(raw)
	if err != nil {
		return err
	} else {
		*t = testIDAlias(testID)
	}

	return nil
}

func (t *testIDAlias) toTestIDPtr() *TestID {
	if t == nil {
		return nil
	}

	id := TestID(*t)
	return &id
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

	test := &Test{}
	err := db.C(TestsCollection).Find(bson.M{"_id": TestID(id)}).One(test)
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
