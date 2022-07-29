package storage

import "context"

// Iterator represents a cursor for generic iteration over a sequence of items.
type Iterator interface {
	// Next returns true if the iterator has not yet been exhausted or
	// closed, false otherwise.
	Next(context.Context) bool
	// Exhausted returns true if the iterator has not yet been exhausted,
	// regardless if it has been closed or not.
	Exhausted() bool
	// Err returns any errors that are captured by the iterator.
	Err() error
	// Close closes the iterator. This function should be called once the
	// iterator is no longer needed.
	Close() error
}
