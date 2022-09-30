package env

import (
	"sync"

	"github.com/evergreen-ci/logkeeper/storage"
	"github.com/pkg/errors"
)

type environment struct {
	bucket *storage.Bucket
	sync.RWMutex
}

var globalEnv *environment

func init() {
	globalEnv = &environment{}
}

// SetBucket caches a storage Bucket to be available from the environment.
func SetBucket(b *storage.Bucket) error {
	if b == nil {
		return errors.New("cannot set a nil bucket")
	}

	globalEnv.Lock()
	defer globalEnv.Unlock()
	globalEnv.bucket = b

	return nil
}

// Bucket returns the cached storage bucket from the environment.
func Bucket() *storage.Bucket {
	globalEnv.RLock()
	defer globalEnv.RUnlock()

	return globalEnv.bucket
}
