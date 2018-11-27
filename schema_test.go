package logkeeper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func insertTests(t *testing.T, db *mgo.Database) []Test {
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
		Started: now.Add(-deletePassedTestCutoff),
		Failed:  false,
	}
	assert.NoError(db.C("tests").Insert(edgeTestSuccess))
	edgeTestFailed := Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: now.Add(-deletePassedTestCutoff),
		Failed:  true,
	}
	assert.NoError(db.C("tests").Insert(edgeTestFailed))
	newTest := Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: now,
	}
	assert.NoError(db.C("tests").Insert(newTest))

	return []Test{oldTestSuccess, oldTestFail, edgeTestSuccess, edgeTestFailed, newTest}
}

func insertBuilds(t *testing.T, db *mgo.Database) {
	assert := assert.New(t)
	_, err := db.C("builds").RemoveAll(bson.M{})
	assert.NoError(err)
	build1 := LogKeeperBuild{Id: "123"}
	build2 := LogKeeperBuild{Id: "234"}
	build3 := LogKeeperBuild{Id: "notests"}
	build4 := LogKeeperBuild{Id: "alsonotests"}
	assert.NoError(db.C("builds").Insert(build1, build2, build3, build4))
}

func insertLogs(t *testing.T, db *mgo.Database, tests []Test) {
	assert := assert.New(t)
	_, err := db.C("logs").RemoveAll(bson.M{})
	assert.NoError(err)

	id1 := tests[0].Id
	id2 := tests[1].Id
	log1 := Log{TestId: &id1}
	log2 := Log{TestId: &id1}
	log3 := Log{TestId: &id2}
	newId := bson.NewObjectId()
	log4 := Log{TestId: &newId}
	assert.NoError(db.C("logs").Insert(log1, log2, log3, log4))
}

func TestFindAndDeleteTests(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	insertTests(t, db)

	tests, err := findAndDeleteTests(db)
	assert.NoError(err)
	assert.Len(tests, 2)

	count, _ := db.C("tests").Find(bson.M{}).Count()
	assert.Equal(3, count)
}

func TestDeleteLogsByTests(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	tests := insertTests(t, db)
	insertLogs(t, db, tests)

	info, err := deleteLogsByTests(db, tests)
	assert.NoError(err)
	assert.Equal(3, info.Removed)
}

func TestDeleteBuildsWithoutTests(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	insertTests(t, db)
	insertBuilds(t, db)

	count, err := db.C("builds").Find(bson.M{}).Count()
	assert.NoError(err)
	assert.Equal(4, count)

	assert.NoError(deleteBuildsWithoutTests(db))

	count, err = db.C("builds").Find(bson.M{}).Count()
	assert.NoError(err)
	assert.Equal(2, count)
}

func TestCleanupOldLogsTestsAndBuilds(t *testing.T) {
	assert := assert.New(t)
	lk := makeTestLogkeeperApp(t)
	_, db := lk.getSession()
	tests := insertTests(t, db)
	insertBuilds(t, db)
	insertLogs(t, db, tests)

	assert.NoError(CleanupOldLogsTestsAndBuilds(db))
	count, _ := db.C("tests").Find(bson.M{}).Count()
	assert.Equal(3, count)

	count, _ = db.C("logs").Find(bson.M{}).Count()
	assert.Equal(2, count)

	count, _ = db.C("builds").Find(bson.M{}).Count()
	assert.Equal(1, count)
}

func TestNoErrorWithNoOldTests(t *testing.T) {
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
	assert.NoError(CleanupOldLogsTestsAndBuilds(db))
}
