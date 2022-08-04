package storage

import (
	"context"
	"os"
	"testing"

	"github.com/evergreen-ci/pail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tempDir = "../_bucketdata"

func makeTestStorage(t *testing.T, initDir string) Bucket {
	err := os.RemoveAll(tempDir)
	require.NoError(t, err)
	err = os.Mkdir(tempDir, 0755)
	require.NoError(t, err)

	bucket, err := NewBucket(BucketOpts{
		Location: PailLocal,
		Path:     tempDir,
	})
	require.NoError(t, err)

	if initDir != "" {
		err = bucket.Push(context.Background(), pail.SyncOptions{
			Local:  initDir,
			Remote: "/",
		})
		require.NoError(t, err)
	}

	return bucket
}

func TestBasicStorage(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer os.RemoveAll(tempDir)
	results, err := storage.Get(context.Background(), "5a75f537726934e4b62833ab6d5dca41/metadata.json")
	assert.NoError(t, err)
	assert.NotEqual(t, nil, results)

}
