package model

import (
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncrementTestSequence(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	require.NoError(t, testutil.ClearCollections(TestsCollection))

	testID := NewTestID(time.Time{})
	test := &Test{Id: testID, Seq: 1}
	require.NoError(t, test.Insert())

	assert.NoError(t, test.IncrementSequence(1))
	assert.Equal(t, 2, test.Seq)

	test, err := FindTestByID(string(testID))
	assert.NoError(t, err)
	assert.Equal(t, test.Seq, 2)
}

func TestFindTestsForBuild(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	require.NoError(t, testutil.ClearCollections(TestsCollection))

	require.NoError(t, (&Test{Id: NewTestID(time.Time{}), Name: "t0", BuildId: "b0", Started: time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)}).Insert())
	require.NoError(t, (&Test{Id: NewTestID(time.Time{}), Name: "t1", BuildId: "b0", Started: time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC)}).Insert())
	require.NoError(t, (&Test{Id: NewTestID(time.Time{}), BuildId: "b1"}).Insert())

	tests, err := FindTestsForBuild("b0")
	assert.NoError(t, err)
	require.Len(t, tests, 2)
	assert.Equal(t, tests[0].Name, "t0")
	assert.Equal(t, tests[1].Name, "t1")
}

func TestRemoveTestsForBuild(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	require.NoError(t, testutil.ClearCollections(TestsCollection))

	require.NoError(t, (&Test{Id: NewTestID(time.Time{}), BuildId: "b0"}).Insert())
	require.NoError(t, (&Test{Id: NewTestID(time.Time{}), BuildId: "b0"}).Insert())
	require.NoError(t, (&Test{Id: NewTestID(time.Time{}), BuildId: "b1"}).Insert())

	count, err := RemoveTestsForBuild("b0")
	assert.NoError(t, err)
	assert.Equal(t, count, 2)
}

func TestFindNext(t *testing.T) {
	require.NoError(t, testutil.InitDB())
	require.NoError(t, testutil.ClearCollections(TestsCollection))

	t0 := Test{Id: NewTestID(time.Time{}), Name: "t0", BuildId: "b0", Started: time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)}
	t1 := Test{Id: NewTestID(time.Time{}), Name: "t1", BuildId: "b0", Started: time.Date(2009, time.November, 10, 23, 1, 0, 0, time.UTC)}
	require.NoError(t, t0.Insert())
	require.NoError(t, t1.Insert())

	next, err := t0.findNext()
	assert.NoError(t, err)
	assert.Equal(t, t1.Name, next.Name)
}

func TestGetExecutionWindow(t *testing.T) {
	require.NoError(t, testutil.InitDB())

	t.Run("NoLaterTest", func(t *testing.T) {
		require.NoError(t, testutil.ClearCollections(TestsCollection))

		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		t0 := Test{Id: NewTestID(startTime), Name: "t0", BuildId: "b0", Started: startTime}
		assert.NoError(t, t0.Insert())
		minTime, maxTime, err := t0.GetExecutionWindow()
		assert.NoError(t, err)
		assert.True(t, t0.Started.Equal(minTime))
		assert.Nil(t, maxTime)
	})

	t.Run("LaterTest", func(t *testing.T) {
		require.NoError(t, testutil.ClearCollections(TestsCollection))

		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		t0 := Test{Id: NewTestID(startTime), Name: "t0", BuildId: "b0", Started: time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)}
		assert.NoError(t, t0.Insert())
		t1 := Test{Id: NewTestID(startTime.Add(time.Hour)), Name: "t1", BuildId: "b0", Started: startTime.Add(time.Hour)}
		assert.NoError(t, t1.Insert())
		minTime, maxTime, err := t0.GetExecutionWindow()
		assert.NoError(t, err)
		assert.True(t, t0.Started.Equal(minTime))
		require.NotNil(t, maxTime)
		assert.True(t, t1.Started.Equal(*maxTime))
	})
}

func TestNewTestID(t *testing.T) {
	assert.True(t, strings.HasPrefix(string(NewTestID(time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC))), "1174efedab186000"))
}

func TestTestID(t *testing.T) {
	require.NoError(t, testutil.InitDB())

	t.Run("Timestamp", func(t *testing.T) {
		startTime := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
		newID := NewTestID(startTime)
		assert.True(t, startTime.Equal(newID.Timestamp()))
	})

	t.Run("SetBSON", func(t *testing.T) {
		require.NoError(t, testutil.ClearCollections(TestsCollection))

	})
}
