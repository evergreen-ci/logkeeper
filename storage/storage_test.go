package storage

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetS3Options(t *testing.T) {
	defer os.Clearenv()

	t.Run("MissingBucketAndPath", func(t *testing.T) {
		os.Clearenv()

		opts := BucketOpts{}
		_, err := opts.getS3Options()
		assert.Error(t, err)
	})

	t.Run("MissingPathWithBucket", func(t *testing.T) {
		os.Clearenv()

		bucket := "the_bucket"
		require.NoError(t, os.Setenv(s3BucketEnvVariable, bucket))

		opts := BucketOpts{}
		s3Opts, err := opts.getS3Options()
		assert.NoError(t, err)
		assert.Equal(t, bucket, s3Opts.Name)
	})

	t.Run("MissingBucketWithPath", func(t *testing.T) {
		os.Clearenv()

		path := "the_path"
		opts := BucketOpts{Path: path}
		s3Opts, err := opts.getS3Options()
		assert.NoError(t, err)
		assert.Equal(t, path, s3Opts.Name)
	})
}
