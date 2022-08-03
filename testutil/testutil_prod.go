//go:build !test
// +build !test

package testutil

import (
	"github.com/pkg/errors"
)

// ClearCollections always returns an error in production.
func ClearCollections(collections ...string) error {
	return errors.New("ClearCollections can't be called outside of tests")
}
