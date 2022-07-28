package storage

import (
	"github.com/evergreen-ci/pail"
)

type Storage struct {
	bucket pail.Bucket
}

func NewStorage(bucket pail.Bucket) Storage {
	return Storage{
		bucket: bucket,
	}
}
