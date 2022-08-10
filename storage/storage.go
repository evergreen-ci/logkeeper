package storage

import (
	"os"

	"github.com/evergreen-ci/pail"
	"github.com/pkg/errors"
)

const (
	awsKeyEnvVariable    = "AWS_KEY"
	awsSecretEnvVariable = "AWS_SECRET"
	awsBucketEnvVariable = "AWS_S3_BUCKET"
	defaultS3Region      = "us-east-1"

	localBucketPermissions = 0750
)

type Bucket struct {
	pail.Bucket
}

type PailType int

const (
	PailS3 PailType = iota
	PailLocal
)

type BucketOpts struct {
	Location PailType
	Path     string
}

func NewBucket(opts BucketOpts) (Bucket, error) {
	bucket, err := opts.getBucket()
	if err != nil {
		return Bucket{}, errors.Wrap(err, "making bucket")
	}
	return Bucket{bucket}, nil
}

func (opts *BucketOpts) getBucket() (pail.Bucket, error) {
	switch opts.Location {
	case PailLocal:
		if opts.Path == "" {
			return nil, errors.New("local path must be specified")
		}
		if err := os.MkdirAll(opts.Path, localBucketPermissions); err != nil {
			return nil, errors.Wrapf(err, "creating local path '%s'", opts.Path)
		}

		localBucket, err := pail.NewLocalBucket(pail.LocalOptions{
			Path: opts.Path,
		})
		if err != nil {
			return nil, errors.Wrapf(err, "creating local bucket at '%s'", opts.Path)
		}

		return Bucket{localBucket}, nil
	case PailS3:
		s3Options, err := opts.getS3Options()
		if err != nil {
			return nil, errors.Wrap(err, "getting S3 options")
		}
		s3Bucket, err := pail.NewS3Bucket(s3Options)
		if err != nil {
			return nil, errors.Wrap(err, "creating S3 bucket")
		}

		return Bucket{s3Bucket}, nil
	default:
		return nil, errors.Errorf("unknown location '%d'", opts.Location)
	}
}

func (opts *BucketOpts) getS3Options() (pail.S3Options, error) {
	key := os.Getenv(awsKeyEnvVariable)
	if key == "" {
		return pail.S3Options{}, errors.Errorf("environment variable '%s' is not set", awsKeyEnvVariable)
	}

	secret := os.Getenv(awsSecretEnvVariable)
	if secret == "" {
		return pail.S3Options{}, errors.Errorf("environment variable '%s' is not set", awsSecretEnvVariable)
	}

	bucketName := opts.Path
	if bucketName == "" {
		bucketName = os.Getenv(awsBucketEnvVariable)
	}
	if bucketName == "" {
		return pail.S3Options{}, errors.Errorf("path is specified neither in options nor in the environment variable '%s'", awsBucketEnvVariable)
	}

	return pail.S3Options{
		Name:        bucketName,
		Region:      defaultS3Region,
		Credentials: pail.CreateAWSCredentials(key, secret, ""),
	}, nil
}
