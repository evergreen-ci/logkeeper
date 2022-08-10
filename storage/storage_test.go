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

func cleanTestStorage(t *testing.T) {
	err := os.RemoveAll(tempDir)
	require.NoError(t, err)
}

func TestBasicStorage(t *testing.T) {
	storage := makeTestStorage(t, "../testdata/simple")
	defer cleanTestStorage(t)
	results, err := storage.Get(context.Background(), "/builds/5a75f537726934e4b62833ab6d5dca41/metadata.json")
	assert.NoError(t, err)
	assert.NotEqual(t, nil, results)

}

func TestGetS3Options(t *testing.T) {
	defer os.Clearenv()

	t.Run("AllSet", func(t *testing.T) {
		os.Clearenv()

		key := "the_key"
		secret := "the_secret"
		bucket := "the_bucket"
		require.NoError(t, os.Setenv(awsKeyEnvVariable, key))
		require.NoError(t, os.Setenv(awsSecretEnvVariable, secret))
		require.NoError(t, os.Setenv(s3BucketEnvVariable, bucket))

		opts := BucketOpts{}
		s3Opts, err := opts.getS3Options()
		assert.NoError(t, err)
		require.NotNil(t, s3Opts.Credentials)
		creds, err := s3Opts.Credentials.Get()
		assert.NoError(t, err)
		assert.Equal(t, key, creds.AccessKeyID)
		assert.Equal(t, secret, creds.SecretAccessKey)
		assert.Equal(t, bucket, s3Opts.Name)
	})

	t.Run("MissingKey", func(t *testing.T) {
		os.Clearenv()

		secret := "the_secret"
		bucket := "the_bucket"
		require.NoError(t, os.Setenv(awsSecretEnvVariable, secret))
		require.NoError(t, os.Setenv(s3BucketEnvVariable, bucket))

		opts := BucketOpts{}
		_, err := opts.getS3Options()
		assert.Error(t, err)
	})

	t.Run("MissingBucketAndPath", func(t *testing.T) {
		os.Clearenv()

		key := "the_key"
		secret := "the_secret"
		require.NoError(t, os.Setenv(awsKeyEnvVariable, key))
		require.NoError(t, os.Setenv(awsSecretEnvVariable, secret))

		opts := BucketOpts{}
		_, err := opts.getS3Options()
		assert.Error(t, err)
	})

	t.Run("MissingBucketWithPath", func(t *testing.T) {
		os.Clearenv()

		key := "the_key"
		secret := "the_secret"
		path := "the_path"
		require.NoError(t, os.Setenv(awsKeyEnvVariable, key))
		require.NoError(t, os.Setenv(awsSecretEnvVariable, secret))

		opts := BucketOpts{Path: path}
		s3Opts, err := opts.getS3Options()
		assert.NoError(t, err)
		assert.Equal(t, path, s3Opts.Name)
	})
}
