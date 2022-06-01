package logkeeper

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	deletePassedTestCutoff = 30 * (24 * time.Hour)
	logsName               = "logs"
	testsName              = "tests"
	buildsName             = "builds"
)

type Test struct {
	Id        primitive.ObjectID     `bson:"_id"`
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
	Id       string                 `bson:"_id"`
	Builder  string                 `bson:"builder"`
	BuildNum int                    `bson:"buildnum"`
	Started  time.Time              `bson:"started"`
	Name     string                 `bson:"name"`
	Info     map[string]interface{} `bson:"info"`
	Failed   bool                   `bson:"failed"`
	Phases   []string               `bson:"phases"`
	Seq      int                    `bson:"seq"`
}

func findTest(id string) (*Test, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, nil
	}

	test := &Test{}
	if err := db.C(testsName).FindOne(db.Context(), bson.M{"_id": objectID}).Decode(test); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "fetching test '%s'", id)
	}

	return test, nil
}

func findTestsForBuild(buildID string) ([]Test, error) {
	cur, err := db.C(testsName).Find(db.Context(), bson.M{"build_id": buildID}, options.Find().SetSort("started"))
	if err != nil {
		return nil, errors.Wrapf(err, "finding tests for build '%s'", buildID)
	}

	var tests []Test
	if err := cur.All(db.Context(), &tests); err != nil {
		return nil, errors.Wrapf(err, "decoding tests for build '%s'", buildID)
	}

	return tests, nil
}

func findBuildById(id string) (*LogKeeperBuild, error) {
	build := &LogKeeperBuild{}
	if err := db.C(buildsName).FindOne(db.Context(), bson.M{"_id": id}).Decode(build); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "fetching build '%s'", id)
	}

	return build, nil
}

func findBuildByBuilder(builder string, buildnum int) (*LogKeeperBuild, error) {
	build := &LogKeeperBuild{}
	if err := db.C(buildsName).FindOne(db.Context(), bson.M{"builder": builder, "buildnum": buildnum}).Decode(build); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "fetching builder '%s' build number '%d'", builder, buildnum)
	}

	return build, nil
}

func UpdateFailedBuild(id interface{}) error {
	if id == nil {
		return errors.New("no build id defined")
	}

	_, err := db.C(buildsName).UpdateByID(db.Context(), id, bson.M{"$set": bson.M{"failed": true}})
	return errors.Wrapf(err, "setting failed state on build %v", id)
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

func GetOldBuilds(limit int) ([]LogKeeperBuild, error) {
	cur, err := db.C(buildsName).Find(db.Context(), getOldBuildQuery(), options.Find().SetLimit(int64(limit)).SetMaxTime(2*AmboyInterval))
	if err != nil {
		return nil, errors.Wrap(err, "finding builds")
	}

	var builds []LogKeeperBuild
	if err := cur.All(db.Context(), &builds); err != nil {
		return nil, errors.Wrap(err, "decoding builds")
	}

	return builds, err
}

func StreamingGetOldBuilds(ctx context.Context) (<-chan LogKeeperBuild, <-chan error) {
	errOut := make(chan error)
	out := make(chan LogKeeperBuild)
	go func() {
		defer close(errOut)
		defer close(out)
		defer recovery.LogStackTraceAndContinue("streaming query")

		cur, err := db.C(buildsName).Find(db.Context(), getOldBuildQuery(), options.Find().SetMaxTime(5*time.Minute))
		if err != nil {
			errOut <- err
			return
		}

		for cur.Next(ctx) {
			build := LogKeeperBuild{}
			if err := cur.Decode(&build); err != nil {
				errOut <- err
				return
			}
			out <- build
			build = LogKeeperBuild{}
		}

		if err := cur.Err(); err != nil && err != ctx.Err() {
			errOut <- err
		}
	}()

	return out, errOut
}

func CleanupOldLogsAndTestsByBuild(id interface{}) (int, error) {
	if id == nil {
		return 0, errors.New("no build ID defined")
	}

	var num int
	result, err := db.C(logsName).DeleteMany(db.Context(), bson.M{"build_id": id})
	if err != nil {
		return num, errors.Wrap(err, "error deleting logs from old builds")
	}
	num += int(result.DeletedCount)

	result, err = db.C(testsName).DeleteMany(db.Context(), bson.M{"build_id": id})
	if err != nil {
		return num, errors.Wrap(err, "error deleting tests from old builds")
	}
	num += int(result.DeletedCount)

	result, err = db.C(buildsName).DeleteOne(db.Context(), bson.M{"_id": id})
	if err != nil {
		return num, errors.Wrap(err, "error deleting build record")
	}
	num++

	return num, nil
}
