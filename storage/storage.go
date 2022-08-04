package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/pail"
	"github.com/pkg/errors"
)

const (
	metadataFilename     = "metadata.json"
	awsKeyEnvVariable    = "AWS_KEY"
	awsSecretEnvVariable = "AWS_SECRET"
)

type Bucket struct {
	pail.Bucket
}

type PailType int

const (
	defaultS3Region = "us-east-1"

	PailS3 PailType = iota
	PailLocal
)

type BucketOpts struct {
	Location PailType
	Path     string
}

func NewBucket(opts BucketOpts) (Bucket, error) {
	switch opts.Location {
	case PailLocal:
		localBucket, err := pail.NewLocalBucket(pail.LocalOptions{
			Path: opts.Path,
		})
		if err != nil {
			return Bucket{}, errors.Wrapf(err, "creating local bucket at '%s'")
		}

		return Bucket{localBucket}, nil
	case PailS3:
		credentials, err := getS3Credentials()
		if err != nil {
			return Bucket{}, errors.Wrap(err, "getting credentials")
		}
		s3Bucket, err := pail.NewS3Bucket(pail.S3Options{
			Name:        opts.Path,
			Region:      defaultS3Region,
			Credentials: credentials,
		})
		if err != nil {
			return Bucket{}, errors.Wrapf(err, "creating S3 bucket in '%s'", opts.Path)
		}

		return Bucket{s3Bucket}, nil
	default:
		return Bucket{}, errors.Errorf("unknown location '%d'", opts.Location)
	}
}

func getS3Credentials() (*credentials.Credentials, error) {
	key := os.Getenv(awsKeyEnvVariable)
	if key == "" {
		return nil, errors.Errorf("environment variable '%s' is not set", awsKeyEnvVariable)
	}
	secret := os.Getenv(awsSecretEnvVariable)
	if secret == "" {
		return nil, errors.Errorf("environment variable '%s' is not set", awsSecretEnvVariable)
	}

	return pail.CreateAWSCredentials(key, secret, ""), nil
}

func parseName(name string) (start time.Time, end time.Time, numLines int64, err error) {
	nameParts := strings.Split(name, "_")
	startNanos, err := strconv.ParseInt(nameParts[0], 10, 64)
	if err != nil {
		return
	}
	start = time.Unix(0, startNanos)

	endNanos, err := strconv.ParseInt(nameParts[1], 10, 64)
	if err != nil {
		return
	}
	end = time.Unix(0, endNanos)

	numLines, err = strconv.ParseInt(nameParts[2], 10, 64)
	if err != nil {
		return
	}
	return
}

func buildPrefix(buildID string) string {
	return fmt.Sprintf("/%s/", buildID)
}

func (b *Bucket) getAllChunks(context context.Context, buildId string) ([]LogChunkInfo, error) {
	iterator, listErr := b.List(context, buildPrefix(buildId))
	buildChunks := []LogChunkInfo{}
	if listErr != nil {
		return nil, listErr
	}
	for iterator.Next(context) {
		if strings.HasSuffix(iterator.Item().Name(), "metadata.json") {
			continue
		}
		if strings.Contains(iterator.Item().Name(), "/tests/") {
			keyParts := strings.Split(iterator.Item().Name(), "/")
			buildID := keyParts[1]
			testID := keyParts[3]
			name := keyParts[4]
			start, end, numLines, nameErr := parseName(name)
			if nameErr != nil {
				return nil, nameErr
			}
			buildChunks = append(buildChunks, LogChunkInfo{
				BuildID:  buildID,
				TestID:   testID,
				Start:    start,
				End:      end,
				NumLines: int(numLines),
			})
		} else {
			keyParts := strings.Split(iterator.Item().Name(), "/")
			buildID := keyParts[1]
			name := keyParts[2]
			start, end, numLines, nameErr := parseName(name)
			if nameErr != nil {
				return nil, nameErr
			}
			buildChunks = append(buildChunks, LogChunkInfo{
				BuildID:  buildID,
				TestID:   "",
				Start:    start,
				End:      end,
				NumLines: int(numLines),
			})
		}
	}
	return buildChunks, nil
}

func (b *Bucket) GetTestLogLines(context context.Context, buildId string, testId string) (LogIterator, error) {
	chunks, err := b.getAllChunks(context, buildId)
	if err != nil {
		return nil, err
	}

	testChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Find our test id
		if chunks[i].TestID == testId {
			testChunks = append(testChunks, chunks[i])
		}
	}
	sort.Slice(testChunks, func(i, j int) bool {
		return testChunks[i].Start.Before(testChunks[j].Start)
	})

	var latestTime = testChunks[len(testChunks)-1].End
	for _, chunk := range testChunks {
		if chunk.End.After(latestTime) {
			latestTime = chunk.End
		}
	}

	testTimeRange := TimeRange{
		StartAt: testChunks[0].Start,
		EndAt:   testChunks[len(testChunks)-1].End,
	}

	testChunkIterator := NewBatchedLogIterator(b, testChunks, 4, testTimeRange)

	buildChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		// Include any build logs that are in the time range of our test
		chunkTimeRange := TimeRange{
			StartAt: chunks[i].Start,
			EndAt:   chunks[i].End,
		}
		// check if the global build chunk's time range intersects the test's time range, and if so
		// add it to our list of build chunks, but constrained to the test's time range to only
		// include entries during that time.
		if chunks[i].TestID == "" && testTimeRange.Intersects(chunkTimeRange) {
			buildChunks = append(buildChunks, chunks[i])
		}
	}

	sort.Slice(buildChunks, func(i, j int) bool {
		return buildChunks[i].Start.Before(buildChunks[j].Start)
	})
	buildChunkIterator := NewBatchedLogIterator(b, buildChunks, 4, testTimeRange)

	// Merge everything together
	return NewMergingIterator(testChunkIterator, buildChunkIterator), nil
}

type BuildMetadata struct {
	ID       string `json:"id"`
	Builder  string `json:"builder"`
	BuildNum int    `json:"buildnum"`
	TaskID   string `json:"task_id"`
}

func newBuildMetadata(b model.Build) BuildMetadata {
	return BuildMetadata{
		ID:       b.Id,
		Builder:  b.Builder,
		BuildNum: b.BuildNum,
		TaskID:   b.Info.TaskID,
	}
}

func (m *BuildMetadata) Key() string {
	return fmt.Sprintf("%s/%s", buildPrefix(m.ID), metadataFilename)
}

func (b *Bucket) UploadBuildMetadata(ctx context.Context, build model.Build) error {
	metadata := newBuildMetadata(build)
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return errors.Wrap(err, "marshaling metadata")
	}

	return errors.Wrapf(b.Put(ctx, metadata.Key(), bytes.NewReader(metadataJSON)), "putting build metadata for build '%s'", build.Id)
}
