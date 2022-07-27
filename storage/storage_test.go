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
	os.RemoveAll("../_bucketdata")
	os.Mkdir("../_bucketdata", 0755)
	bucket, err := pail.NewLocalBucket(pail.LocalOptions{
		Path:   "../_bucketdata",
		Prefix: "",
	})
	bucket.Push(context.Background(), pail.SyncOptions{
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
