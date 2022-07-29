package logkeeper

import (
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/evergreen-ci/logkeeper/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func makeTestLogkeeperApp(t *testing.T) *logKeeper {
	connInfo := mgo.DialInfo{
		Addrs:   []string{"localhost"},
		Timeout: 5 * time.Second,
	}
	session, err := mgo.DialWithInfo(&connInfo)
	require.NoError(t, err)
	require.NoError(t, env.SetSession(session))
	env.SetDBName("logkeeper_test")
	lk := New(Options{
		URL:            "http://localhost:8080",
		MaxRequestSize: 1024 * 1024 * 32,
	})

	return lk
}

func TestFindGlobalLogsDuringTest(t *testing.T) {
	assert := assert.New(t)
	now := time.Now()
	lk := makeTestLogkeeperApp(t)
	db, closer := db.DB()
	defer closer()

	b := LogKeeperBuild{
		Id:      "b",
		Started: now,
	}
	assert.NoError(db.C("builds").Insert(b))
	t1 := Test{
		Id:      bson.NewObjectId(),
		Started: now.Add(10 * time.Second),
	}
	assert.NoError(db.C("tests").Insert(t1))
	t2 := Test{
		Id:      bson.NewObjectId(),
		Started: now,
	}
	assert.NoError(db.C("tests").Insert(t2))

	globalLogTime := now.Add(5 * time.Second)
	globalLog := Log{
		BuildId: b.Id,
		TestId:  nil,
		Seq:     3,
		Started: &globalLogTime,
		Lines: []LogLine{
			*NewLogLine([]interface{}{float64(globalLogTime.Unix()), "build 1-1"}),
			*NewLogLine([]interface{}{float64(globalLogTime.Add(10 * time.Second).Unix()), "build 1-2"}),
		},
	}
	assert.NoError(db.C("logs").Insert(globalLog))
	testLog1 := Log{
		BuildId: b.Id,
		TestId:  &t1.Id,
		Seq:     1,
		Started: &t1.Started,
		Lines: []LogLine{
			*NewLogLine([]interface{}{float64(t1.Started.Unix()), "test 1-1"}),
			*NewLogLine([]interface{}{float64(t1.Started.Add(10 * time.Second).Unix()), "test 1-2"}),
		},
	}
	assert.NoError(db.C("logs").Insert(testLog1))
	testLog2 := Log{
		BuildId: b.Id,
		TestId:  &t2.Id,
		Seq:     2,
		Started: &t2.Started,
		Lines: []LogLine{
			*NewLogLine([]interface{}{float64(t2.Started.Unix()), "test 2-1"}),
			*NewLogLine([]interface{}{float64(t2.Started.Add(10 * time.Second).Unix()), "test 2-2"}),
		},
	}
	assert.NoError(db.C("logs").Insert(testLog2))

	// build logs that during a test should be returned as part of the test, even
	// if the build itself started after the test
	logChan, err := lk.findGlobalLogsDuringTest(&b, &t2)
	assert.NoError(err)
	count := 0
	for logLine := range logChan {
		count++
		assert.Contains(logLine.Data, "build 1")
	}
	assert.Equal(2, count)

	// test that we can correctly find global logs during a test that start before a test starts
	logChan, err = lk.findGlobalLogsDuringTest(&b, &t1)
	assert.NoError(err)
	count = 0
	for logLine := range logChan {
		count++
		assert.Contains(logLine.Data, "build 1-2")
	}
	assert.Equal(1, count)
}
