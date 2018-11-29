package logkeeper

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	approxMonth            = 30 * (time.Hour * 24)
	deletePassedTestCutoff = 3 * approxMonth // ~3 months
	maxTests               = 1000
)

type Test struct {
	Id        bson.ObjectId          `bson:"_id"`
	BuildId   interface{}            `bson:"build_id"`
	BuildName string                 `bson:"build_name"`
	Name      string                 `bson:"name"`
	Command   string                 `bson:"command"`
	Started   time.Time              `bson:"started"`
	Ended     *time.Time             `bson:"ended"`
	Info      map[string]interface{} `bson:"info"`
	Failed    bool                   `bson:"failed"`
	Phase     string                 `bson:"phase"`
	Seq       int                    `bson:"seq"`
}

type LogKeeperBuild struct {
	Id       interface{}            `bson:"_id"`
	Builder  string                 `bson:"builder"`
	BuildNum int                    `bson:"buildnum"`
	Started  time.Time              `bson:"started"`
	Name     string                 `bson:"name"`
	Info     map[string]interface{} `bson:"info"`
	Phases   []string               `bson:"phases"`
	Seq      int                    `bson:"seq"`
}

// If "raw" is a bson.ObjectId, returns the string value of its .Hex() function.
// Otherwise, returns it's string representation if it implements Stringer, or
// string representation generated by fmt's %v formatter.
func stringifyId(raw interface{}) string {
	if buildObjId, ok := raw.(bson.ObjectId); ok {
		return buildObjId.Hex()
	}
	if asStr, ok := raw.(fmt.Stringer); ok {
		return asStr.String()
	}
	return fmt.Sprintf("%v", raw)
}

func idFromString(raw string) interface{} {
	if bson.IsObjectIdHex(raw) {
		return bson.ObjectIdHex(raw)
	}
	return raw
}

func findTest(db *mgo.Database, id string) (*Test, error) {
	if !bson.IsObjectIdHex(id) {
		return nil, nil
	}
	test := &Test{}

	err := db.C("tests").Find(bson.M{"_id": bson.ObjectIdHex(id)}).One(test)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return test, nil
}

func findTestsForBuild(db *mgo.Database, buildId string) ([]Test, error) {
	queryBuildId := idFromString(buildId)
	tests := []Test{}

	err := db.C("tests").Find(bson.M{"build_id": queryBuildId}).Sort("started").All(&tests)
	if err != nil {
		return nil, err
	}
	return tests, nil
}

func findBuildById(db *mgo.Database, id string) (*LogKeeperBuild, error) {
	queryBuildId := idFromString(id)
	build := &LogKeeperBuild{}

	err := db.C("builds").Find(bson.M{"_id": queryBuildId}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

func findBuildByBuilder(db *mgo.Database, builder string, buildnum int) (*LogKeeperBuild, error) {
	build := &LogKeeperBuild{}

	err := db.C("builds").Find(bson.M{"builder": builder, "buildnum": buildnum}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

func UpdateFailedTest(db *mgo.Database, id bson.ObjectId) error {
	update := bson.M{"failed": true}
	return db.C("tests").UpdateId(id, update)
}

func GetOldTests(db *mgo.Database, now time.Time) (*[]Test, error) {
	query := bson.M{
		"started": bson.M{"$lte": now.Add(-deletePassedTestCutoff)},
		"failed":  false,
	}
	tests := []Test{}
	err := db.C("tests").Find(query).Sort("-started").Limit(maxTests).All(&tests)
	if err != nil {
		return nil, errors.Wrap(err, "error finding tests")
	}
	return &tests, err
}

func CleanupOldLogsByTest(db *mgo.Database, id bson.ObjectId) error {
	err := db.C("tests").RemoveId(id)
	if err != nil {
		return errors.Wrap(err, "error deleting test")
	}

	_, err = db.C("logs").RemoveAll(bson.M{"test_id": id})
	if err != nil {
		return errors.Wrap(err, "error deleting logs from old tests")
	}
	return nil
}
