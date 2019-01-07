package logkeeper

import (
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func insertTests(t *testing.T) []interface{} {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C("tests").RemoveAll(bson.M{})
	require.NoError(t, err)

	now := time.Now()
	oldTest1 := Test{
		Id:      bson.NewObjectId(),
		BuildId: "one",
		Started: time.Date(2016, time.January, 15, 0, 0, 0, 0, time.Local),
	}
	assert.NoError(db.C("tests").Insert(oldTest1))
	oldTest2 := Test{
		Id:      bson.NewObjectId(),
		BuildId: "two",
		Started: time.Date(2016, time.February, 15, 0, 0, 0, 0, time.Local),
	}
	assert.NoError(db.C("tests").Insert(oldTest2))
	edgeTest := Test{
		Id:      bson.NewObjectId(),
		Started: now.Add(-deletePassedTestCutoff + time.Minute),
		BuildId: "three",
		Failed:  false,
	}
	assert.NoError(db.C("tests").Insert(edgeTest))
	newTest := Test{
		Id:      bson.NewObjectId(),
		BuildId: "four",
		Started: now,
	}
	assert.NoError(db.C("tests").Insert(newTest))
	return []interface{}{oldTest1.BuildId, oldTest2.BuildId, edgeTest.BuildId, newTest.BuildId}
}

func insertLogs(t *testing.T, ids []interface{}) {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C("logs").RemoveAll(bson.M{})
	require.NoError(t, err)

	log1 := Log{BuildId: &ids[0]}
	log2 := Log{BuildId: &ids[0]}
	log3 := Log{BuildId: &ids[1]}
	newId := bson.NewObjectId()
	log4 := Log{BuildId: &newId}
	assert.NoError(db.C("logs").Insert(log1, log2, log3, log4))
}

func TestGetOldTests(t *testing.T) {
	assert := assert.New(t)
	ids := insertTests(t)
	insertLogs(t, ids)

	tests, err := GetOldTests(CleanupBatchSize)
	assert.NoError(err)
	assert.Len(tests, 2)
}

func TestCleanupOldLogsTestsAndBuilds(t *testing.T) {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()

	ids := insertTests(t)
	insertLogs(t, ids)

	count, _ := db.C("tests").Find(bson.M{}).Count()
	assert.Equal(4, count)

	_, err := CleanupOldLogsByBuild(ids[0])
	assert.NoError(err)
	count, _ = db.C("tests").Find(bson.M{}).Count()
	assert.Equal(3, count)

	count, _ = db.C("logs").Find(bson.M{}).Count()
	assert.Equal(2, count)
}

func TestNoErrorWithBadTest(t *testing.T) {
	assert := assert.New(t)
	db, closer := db.GetDatabase()
	defer closer()
	_, err := db.C("tests").RemoveAll(bson.M{})
	require.NoError(t, err)

	test := Test{
		Id:      bson.NewObjectId(),
		BuildId: "lol",
		Started: time.Now(),
	}
	assert.NoError(db.C("tests").Insert(test))
	_, err = CleanupOldLogsByBuild(test.BuildId)
	assert.NoError(err)
}

func TestUpdateFailedTest(t *testing.T) {
	assert := assert.New(t)
	ids := insertTests(t)
	insertLogs(t, ids)

	tests, err := GetOldTests(CleanupBatchSize)
	assert.NoError(err)
	assert.Len(tests, 2)

	_, err = UpdateFailedTestsByBuildID(ids[1])
	assert.NoError(err)
	tests, err = GetOldTests(CleanupBatchSize)
	assert.NoError(err)
	assert.Len(tests, 1)
}
