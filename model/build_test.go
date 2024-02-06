package model

import (
	"context"
	"go.opentelemetry.io/otel"
	"io"
	"testing"

	"github.com/evergreen-ci/logkeeper/env"
	"github.com/evergreen-ci/logkeeper/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBuildID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop

	result, err := NewBuildID(ctx, tracer, "A", 123)
	require.NoError(t, err)
	assert.Equal(t, "1e7747b3e13274f0bee0de868c8314c9", result)

	result, err = NewBuildID(ctx, tracer, "", -10000)
	require.NoError(t, err)
	assert.Equal(t, "7d2e3a33d801c1ac74f062b41c977104", result)

	result, err = NewBuildID(ctx, tracer, `{"builder": "builder", "buildNum": "1000"}`, 0)
	require.NoError(t, err)
	assert.Equal(t, "ed39e8e7310193625e521204242e80c4", result)

	result, err = NewBuildID(ctx, tracer, "10", 100)
	require.NoError(t, err)
	assert.Equal(t, "f4088565508a32f3e6ff9205408bcce9", result)

	result, err = NewBuildID(ctx, tracer, "100", 10)
	require.NoError(t, err)
	assert.Equal(t, "b2f7b29a7f76e38abe38fc8145c0cf98", result)
}

func TestUploadBuildMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop

	defer testutil.SetBucket(t, "")()
	build := Build{
		ID:       "5a75f537726934e4b62833ab6d5dca41",
		Builder:  "builder0",
		BuildNum: 1,
		TaskID:   "t0",
	}
	expectedData, err := build.toJSON()
	require.NoError(t, err)
	require.NoError(t, build.UploadMetadata(ctx, tracer))

	r, err := env.Bucket().Get(ctx, "/builds/5a75f537726934e4b62833ab6d5dca41/metadata.json")
	require.NoError(t, err)
	defer r.Close()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

func TestBuildKey(t *testing.T) {
	build := Build{
		ID:            "b0",
		Builder:       "builder0",
		BuildNum:      1,
		TaskID:        "t0",
		TaskExecution: 1,
	}
	assert.Equal(t, "builds/b0/metadata.json", build.key())
}

func TestBuildToJSON(t *testing.T) {
	build := Build{
		ID:            "b0",
		Builder:       "builder0",
		BuildNum:      1,
		TaskID:        "t0",
		TaskExecution: 1,
	}
	data, err := build.toJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"b0","builder":"builder0","buildnum":1,"task_id":"t0","execution":1}`, string(data))
}

func TestCheckBuildMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer testutil.SetBucket(t, "../testdata/simple")()
	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop

	t.Run("MetadataExists", func(t *testing.T) {
		exists, err := CheckBuildMetadata(ctx, tracer, "5a75f537726934e4b62833ab6d5dca41")
		require.NoError(t, err)
		assert.True(t, exists)
	})
	t.Run("NonexistentBuild", func(t *testing.T) {
		exists, err := CheckBuildMetadata(ctx, tracer, "DOA")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestFindBuildByID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer testutil.SetBucket(t, "../testdata/simple")()

	tracer := otel.GetTracerProvider().Tracer("noop_tracer") // default noop
	t.Run("Exists", func(t *testing.T) {
		expected := &Build{
			ID:       "5a75f537726934e4b62833ab6d5dca41",
			Builder:  "MCI_enterprise-rhel_job0",
			BuildNum: 157865445,
			TaskID:   "mongodb_mongo_master_enterprise_f98b3361fbab4e02683325cc0e6ebaa69d6af1df_22_07_22_11_24_37",
		}
		actual, err := FindBuildByID(ctx, tracer, "5a75f537726934e4b62833ab6d5dca41")
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	})
	t.Run("DNE", func(t *testing.T) {
		build, err := FindBuildByID(ctx, tracer, "DNE")
		require.NoError(t, err)
		assert.Nil(t, build)
	})
}
