package logkeeper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackgroundLogging(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*backgroundLoggingInterval)
	defer cancel()

	assert.NotPanics(func() {
		BackgroundLogging(ctx)
	})
}
