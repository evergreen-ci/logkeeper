package logkeeper

import (
	"context"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestFindGlobalLogsDuringTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initDB(ctx, t)
	clearCollections(ctx, t, buildsCollection, testsCollection, logsCollection)

	assert := assert.New(t)
	lk := New(Options{
		URL:            "http://localhost:8080",
		MaxRequestSize: 1024 * 1024 * 32,
	})

	buildStart := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)

	b := LogKeeperBuild{
		Id:      "b",
		Started: buildStart,
	}
	_, err := db.C("builds").InsertOne(ctx, b)
	assert.NoError(err)

	t0 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: b.Id,
		Started: buildStart,
	}
	_, err = db.C("tests").InsertOne(ctx, t0)
	assert.NoError(err)

	t1 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: b.Id,
		Started: buildStart.Add(10 * time.Second),
	}
	_, err = db.C("tests").InsertOne(ctx, t1)
	assert.NoError(err)

	globalLogTime := t0.Started.Add(5 * time.Second)
	globalLog := Log{
		BuildId: b.Id,
		TestId:  nil,
		Seq:     3,
		Started: &globalLogTime,
		Lines: []LogLine{
			*NewLogLine([]interface{}{float64(globalLogTime.Unix()), "during t0"}),
			*NewLogLine([]interface{}{float64(globalLogTime.Add(10 * time.Second).Unix()), "during t1"}),
		},
	}
	_, err = db.C("logs").InsertOne(ctx, globalLog)
	assert.NoError(err)

	t0Log := Log{
		BuildId: b.Id,
		TestId:  &t0.Id,
		Seq:     2,
		Started: &t0.Started,
		Lines: []LogLine{
			*NewLogLine([]interface{}{float64(t0.Started.Unix()), "t0 - line1"}),
			*NewLogLine([]interface{}{float64(t0.Started.Add(10 * time.Second).Unix()), "t0 - line2"}),
		},
	}
	_, err = db.C("logs").InsertOne(ctx, t0Log)
	assert.NoError(err)

	t1Log := Log{
		BuildId: b.Id,
		TestId:  &t1.Id,
		Seq:     1,
		Started: &t1.Started,
		Lines: []LogLine{
			*NewLogLine([]interface{}{float64(t1.Started.Unix()), "t1 - line1"}),
			*NewLogLine([]interface{}{float64(t1.Started.Add(10 * time.Second).Unix()), "t1 - line2"}),
		},
	}
	_, err = db.C("logs").InsertOne(ctx, t1Log)
	assert.NoError(err)

	t.Run("BuildStartedAfterTest", func(t *testing.T) {
		// global logs during a test should be returned as part of the test, even
		// if the build itself started after the test
		logChan, err := lk.findGlobalLogsDuringTest(ctx, &b, &t0)
		assert.NoError(err)
		count := 0
		for logLine := range logChan {
			count++
			assert.Equal("during t0", logLine.Data)
		}
		assert.Equal(1, count)
	})

	t.Run("LogsBeforeTestStarts", func(t *testing.T) {
		// global logs that start before the first test starts are included in the test's global logs
		logChan, err := lk.findGlobalLogsDuringTest(ctx, &b, &t1)
		assert.NoError(err)
		count := 0
		for logLine := range logChan {
			count++
			assert.Contains("during t1", logLine.Data)
		}
		assert.Equal(1, count)
	})
}
