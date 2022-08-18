package units

import (
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func TestCleanupOldLogsAndTestsByBuild(t *testing.T) {
	require.NoError(t, testutil.InitDB())

	assert := assert.New(t)
	db, closer := db.DB()
	defer closer()

	ids := insertBuilds(t)
	insertTests(t, ids)
	insertLogs(t, ids)

	count, _ := db.C(model.TestsCollection).Find(bson.M{}).Count()
	assert.Equal(4, count)

	count, _ = db.C(model.LogsCollection).Find(bson.M{}).Count()
	assert.Equal(4, count)

	numDeleted, err := cleanupOldLogsAndTestsByBuild(ids[0])
	assert.NoError(err)
	assert.Equal(4, numDeleted)

	count, _ = db.C(model.TestsCollection).Find(bson.M{}).Count()
	assert.Equal(3, count)

	count, _ = db.C(model.LogsCollection).Find(bson.M{}).Count()
	assert.Equal(2, count)
}

func TestNoErrorWithNoLogsOrTests(t *testing.T) {
	require.NoError(t, testutil.InitDB())

	assert := assert.New(t)
	db, closer := db.DB()
	defer closer()
	_, err := db.C(model.TestsCollection).RemoveAll(bson.M{})
	require.NoError(t, err)

	test := model.Test{
		Id:      model.NewTestID(time.Time{}),
		BuildId: "incompletebuild",
		Started: time.Now(),
	}
	build := model.Build{Id: "incompletebuild"}
	assert.NoError(db.C(model.BuildsCollection).Insert(build))
	assert.NoError(db.C(model.TestsCollection).Insert(test))
	count, err := cleanupOldLogsAndTestsByBuild(test.BuildId)
	assert.NoError(err)
	assert.Equal(2, count)

	log := model.Log{BuildId: "incompletebuild"}
	assert.NoError(db.C(model.BuildsCollection).Insert(build))
	assert.NoError(db.C(model.LogsCollection).Insert(log))
	count, err = cleanupOldLogsAndTestsByBuild(log.BuildId)
	assert.NoError(err)
	assert.Equal(2, count)
}

func insertBuilds(t *testing.T) []string {
	assert := assert.New(t)
	db, closer := db.DB()
	defer closer()
	_, err := db.C(model.BuildsCollection).RemoveAll(bson.M{})
	require.NoError(t, err)

	now := time.Now()
	info := model.BuildInfo{TaskID: bson.NewObjectId().Hex()}
	oldBuild1 := model.Build{
		Id:      "one",
		Started: time.Date(2016, time.January, 15, 0, 0, 0, 0, time.Local),
		Info:    info,
	}
	oldBuild2 := model.Build{
		Id:      "two",
		Started: time.Date(2016, time.February, 15, 0, 0, 0, 0, time.Local),
		Info:    info,
	}
	edgeBuild := model.Build{
		Id:      "three",
		Started: now.Add(-model.DeletePassedTestCutoff + time.Minute),
		Failed:  false,
		Info:    info,
	}
	newBuild := model.Build{
		Id:      "four",
		Started: now,
		Info:    info,
	}
	assert.NoError(db.C(model.BuildsCollection).Insert(oldBuild1, oldBuild2, edgeBuild, newBuild))
	return []string{oldBuild1.Id, oldBuild2.Id, edgeBuild.Id, newBuild.Id}
}

func insertTests(t *testing.T, ids []string) {
	assert := assert.New(t)
	db, closer := db.DB()
	defer closer()
	_, err := db.C(model.TestsCollection).RemoveAll(bson.M{})
	require.NoError(t, err)

	test1 := model.Test{
		Id:      model.NewTestID(time.Time{}),
		BuildId: ids[0],
	}
	test2 := model.Test{
		Id:      model.NewTestID(time.Time{}),
		BuildId: ids[1],
	}
	test3 := model.Test{
		Id:      model.NewTestID(time.Time{}),
		BuildId: ids[2],
	}
	test4 := model.Test{
		Id:      model.NewTestID(time.Time{}),
		BuildId: ids[3],
	}
	assert.NoError(db.C(model.TestsCollection).Insert(test1, test2, test3, test4))
}

func insertLogs(t *testing.T, ids []string) {
	assert := assert.New(t)
	db, closer := db.DB()
	defer closer()
	_, err := db.C(model.LogsCollection).RemoveAll(bson.M{})
	require.NoError(t, err)

	log1 := model.Log{BuildId: ids[0]}
	log2 := model.Log{BuildId: ids[0]}
	log3 := model.Log{BuildId: ids[1]}
	newId := bson.NewObjectId().Hex()
	log4 := model.Log{BuildId: newId}
	assert.NoError(db.C(model.LogsCollection).Insert(log1, log2, log3, log4))
}
