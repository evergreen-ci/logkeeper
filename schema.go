package logkeeper

import (
	"context"
	"fmt"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	deletePassedTestCutoff = 30 * (24 * time.Hour)
	logsName               = "logs"
	testsName              = "tests"
	buildsName             = "builds"
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
	Failed    bool                   `bson:"failed,omitempty"`
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
	Failed   bool                   `bson:"failed"`
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

	err := db.C(testsName).Find(bson.M{"_id": bson.ObjectIdHex(id)}).One(test)
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

	err := db.C(testsName).Find(bson.M{"build_id": queryBuildId}).Sort("started").All(&tests)
	if err != nil {
		return nil, err
	}
	return tests, nil
}

func findBuildById(db *mgo.Database, id string) (*LogKeeperBuild, error) {
	queryBuildId := idFromString(id)
	build := &LogKeeperBuild{}

	err := db.C(buildsName).Find(bson.M{"_id": queryBuildId}).One(build)
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

	err := db.C(buildsName).Find(bson.M{"builder": builder, "buildnum": buildnum}).One(build)
	if err == mgo.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return build, nil
}

func UpdateFailedBuild(id interface{}) error {
	if id == nil {
		return errors.New("no build id defined")
	}

	db, closer := db.DB()
	defer closer()

	err := db.C(buildsName).UpdateId(id, bson.M{"$set": bson.M{"failed": true}})

	return errors.Wrapf(err, "problem setting failed state on build %v", id)
}

func GetOldBuilds(limit int) ([]LogKeeperBuild, error) {
	db, closer := db.DB()
	defer closer()
	query := getOldBuildQuery()

	db.Session.SetSocketTimeout(2 * AmboyInterval)

	var builds []LogKeeperBuild
	err := db.C(buildsName).Find(query).Limit(limit).All(&builds)
	if err != nil {
		return nil, errors.Wrap(err, "error finding builds")
	}
	return builds, err
}

func getOldBuildQuery() bson.M {
	return bson.M{
		"started": bson.M{"$lte": time.Now().Add(-deletePassedTestCutoff)},
		"$or": []bson.M{
			{"failed": bson.M{"$exists": false}},
			{"failed": bson.M{"$eq": false}},
		},
		"$and": []bson.M{
			{"info.task_id": bson.M{"$exists": true}},
			{"info.task_id": bson.M{"$ne": ""}},
		},
	}

}

func StreamingGetOldBuilds(ctx context.Context) (<-chan LogKeeperBuild, <-chan error) {
	db, closer := db.DB()

	errOut := make(chan error)
	out := make(chan LogKeeperBuild)
	db.Session.SetSocketTimeout(5 * time.Minute)
	go func() {
		defer closer()
		defer close(errOut)
		defer close(out)
		defer recovery.LogStackTraceAndContinue("streaming query")

		iter := db.C(buildsName).Find(getOldBuildQuery()).Iter()
		build := LogKeeperBuild{}
		for iter.Next(&build) {
			out <- build
			build = LogKeeperBuild{}

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

func CleanupOldLogsAndTestsByBuild(id interface{}) (int, error) {
	if id == nil {
		return 0, errors.New("no build ID defined")
	}

	db, closer := db.DB()
	defer closer()

	var err error
	var num int

	info, err := db.C(logsName).RemoveAll(bson.M{"build_id": id})
	if err != nil {
		return num, errors.Wrap(err, "error deleting logs from old builds")
	}
	num += info.Removed

	info, err = db.C(testsName).RemoveAll(bson.M{"build_id": id})
	if err != nil {
		return num, errors.Wrap(err, "error deleting tests from old builds")
	}
	num += info.Removed

	err = db.C(buildsName).RemoveId(id)
	if err != nil {
		return num, errors.Wrap(err, "error deleting build record")
	}
	num++

	return num, nil
}
