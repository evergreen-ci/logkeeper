package logkeeper

import (
	"context"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func makeTestLogkeeperApp(t *testing.T) *logKeeper {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Fatal(err)
	}

	if err = client.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	db.SetClient(client)
	db.SetDBName("logkeeper_test")

	_, err = db.C(buildsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)
	_, err = db.C(testsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)
	_, err = db.C(logsName).DeleteMany(ctx, bson.M{})
	require.NoError(t, err)

	lk := New(Options{
		URL:            "http://localhost:8080",
		MaxRequestSize: 1024 * 1024 * 32,
	})

	return lk
}

func TestFindGlobalLogsDuringTest(t *testing.T) {
	ctx := context.Background()
	assert := assert.New(t)
	testStart := time.Now()
	lk := makeTestLogkeeperApp(t)

	b := LogKeeperBuild{
		Id:      "b",
		Started: testStart,
	}
	_, err := db.C("builds").InsertOne(ctx, b)
	assert.NoError(err)

	t1 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: b.Id,
		Started: testStart.Add(10 * time.Second),
	}
	_, err = db.C("tests").InsertOne(ctx, t1)
	assert.NoError(err)
	t2 := Test{
		Id:      primitive.NewObjectID(),
		BuildId: b.Id,
		Started: testStart,
	}
	_, err = db.C("tests").InsertOne(ctx, t2)
	assert.NoError(err)

	globalLogTime := testStart.Add(5 * time.Second)
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
	_, err = db.C("logs").InsertOne(ctx, globalLog)
	assert.NoError(err)
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
	_, err = db.C("logs").InsertOne(ctx, testLog1)
	assert.NoError(err)
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
	_, err = db.C("logs").InsertOne(ctx, testLog2)
	assert.NoError(err)

	// build logs during a test should be returned as part of the test, even
	// if the build itself started after the test
	logChan, err := lk.findGlobalLogsDuringTest(ctx, &b, &t2)
	assert.NoError(err)
	count := 0
	for logLine := range logChan {
		count++
		assert.Contains(logLine.Data, "build 1-1")
	}
	assert.Equal(1, count)

	// test that we can correctly find global logs during a test that start before a test starts
	logChan, err = lk.findGlobalLogsDuringTest(ctx, &b, &t1)
	assert.NoError(err)
	count = 0
	for logLine := range logChan {
		count++
		assert.Contains(logLine.Data, "build 1-2")
	}
	assert.Equal(1, count)
}
