package storage

import (
	"context"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"strings"

	"github.com/evergreen-ci/pail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func copyDirectory(source string, target string) error {
	var err error = filepath.Walk(source, func(path string, info fs.FileInfo, err error) error {
		var relPath string = strings.Replace(path, source, "", 1)
		if relPath == "" {
			return nil
		}
		if info.IsDir() {
			return os.Mkdir(filepath.Join(target, relPath), 0755)
		} else {
			var data, fileReadErr = ioutil.ReadFile(filepath.Join(source, relPath))
			if fileReadErr != nil {
				return fileReadErr
			}
			return ioutil.WriteFile(filepath.Join(target, relPath), data, 0666)
		}
	})
	return err
}

func makeTestStorage(t *testing.T, initDir string) Storage {
	os.Mkdir("../_bucketdata", 0755)
	copyDirectory(initDir, "../_bucketdata")
	bucket, err := pail.NewLocalBucket(pail.LocalOptions{
		Path:   "../_bucketdata",
		Prefix: "",
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
