package logkeeper

import (
	"context"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	deletePassedTestCutoff = 30 * (24 * time.Hour)
	logsCollection         = "logs"
	testsCollection        = "tests"
	buildsCollection       = "builds"
)

type Test struct {
	Id        primitive.ObjectID     `bson:"_id"`
	BuildId   string                 `bson:"build_id"`
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
	if err := db.C(testsCollection).FindOne(db.Context(), bson.M{"_id": objectID}).Decode(test); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "fetching test '%s'", id)
	}

	return test, nil
}

func findTestsForBuild(buildID string) ([]Test, error) {
	cur, err := db.C(testsCollection).Find(db.Context(), bson.M{"build_id": buildID}, options.Find().SetSort(bson.M{"started": 1}))
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
	if err := db.C(buildsCollection).FindOne(db.Context(), bson.M{"_id": id}).Decode(build); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "fetching build '%s'", id)
	}

	return build, nil
}

func findBuildByBuilder(builder string, buildnum int) (*LogKeeperBuild, error) {
	build := &LogKeeperBuild{}
	if err := db.C(buildsCollection).FindOne(db.Context(), bson.M{"builder": builder, "buildnum": buildnum}).Decode(build); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "fetching builder '%s' build number %d", builder, buildnum)
	}

	return build, nil
}

func UpdateFailedBuild(id string) error {
	_, err := db.C(buildsCollection).UpdateByID(db.Context(), id, bson.M{"$set": bson.M{"failed": true}})
	return errors.Wrapf(err, "setting failed state on build '%s'", id)
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
	cur, err := db.C(buildsCollection).Find(db.Context(), getOldBuildQuery(), options.Find().SetLimit(int64(limit)).SetMaxTime(2*AmboyInterval))
	if err != nil {
		return nil, errors.Wrap(err, "finding old builds")
	}

	var builds []LogKeeperBuild
	if err := cur.All(db.Context(), &builds); err != nil {
		return nil, errors.Wrap(err, "decoding old builds")
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

		cur, err := db.C(buildsCollection).Find(db.Context(), getOldBuildQuery(), options.Find().SetMaxTime(5*time.Minute))
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

type CleanupStats struct {
	NumBuilds int `json:"num_builds"`
	NumTests  int `json:"num_tests"`
	NumLogs   int `json:"num_logs"`
}

func CleanupOldLogsAndTestsByBuild(id string) (CleanupStats, error) {
	var stats CleanupStats
	result, err := db.C(logsCollection).DeleteMany(db.Context(), bson.M{"build_id": id})
	if err != nil {
		return stats, errors.Wrap(err, "deleting logs from old builds")
	}
	stats.NumLogs += int(result.DeletedCount)

	result, err = db.C(testsCollection).DeleteMany(db.Context(), bson.M{"build_id": id})
	if err != nil {
		return stats, errors.Wrap(err, "deleting tests from old builds")
	}
	stats.NumTests += int(result.DeletedCount)

	result, err = db.C(buildsCollection).DeleteOne(db.Context(), bson.M{"_id": id})
	if err != nil {
		return stats, errors.Wrap(err, "deleting build record")
	}
	stats.NumBuilds += int(result.DeletedCount)

	return stats, nil
}
