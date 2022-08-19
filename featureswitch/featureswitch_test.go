package featureswitch

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashToFloat(t *testing.T) {
	actual := hashToFloat([]byte{0xff, 0xff, 0xff, 0xff, 0x01})
	assert.InDelta(t, actual, 1.0, 0.00001, "Max hash should be very close to one")
	assert.Less(t, actual, 1.0, "Max hash should still be less than one")

	actual = hashToFloat([]byte{0x80, 0x00, 0x00, 0x00, 0x00})
	assert.Equal(t, 0.5, actual)

	actual = hashToFloat([]byte{0x00, 0x00, 0x00, 0x00, 0x01})
	assert.Equal(t, 0.0, actual, "Min hash should be exactly 0")
}

func TestMatchesFeatureForHash(t *testing.T) {
	defer os.Clearenv()

	os.Clearenv()
	require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "1.0"))

	actual := matchesFeatureForHash(s3WriteFeatureSwitch, []byte{0xff, 0xff, 0xff, 0xff, 0x01})
	assert.Equal(t, true, actual)

	actual = matchesFeatureForHash(s3WriteFeatureSwitch, []byte{0x00, 0x00, 0x00, 0x00, 0x01})
	assert.Equal(t, true, actual)

	require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "0.0"))
	actual = matchesFeatureForHash(s3WriteFeatureSwitch, []byte{0xff, 0xff, 0xff, 0xff, 0x01})
	assert.Equal(t, false, actual)

	actual = matchesFeatureForHash(s3WriteFeatureSwitch, []byte{0x00, 0x00, 0x00, 0x00, 0x01})
	assert.Equal(t, false, actual)

	require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "0.5"))
	actual = matchesFeatureForHash(s3WriteFeatureSwitch, []byte{0x80, 0x00, 0x00, 0x00, 0x00})
	assert.Equal(t, false, actual)

	actual = matchesFeatureForHash(s3WriteFeatureSwitch, []byte{0x7f, 0xff, 0xff, 0xff, 0x00})
	assert.Equal(t, true, actual)
}

func TestMatchesFeatureForString(t *testing.T) {
	defer os.Clearenv()

	os.Clearenv()
	require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "1.0"))

	actual := matchesFeatureForString(s3WriteFeatureSwitch, "A string")
	assert.Equal(t, true, actual)

	require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "0.0"))
	actual = matchesFeatureForString(s3WriteFeatureSwitch, "A string")
	assert.Equal(t, false, actual)
}

func TestGetThreshold(t *testing.T) {
	defer os.Clearenv()

	t.Run("MissingFlag", func(t *testing.T) {
		os.Clearenv()
		threshold := getThreshold("NONEXISTENT")
		assert.Equal(t, 0.0, threshold)
	})

	t.Run("InvalidFlag", func(t *testing.T) {
		os.Clearenv()

		require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "NoNumber"))
		threshold := getThreshold(s3WriteFeatureSwitch)
		assert.Equal(t, 0.0, threshold)
	})

	t.Run("ZeroFlag", func(t *testing.T) {
		os.Clearenv()
		require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "0"))

		threshold := getThreshold(s3WriteFeatureSwitch)
		assert.Equal(t, 0.0, threshold)
	})

	t.Run("OneFlag", func(t *testing.T) {
		os.Clearenv()
		require.NoError(t, os.Setenv(s3WriteFeatureSwitch, "1.0"))

		threshold := getThreshold(s3WriteFeatureSwitch)
		assert.Equal(t, 1.0, threshold)
	})
}

func TestSetFeatureSwitchLevel(t *testing.T) {
	defer os.Clearenv()
	const switchName = "Switch"

	t.Run("NoVariable", func(t *testing.T) {
		os.Clearenv()
		clearFunc := setFeatureSwitchLevel("Switch", 0.5)
		assert.Equal(t, 0.5, getThreshold("Switch"))
		clearFunc()
		assert.Equal(t, 0.0, getThreshold("Switch"))
	})

	t.Run("AlreadySet", func(t *testing.T) {
		os.Clearenv()
		os.Setenv(switchName, "0.25")
		clearFunc := setFeatureSwitchLevel("Switch", 0.5)
		assert.Equal(t, 0.5, getThreshold("Switch"))
		clearFunc()
		assert.Equal(t, 0.25, getThreshold("Switch"))
	})

}
