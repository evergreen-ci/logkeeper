package logkeeper

import (
	"context"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo/bson"
	"go.mongodb.org/mongo-driver/mongo/bson/primitive"
)

func insertBuilds(t *testing.T) []interface{} {
	ctx := context.Background()
	assert := assert.New(t)
	_, err := db.C(buildsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)

	info := make(map[string]interface{})
	info["task_id"] = primitive.NewObjectID().Hex()
	now := time.Now()
	oldBuild1 := LogKeeperBuild{
		Id:      "one",
		Started: time.Date(2016, time.January, 15, 0, 0, 0, 0, time.Local),
		Info:    info,
	}
	oldBuild2 := LogKeeperBuild{
		Id:      "two",
		Started: time.Date(2016, time.February, 15, 0, 0, 0, 0, time.Local),
		Info:    info,
	}
	edgeBuild := LogKeeperBuild{
		Id:      "three",
		Started: now.Add(-deletePassedTestCutoff + time.Minute),
		Failed:  false,
		Info:    info,
	}
	newBuild := LogKeeperBuild{
		Id:      "four",
		Started: now,
		Info:    info,
	}
	_, err = db.C(buildsName).InsertMany(ctx, []interface{}{oldBuild1, oldBuild2, edgeBuild, newBuild})
	assert.NoError(err)
	return []interface{}{oldBuild1.Id, oldBuild2.Id, edgeBuild.Id, newBuild.Id}
}

func insertTests(t *testing.T, ids []interface{}) {
	ctx := context.Background()
	assert := assert.New(t)

	_, err := db.C(testsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)

	test1 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: &ids[0],
	}
	test2 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: &ids[1],
	}
	test3 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: &ids[2],
	}
	test4 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: &ids[3],
	}
	_, err = db.C(testsName).InsertMany(ctx, []interface{}{test1, test2, test3, test4})
	assert.NoError(err)
}

func insertLogs(t *testing.T, ids []interface{}) {
	ctx := context.Background()
	assert := assert.New(t)
	_, err := db.C(logsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)

	log1 := Log{BuildId: &ids[0]}
	log2 := Log{BuildId: &ids[0]}
	log3 := Log{BuildId: &ids[1]}
	newId := primitive.NewObjectID()
	log4 := Log{BuildId: &newId}
	_, err = db.C(logsName).InsertMany(ctx, []interface{}{log1, log2, log3, log4})
	assert.NoError(err)
}

func TestGetOldTests(t *testing.T) {
	assert := assert.New(t)
	ids := insertBuilds(t)
	insertTests(t, ids)
	insertLogs(t, ids)

	builds, err := GetOldBuilds(CleanupBatchSize)
	assert.NoError(err)
	assert.Len(builds, 2)
}

func TestCleanupOldLogsAndTestsByBuild(t *testing.T) {
	ctx := context.Background()
	assert := assert.New(t)

	ids := insertBuilds(t)
	insertTests(t, ids)
	insertLogs(t, ids)

	count, _ := db.C(testsName).CountDocuments(ctx, bson.M{})
	assert.Equal(4, count)

	count, _ = db.C(logsName).CountDocuments(ctx, bson.M{})
	assert.Equal(4, count)

	numDeleted, err := CleanupOldLogsAndTestsByBuild(ids[0])
	assert.NoError(err)
	assert.Equal(4, numDeleted)

	count, _ = db.C(testsName).CountDocuments(ctx, bson.M{})
	assert.Equal(3, count)

	count, _ = db.C(logsName).CountDocuments(ctx, bson.M{})
	assert.Equal(2, count)
}

func TestNoErrorWithNoLogsOrTests(t *testing.T) {
	ctx := context.Background()
	assert := assert.New(t)

	_, err := db.C(testsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)

	test := Test{
		Id:      primitive.NewObjectID(),
		BuildId: "incompletebuild",
		Started: time.Now(),
	}
	build := LogKeeperBuild{Id: "incompletebuild"}
	_, err = db.C(buildsName).InsertOne(ctx, build)
	assert.NoError(err)
	_, err = db.C(testsName).InsertOne(ctx, test)
	assert.NoError(err)
	count, err := CleanupOldLogsAndTestsByBuild(test.BuildId)
	assert.NoError(err)
	assert.Equal(2, count)

	log := Log{BuildId: "incompletebuild"}
	_, err = db.C(buildsName).InsertOne(ctx, build)
	assert.NoError(err)
	_, err = db.C(logsName).InsertOne(ctx, log)
	assert.NoError(err)
	count, err = CleanupOldLogsAndTestsByBuild(log.BuildId)
	assert.NoError(err)
	assert.Equal(2, count)
}

func TestUpdateFailedTest(t *testing.T) {
	assert := assert.New(t)
	ids := insertBuilds(t)
	insertTests(t, ids)
	insertLogs(t, ids)

	builds, err := GetOldBuilds(CleanupBatchSize)
	assert.NoError(err)
	assert.Len(builds, 2)

	err = UpdateFailedBuild(ids[1])
	assert.NoError(err)
	builds, err = GetOldBuilds(CleanupBatchSize)
	assert.NoError(err)
	assert.Len(builds, 1)
}
