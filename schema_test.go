package logkeeper

import (
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func insertBuilds(t *testing.T) []interface{} {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C(buildsName).RemoveAll(bson.M{})
	require.NoError(t, err)

	info := make(map[string]interface{})
	info["task_id"] = bson.NewObjectId()
	now := time.Now()
	oldBuild1 := LogKeeperBuild{
		Id: "one",
		Started: time.Date(2016, time.January, 15, 0, 0, 0, 0, time.Local),
		Info: info,
	}
	oldBuild2 := LogKeeperBuild{
		Id: "two",
		Started: time.Date(2016, time.February, 15, 0, 0, 0, 0, time.Local),
		Info: info,
	}
	edgeBuild := LogKeeperBuild{
		Id: "three",
		Started: now.Add(-deletePassedTestCutoff + time.Minute),
		Failed: false,
		Info: info,
	}
	newBuild := LogKeeperBuild{
		Id: "four",
		Started: now,
		Info: info,
	}
	assert.NoError(db.C(buildsName).Insert(oldBuild1, oldBuild2, edgeBuild, newBuild))
	return []interface{}{oldBuild1.Id, oldBuild2.Id, edgeBuild.Id, newBuild.Id}
}

func insertTests(t *testing.T, ids []interface{}) {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C(testsName).RemoveAll(bson.M{})
	require.NoError(t, err)

	test1 := Test{
		Id:      bson.NewObjectId(),
		BuildId: &ids[0],
	}
	test2 := Test{
		Id:      bson.NewObjectId(),
		BuildId: &ids[1],
	}
	test3 := Test{
		Id:      bson.NewObjectId(),
		BuildId: &ids[2],
	}
	test4 := Test{
		Id:      bson.NewObjectId(),
		BuildId: &ids[3],
	}
	assert.NoError(db.C(testsName).Insert(test1, test2, test3, test4))
}

func insertLogs(t *testing.T, ids []interface{}) {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C(logsName).RemoveAll(bson.M{})
	require.NoError(t, err)

	log1 := Log{BuildId: &ids[0]}
	log2 := Log{BuildId: &ids[0]}
	log3 := Log{BuildId: &ids[1]}
	newId := bson.NewObjectId()
	log4 := Log{BuildId: &newId}
	assert.NoError(db.C(logsName).Insert(log1, log2, log3, log4))
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
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()

	ids := insertBuilds(t)
	insertTests(t, ids)
	insertLogs(t, ids)

	count, _ := db.C(testsName).Find(bson.M{}).Count()
	assert.Equal(4, count)

	count, _ = db.C(logsName).Find(bson.M{}).Count()
	assert.Equal(4, count)

	numDeleted, err := CleanupOldLogsAndTestsByBuild(ids[0])
	assert.NoError(err)
	assert.Equal(3, numDeleted)

	count, _ = db.C(testsName).Find(bson.M{}).Count()
	assert.Equal(3, count)

	count, _ = db.C(logsName).Find(bson.M{}).Count()
	assert.Equal(2, count)
}

func TestNoErrorWithNoLogsOrTests(t *testing.T) {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C(testsName).RemoveAll(bson.M{})
	require.NoError(t, err)

	test := Test{
		Id:      bson.NewObjectId(),
		BuildId: "testwithnolog",
		Started: time.Now(),
	}
	assert.NoError(db.C(testsName).Insert(test))
	count, err := CleanupOldLogsAndTestsByBuild(test.BuildId)
	assert.NoError(err)
	assert.Equal(1, count)

	log := Log{BuildId: "logwithnotest"}
	assert.NoError(db.C(logsName).Insert(log))
	count, err = CleanupOldLogsAndTestsByBuild(log.BuildId)
	assert.NoError(err)
	assert.Equal(1, count)
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
