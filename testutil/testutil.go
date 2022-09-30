package testutil

import (
	"context"
	"testing"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/storage"
	"github.com/evergreen-ci/pail"
	"github.com/stretchr/testify/require"
)

// SetBucket sets the bucket in the environment to a local pail bucket
// backed by a temporary directory.
// If initDir is not the empty string the contents of initDir are copied into the local bucket.
// If an error is encounter it will fail the test.
func SetBucket(t *testing.T, initDir string) func() {
	originalBucket := env.Bucket()

	bucket, err := storage.NewBucket(storage.BucketOpts{
		Location: storage.PailLocal,
		Path:     t.TempDir(),
	})
	require.NoError(t, err)

	if initDir != "" {
		err = bucket.Push(context.Background(), pail.SyncOptions{
			Local:  initDir,
			Remote: "/",
		})
		require.NoError(t, err)
	}
	require.NoError(t, env.SetBucket(&bucket))

	return func() {
		if originalBucket != nil {
			require.NoError(t, env.SetBucket(originalBucket))
		}
	}
}
