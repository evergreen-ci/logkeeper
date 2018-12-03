package logkeeper

import (
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"gopkg.in/mgo.v2/bson"
)

func insertTests(t *testing.T) []interface{} {
	assert := assert.New(t)
	db := db.GetDatabase()
	_, err := db.C("tests").RemoveAll(bson.M{})
	assert.NoError(err)

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
	db := db.GetDatabase()
	_, err := db.C("logs").RemoveAll(bson.M{})
	assert.NoError(err)

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

	tests, err := GetOldTests()
	assert.NoError(err)
	assert.Len(tests, 2)
}

func TestCleanupOldLogsTestsAndBuilds(t *testing.T) {
	assert := assert.New(t)
	db := db.GetDatabase()
	ids := insertTests(t)
	insertLogs(t, ids)

	count, _ := db.C("tests").Find(bson.M{}).Count()
	assert.Equal(4, count)

	assert.NoError(CleanupOldLogsByBuild(ids[0]))
	count, _ = db.C("tests").Find(bson.M{}).Count()
	assert.Equal(3, count)

	count, _ = db.C("logs").Find(bson.M{}).Count()
	assert.Equal(2, count)
}

func TestNoErrorWithBadTest(t *testing.T) {
	assert := assert.New(t)
	db := db.GetDatabase()
	_, err := db.C("tests").RemoveAll(bson.M{})
	assert.NoError(err)
	test := Test{
		Id:      bson.NewObjectId(),
		BuildId: "lol",
		Started: time.Now(),
	}
	assert.NoError(db.C("tests").Insert(test))
	assert.NoError(CleanupOldLogsByBuild(test.BuildId))
}

func TestUpdateFailedTest(t *testing.T) {
	assert := assert.New(t)
	ids := insertTests(t)
	insertLogs(t, ids)

	tests, err := GetOldTests()
	assert.NoError(err)
	assert.Len(tests, 2)

	assert.NoError(UpdateFailedTestsByBuildID(ids[1]))
	tests, err = GetOldTests()
	assert.NoError(err)
	assert.Len(tests, 1)
}
