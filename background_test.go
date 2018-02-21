package logkeeper

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMakeSure(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	assert.NotPanics(func() {
		StartBackgroundLogging(ctx)
	})
}
