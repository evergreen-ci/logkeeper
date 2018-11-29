package logkeeper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func insertTests(t *testing.T, db *mgo.Database) []bson.ObjectId {
	now := time.Now()
	assert := assert.New(t)
	_, err := db.C("tests").RemoveAll(bson.M{})
	assert.NoError(err)
	oldTestSuccess := Test{
		Id:      bson.NewObjectId(),
		BuildId: "123",
		Started: time.Date(2016, time.January, 15, 0, 0, 0, 0, time.Local),
		Failed:  false,
	}
	assert.NoError(db.C("tests").Insert(oldTestSuccess))
	oldTestFail := Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: time.Date(2016, time.February, 15, 0, 0, 0, 0, time.Local),
		Failed:  true,
	}
	assert.NoError(db.C("tests").Insert(oldTestFail))
	edgeTestSuccess := Test{
		Id:      bson.NewObjectId(),
		BuildId: "123",
		Started: now.Add(-deletePassedTestCutoff + time.Minute),
		Failed:  false,
	}
	assert.NoError(db.C("tests").Insert(edgeTestSuccess))
	edgeTestFailed := Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: now.Add(-deletePassedTestCutoff + time.Minute),
		Failed:  true,
	}
	assert.NoError(db.C("tests").Insert(edgeTestFailed))
	newTest := Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: now,
	}
	assert.NoError(db.C("tests").Insert(newTest))

	return []bson.ObjectId{oldTestSuccess.Id, oldTestFail.Id, edgeTestSuccess.Id, edgeTestFailed.Id, newTest.Id}
}

func insertLogs(t *testing.T, db *mgo.Database, ids []bson.ObjectId) {
	assert := assert.New(t)
	_, err := db.C("logs").RemoveAll(bson.M{})
	assert.NoError(err)

	log1 := Log{TestId: &ids[0]}
	log2 := Log{TestId: &ids[0]}
	log3 := Log{TestId: &ids[1]}
	newId := bson.NewObjectId()
	log4 := Log{TestId: &newId}
	assert.NoError(db.C("logs").Insert(log1, log2, log3, log4))
}

func TestGetOldTests(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	ids := insertTests(t, db)
	insertLogs(t, db, ids)

	tests, err := GetOldTests(db, time.Now())
	assert.NoError(err)
	assert.Len(*tests, 2)
}

func TestCleanupOldLogsTestsAndBuilds(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	ids := insertTests(t, db)
	insertLogs(t, db, ids)
	assert.Len(ids, 5)

	assert.NoError(CleanupOldLogsByTest(db, ids[0]))
	count, _ := db.C("tests").Find(bson.M{}).Count()
	assert.Equal(4, count)

	count, _ = db.C("logs").Find(bson.M{}).Count()
	assert.Equal(2, count)
}

func TestNoErrorWithBadTest(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	_, err := db.C("tests").RemoveAll(bson.M{})
	assert.NoError(err)
	test := Test{
		Id:      bson.NewObjectId(),
		Started: time.Now(),
	}
	assert.NoError(db.C("tests").Insert(test))
	assert.NoError(CleanupOldLogsByTest(db, test.Id))
}
