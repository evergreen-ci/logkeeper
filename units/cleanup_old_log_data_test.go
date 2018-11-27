package units

import (
	"context"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper"
	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func insertTests(t *testing.T, db *mgo.Database) []logkeeper.Test {
	now := time.Now()
	assert := assert.New(t)
	_, err := db.C("tests").RemoveAll(bson.M{})
	assert.NoError(err)
	oldTestSuccess := logkeeper.Test{
		Id:      bson.NewObjectId(),
		BuildId: "123",
		Started: time.Date(2016, time.January, 15, 0, 0, 0, 0, time.Local),
		Failed:  false,
	}
	assert.NoError(db.C("tests").Insert(oldTestSuccess))
	oldTestFail := logkeeper.Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: time.Date(2016, time.February, 15, 0, 0, 0, 0, time.Local),
		Failed:  true,
	}
	assert.NoError(db.C("tests").Insert(oldTestFail))
	newTest := logkeeper.Test{
		Id:      bson.NewObjectId(),
		BuildId: "234",
		Started: now,
	}
	assert.NoError(db.C("tests").Insert(newTest))

	return []logkeeper.Test{oldTestSuccess, oldTestFail, newTest}
}

func insertBuilds(t *testing.T, db *mgo.Database) {
	assert := assert.New(t)
	_, err := db.C("builds").RemoveAll(bson.M{})
	assert.NoError(err)
	build1 := logkeeper.LogKeeperBuild{Id: "123"}
	build2 := logkeeper.LogKeeperBuild{Id: "234"}
	build3 := logkeeper.LogKeeperBuild{Id: "notests"}
	build4 := logkeeper.LogKeeperBuild{Id: "alsonotests"}
	assert.NoError(db.C("builds").Insert(build1, build2, build3, build4))
}

func insertLogs(t *testing.T, db *mgo.Database, tests []logkeeper.Test) {
	assert := assert.New(t)
	_, err := db.C("logs").RemoveAll(bson.M{})
	assert.NoError(err)

	id1 := tests[0].Id
	id2 := tests[1].Id
	log1 := logkeeper.Log{TestId: &id1}
	log2 := logkeeper.Log{TestId: &id1}
	log3 := logkeeper.Log{TestId: &id2}
	newId := bson.NewObjectId()
	log4 := logkeeper.Log{TestId: &newId}
	assert.NoError(db.C("logs").Insert(log1, log2, log3, log4))
}
func TestCleanupOldLogsWithJob(t *testing.T) {
	assert := assert.New(t)
	connInfo := mgo.DialInfo{
		Addrs:   []string{"localhost"},
		Timeout: 5 * time.Second,
	}
	session, err := mgo.DialWithInfo(&connInfo)
	require.NoError(t, err)
	require.NoError(t, db.SetSession(session))
	db := db.GetDatabase()

	tests := insertTests(t, db)
	insertBuilds(t, db)
	insertLogs(t, db, tests)

	job := NewCleanupOldLogDataJob(time.Now().String())
	job.Run(context.Background())
	count, _ := db.C("tests").Find(bson.M{}).Count()
	assert.Equal(2, count)

	count, _ = db.C("logs").Find(bson.M{}).Count()
	assert.Equal(2, count)

	count, _ = db.C("builds").Find(bson.M{}).Count()
	assert.Equal(1, count)
}
