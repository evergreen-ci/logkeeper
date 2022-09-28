package testutil

import (
	"context"
	"testing"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/storage"
	"github.com/evergreen-ci/pail"
	"github.com/stretchr/testify/require"
)

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
	if err := env.SetBucket(&bucket); err != nil {
		t.Error(err)
	}

	return func() {
		if originalBucket == nil {
			return
		}

		if err := env.SetBucket(originalBucket); err != nil {
			t.Error(err)
		}
	}
}
