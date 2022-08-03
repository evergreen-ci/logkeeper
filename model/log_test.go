package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/mgo.v2/bson"
)

func TestRemoveLogsForBuild(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	t.Run("NoLogs", func(t *testing.T) {
		require.NoError(t, testutil.ClearCollections(LogsCollection))
		count, err := RemoveLogsForBuild("")
		assert.NoError(t, err)
		assert.Zero(t, count)
	})

	t.Run("MixOfBuilds", func(t *testing.T) {
		require.NoError(t, testutil.ClearCollections(LogsCollection))
		require.NoError(t, (&Log{BuildId: "b0"}).Insert())
		require.NoError(t, (&Log{BuildId: "b1"}).Insert())
		count, err := RemoveLogsForBuild("b0")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestFindLogsInWindow(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	require.NoError(t, testutil.ClearCollections(LogsCollection))

	earliestTime := time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC)
	latestTime := time.Date(2009, time.November, 10, 23, 2, 0, 0, time.UTC)
	require.NoError(t, (&Log{Seq: 0, Lines: []LogLine{
		{Time: earliestTime.Add(-time.Hour), Msg: "line0"},
		{Time: earliestTime, Msg: "line1"},
	}}).Insert())
	require.NoError(t, (&Log{Seq: 1, Lines: []LogLine{
		{Time: latestTime, Msg: "line2"},
		{Time: latestTime.Add(time.Hour), Msg: "line3"},
	}}).Insert())

	logChan := findLogsInWindow(bson.M{}, []string{"seq"}, &earliestTime, &latestTime)
	var lines []*LogLineItem
	require.Eventually(t, func() bool {
		select {
		case line := <-logChan:
			lines = append(lines, line)
		default:
		}

		return len(lines) == 2
	}, time.Second, 10*time.Millisecond)

	assert.Equal(t, "line1", lines[0].Data)
	assert.Equal(t, "line2", lines[1].Data)
}

func TestGroupLines(t *testing.T) {
	makeLines := func(lineSize, numLines int) []LogLine {
		builder := strings.Builder{}
		for i := 0; i < lineSize; i++ {
			builder.WriteString("a")
		}
		msg := builder.String()
		var lines []LogLine
		for i := 0; i < numLines; i++ {
			lines = append(lines, LogLine{Msg: msg})
		}

		return lines
	}

	t.Run("UnderThreshold", func(t *testing.T) {
		chunks, err := GroupLines(makeLines(5, 2), 10)
		assert.NoError(t, err)
		require.Len(t, chunks, 1)
		assert.Len(t, chunks[0], 2)
	})

	t.Run("SingleLineOverThreshold", func(t *testing.T) {
		_, err := GroupLines(makeLines(11, 1), 10)
		assert.Error(t, err)
	})

	t.Run("MultipleGroups", func(t *testing.T) {
		chunks, err := GroupLines(makeLines(1, 20), 10)
		assert.NoError(t, err)
		require.Len(t, chunks, 2)
		assert.Len(t, chunks[0], 10)
		assert.Len(t, chunks[1], 10)
	})
}

func TestUnmarshalJSON(t *testing.T) {
	logLineJSON := "[1257894000, \"message\"]"
	line := LogLine{}
	assert.NoError(t, json.Unmarshal([]byte(logLineJSON), &line))
	assert.True(t, line.Time.Equal(time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)))
	assert.Equal(t, "message", line.Msg)
}

func TestMergeLogChannels(t *testing.T) {
	logger0 := make(chan *LogLineItem, 1)
	logger1 := make(chan *LogLineItem, 1)

	logger0 <- &LogLineItem{Data: "m0", Timestamp: time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)}
	close(logger0)
	logger1 <- &LogLineItem{Data: "m1", Timestamp: time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC)}
	close(logger1)

	outChan := MergeLogChannels(logger0, logger1)
	var items []*LogLineItem
	assert.Eventually(t, func() bool {
		select {
		case item := <-outChan:
			items = append(items, item)
		default:
		}

		return len(items) == 2
	}, time.Second, 10*time.Millisecond)

	assert.Equal(t, "m0", items[0].Data)
	assert.Equal(t, "m1", items[1].Data)
}

func TestFindGlobalLogsDuringTest(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	require.NoError(t, testutil.ClearCollections(LogsCollection))

	t0Start := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)

	buildID := "b0"
	t0 := Test{
		Id:      bson.NewObjectId(),
		BuildId: buildID,
		Started: t0Start,
	}
	assert.NoError(t, t0.Insert())
	t1 := Test{
		Id:      bson.NewObjectId(),
		BuildId: buildID,
		Started: t0Start.Add(10 * time.Second),
	}
	assert.NoError(t, t1.Insert())

	globalLogTime := t0Start.Add(5 * time.Second)
	globalLog := Log{
		BuildId: buildID,
		TestId:  nil,
		Seq:     3,
		Started: &globalLogTime,
		Lines: []LogLine{
			{Time: t0Start.Add(5 * time.Second), Msg: "build 0-0"},
			{Time: t0Start.Add(15 * time.Second), Msg: "build 0-1"},
		},
	}
	assert.NoError(t, globalLog.Insert())
	testLog0 := Log{
		BuildId: buildID,
		TestId:  &t0.Id,
		Seq:     1,
		Started: &t0.Started,
		Lines: []LogLine{
			{Time: t0.Started, Msg: "test 0-0"},
			{Time: t0.Started.Add(10 * time.Second), Msg: "test 0-1"},
		},
	}
	assert.NoError(t, testLog0.Insert())
	testLog1 := Log{
		BuildId: buildID,
		TestId:  &t1.Id,
		Seq:     2,
		Started: &t1.Started,
		Lines: []LogLine{
			{Time: t1.Started, Msg: "test 1-0"},
			{Time: t1.Started.Add(10 * time.Second), Msg: "test 1-1"},
		},
	}
	assert.NoError(t, testLog1.Insert())

	// build logs from during a test should be returned as part of the test, even
	// if the build itself started after the test
	logChan, err := findGlobalLogsDuringTest(&t0)
	assert.NoError(t, err)
	count := 0
	for logLine := range logChan {
		count++
		assert.Equal(t, "build 0-0", logLine.Data)
	}
	assert.Equal(t, 1, count)

	// test that we can correctly find global logs during a test that start before the test starts
	logChan, err = findGlobalLogsDuringTest(&t1)
	assert.NoError(t, err)
	count = 0
	for logLine := range logChan {
		count++
		assert.Equal(t, "build 0-1", logLine.Data)
	}
	assert.Equal(t, 1, count)
}
