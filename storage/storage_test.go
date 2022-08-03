package storage

import (
	"context"
	"os"
	"testing"

	"github.com/evergreen-ci/pail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTestStorage(t *testing.T, initDir string) Storage {
	err := os.RemoveAll("../_bucketdata")
	require.NoError(t, err)
	err = os.Mkdir("../_bucketdata", 0755)
	require.NoError(t, err)

	bucket, err := pail.NewLocalBucket(pail.LocalOptions{
		Path:   "../_bucketdata",
		Prefix: "",
	})
	require.NoError(t, err)

	err = bucket.Push(context.Background(), pail.SyncOptions{
		Local:  initDir,
		Remote: "/",
	})
	require.NoError(t, err)

	return NewStorage(bucket)
}

func TestBasicStorage(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer os.RemoveAll("../_bucketdata")
	results, err := storage.bucket.Get(context.Background(), "5a75f537726934e4b62833ab6d5dca41/metadata.json")
	assert.NoError(t, err)
	assert.NotEqual(t, nil, results)

}

func TestGetTestLogLines(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer os.RemoveAll("../_bucketdata")
	iterator, err := storage.GetTestLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "62dba0159041307f697e6ccc")
	require.NoError(t, err)

	// We should have only the one additional intersecting line from the global logs
	const expectedCount = 12
	lines := []string{}
	for iterator.Next(context.Background()) {
		lines = append(lines, iterator.Item().Data)
	}

	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, "I am a global log within the test start/stop ranges.\n", lines[2])
}

func TestGetTestLogLinesOverlapping(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/overlapping")
	defer os.RemoveAll("../_bucketdata")
	iterator, err := storage.GetTestLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41", "62dba0159041307f697e6ccc")
	require.NoError(t, err)

	// We should have only the one additional intersecting line from the global logs
	const expectedCount = 30
	expectedLines := []string{
		"Test Log400\n",
		"Log400\n",
		"Test Log420\n",
		"Log420\n",
		"Test Log440\n",
		"Log440\n",
		"Test Log460\n",
		"Log460\n",
		"Test Log480\n",
		"Log500\n",
		"Test Log500\n",
		"Log501\n",
		"Test Log520\n",
		"Log520\n",
		"Test Log540\n",
		"Log540\n",
		"Test Log560\n",
		"Log560\n",
		"Log580\n",
		"Test Log600\n",
		"Test Log601\n",
		"Test Log620\n",
		"Test Log640\n",
		"Test Log660\n",
		"Test Log680\n",
		"Test Log700\n",
		"Test Log720\n",
		"Test Log740\n",
		"Test Log760\n",
		"Test Log800\n",
	}
	lines := []string{}
	for iterator.Next(context.Background()) {
		lines = append(lines, iterator.Item().Data)
	}

	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, expectedLines, lines)
}

func TestGetAllLogLinesOverlapping(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/overlapping")
	defer os.RemoveAll("../_bucketdata")
	iterator, err := storage.GetAllLogLines(context.Background(), "5a75f537726934e4b62833ab6d5dca41")
	require.NoError(t, err)

	const expectedCount = 40
	expectedLines := []string{
		"Log300\n",
		"Log320\n",
		"Log340\n",
		"Log360\n",
		"Log380\n",
		"Test Log400\n",
		"Log400\n",
		"Test Log420\n",
		"Log420\n",
		"Test Log440\n",
		"Log440\n",
		"Test Log460\n",
		"Log460\n",
		"Test Log480\n",
		"Log500\n",
		"Test Log500\n",
		"Log501\n",
		"Test Log520\n",
		"Log520\n",
		"Test Log540\n",
		"Log540\n",
		"Test Log560\n",
		"Log560\n",
		"Log580\n",
		"Test Log600\n",
		"Test Log601\n",
		"Test Log620\n",
		"Test Log640\n",
		"Test Log660\n",
		"Test Log680\n",
		"Test Log700\n",
		"Test Log720\n",
		"Test Log740\n",
		"Test Log760\n",
		"Test Log800\n",
		"Log810\n",
		"Log820\n",
		"Log840\n",
		"Log860\n",
		"Log900\n",
	}
	lines := []string{}
	for iterator.Next(context.Background()) {
		lines = append(lines, iterator.Item().Data)
	}

	assert.Equal(t, expectedCount, len(lines))
	assert.Equal(t, expectedLines, lines)
}
