package storage

import (
	"bufio"
	"container/heap"
	"context"
	"io"
	"runtime"
	"sync"

	"github.com/evergreen-ci/logkeeper/model"
	"github.com/evergreen-ci/pail"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/mongodb/grip/recovery"
	"github.com/pkg/errors"
)

// LogIterator is an interface that enables iterating over lines of buildlogger
// logs.
type LogIterator interface {
	Iterator
	// Item returns the current LogLine item held by the iterator.
	Item() model.LogLineItem
	// Reverse returns a reversed copy of the iterator.
	Reverse() LogIterator
	// IsReversed returns true if the iterator is in reverse order and
	// false otherwise.
	IsReversed() bool
	// Stream returns a chan of log lines from the iterator.
	Stream(context.Context) chan *model.LogLineItem
}

//////////////////////
// Serialized Iterator
//////////////////////

type serializedIterator struct {
	bucket               pail.Bucket
	chunks               []LogChunkInfo
	timeRange            TimeRange
	reverse              bool
	lineCount            int
	keyIndex             int
	currentReadCloser    io.ReadCloser
	currentReverseReader *reverseLineReader
	currentReader        *bufio.Reader
	currentItem          model.LogLineItem
	catcher              grip.Catcher
	exhausted            bool
	closed               bool
	testID               *model.TestID
}

// NewSerializedLogIterator returns a LogIterator that serially fetches chunks
// from blob storage while iterating over lines of a buildlogger log.
func NewSerializedLogIterator(bucket pail.Bucket, chunks []LogChunkInfo, timeRange TimeRange, testID *model.TestID) LogIterator {
	chunks = filterChunksByTimeRange(timeRange, chunks)

	return &serializedIterator{
		bucket:    bucket,
		chunks:    chunks,
		timeRange: timeRange,
		catcher:   grip.NewBasicCatcher(),
		testID:    testID,
	}
}

func (i *serializedIterator) Reverse() LogIterator {
	chunks := make([]LogChunkInfo, len(i.chunks))
	_ = copy(chunks, i.chunks)
	reverseChunks(chunks)

	return &serializedIterator{
		bucket:    i.bucket,
		chunks:    chunks,
		timeRange: i.timeRange,
		reverse:   !i.reverse,
		catcher:   grip.NewBasicCatcher(),
	}
}

func (i *serializedIterator) IsReversed() bool { return i.reverse }

func (i *serializedIterator) Next(ctx context.Context) bool {
	if i.closed {
		return false
	}

	for {
		if i.currentReader == nil && i.currentReverseReader == nil {
			if i.keyIndex >= len(i.chunks) {
				i.exhausted = true
				return false
			}

			var err error
			i.currentReadCloser, err = i.bucket.Get(ctx, i.chunks[i.keyIndex].key())
			if err != nil {
				i.catcher.Wrap(err, "downloading log artifact")
				return false
			}
			if i.reverse {
				i.currentReverseReader = newReverseLineReader(i.currentReadCloser)
			} else {
				i.currentReader = bufio.NewReader(i.currentReadCloser)
			}
		}

		var data string
		var err error
		if i.reverse {
			data, err = i.currentReverseReader.ReadLine()
		} else {
			data, err = i.currentReader.ReadString('\n')
		}
		if err == io.EOF {
			if i.lineCount != i.chunks[i.keyIndex].NumLines {
				i.catcher.Add(errors.New("corrupt data"))
			}

			i.catcher.Wrap(i.currentReadCloser.Close(), "closing ReadCloser")
			i.currentReadCloser = nil
			i.currentReverseReader = nil
			i.currentReader = nil
			i.lineCount = 0
			i.keyIndex++

			return i.Next(ctx)
		}
		if err != nil {
			i.catcher.Wrap(err, "getting line")
			return false
		}

		item, err := parseLogLineString(data)
		if err != nil {
			i.catcher.Wrap(err, "parsing timestamp")
			return false
		}
		item.TestId = i.testID
		i.lineCount++

		if item.Timestamp.After(i.timeRange.EndAt) && !i.reverse {
			i.exhausted = true
			return false
		}
		if item.Timestamp.Before(i.timeRange.StartAt) && i.reverse {
			i.exhausted = true
			return false
		}

		if (item.Timestamp.After(i.timeRange.StartAt) || item.Timestamp.Equal(i.timeRange.StartAt)) &&
			(item.Timestamp.Before(i.timeRange.EndAt) || item.Timestamp.Equal(i.timeRange.EndAt)) {
			i.currentItem = item
			break
		}
	}

	return true
}

func (i *serializedIterator) Exhausted() bool { return i.exhausted }

func (i *serializedIterator) Err() error { return i.catcher.Resolve() }

func (i *serializedIterator) Item() model.LogLineItem { return i.currentItem }

func (i *serializedIterator) Close() error {
	i.closed = true
	if i.currentReadCloser != nil {
		return i.currentReadCloser.Close()
	}

	return nil
}

func (i *serializedIterator) Stream(ctx context.Context) chan *model.LogLineItem {
	return streamFromLogIterator(ctx, i)
}

///////////////////
// Batched Iterator
///////////////////

type batchedIterator struct {
	bucket               pail.Bucket
	batchSize            int
	chunks               []LogChunkInfo
	chunkIndex           int
	timeRange            TimeRange
	reverse              bool
	lineCount            int
	keyIndex             int
	readers              map[string]io.ReadCloser
	currentReverseReader *reverseLineReader
	currentReader        *bufio.Reader
	currentItem          model.LogLineItem
	catcher              grip.Catcher
	exhausted            bool
	closed               bool
	testID               *model.TestID
}

// NewBatchedLog returns a LogIterator that fetches batches (size set by the
// caller) of chunks from blob storage in parallel while iterating over lines
// of a buildlogger log.
func NewBatchedLogIterator(bucket pail.Bucket, chunks []LogChunkInfo, batchSize int, timeRange TimeRange, testID *model.TestID) LogIterator {
	chunks = filterChunksByTimeRange(timeRange, chunks)

	return &batchedIterator{
		bucket:    bucket,
		batchSize: batchSize,
		chunks:    chunks,
		timeRange: timeRange,
		catcher:   grip.NewBasicCatcher(),
		testID:    testID,
	}
}

// NewParallelizedLogIterator returns a LogIterator that fetches all chunks
// from blob storage in parallel while iterating over lines of a buildlogger
// log.
func NewParallelizedLogIterator(bucket pail.Bucket, chunks []LogChunkInfo, timeRange TimeRange, testID *model.TestID) LogIterator {
	chunks = filterChunksByTimeRange(timeRange, chunks)

	return &batchedIterator{
		bucket:    bucket,
		batchSize: len(chunks),
		chunks:    chunks,
		timeRange: timeRange,
		catcher:   grip.NewBasicCatcher(),
		testID:    testID,
	}
}

func (i *batchedIterator) Reverse() LogIterator {
	chunks := make([]LogChunkInfo, len(i.chunks))
	_ = copy(chunks, i.chunks)
	reverseChunks(chunks)

	return &batchedIterator{
		bucket:    i.bucket,
		batchSize: i.batchSize,
		chunks:    chunks,
		timeRange: i.timeRange,
		reverse:   !i.reverse,
		catcher:   grip.NewBasicCatcher(),
	}
}

func (i *batchedIterator) IsReversed() bool { return i.reverse }

func (i *batchedIterator) getNextBatch(ctx context.Context) error {
	catcher := grip.NewBasicCatcher()
	for _, r := range i.readers {
		catcher.Add(r.Close())
	}
	if err := catcher.Resolve(); err != nil {
		return errors.Wrap(err, "closing readers")
	}

	end := i.chunkIndex + i.batchSize
	if end > len(i.chunks) {
		end = len(i.chunks)
	}
	work := make(chan LogChunkInfo, end-i.chunkIndex)
	for _, chunk := range i.chunks[i.chunkIndex:end] {
		work <- chunk
	}
	close(work)
	var wg sync.WaitGroup
	var mux sync.Mutex
	readers := map[string]io.ReadCloser{}
	catcher = grip.NewBasicCatcher()

	for j := 0; j < runtime.NumCPU(); j++ {
		wg.Add(1)
		go func() {
			defer func() {
				catcher.Add(recovery.HandlePanicWithError(recover(), nil, "log iterator worker"))
				wg.Done()
			}()

			for chunk := range work {
				if err := ctx.Err(); err != nil {
					catcher.Add(err)
					return
				}

				r, err := i.bucket.Get(ctx, chunk.key())
				if err != nil {
					catcher.Add(err)
					return
				}
				mux.Lock()
				readers[chunk.key()] = r
				mux.Unlock()
			}
		}()
	}
	wg.Wait()

	i.chunkIndex = end
	i.readers = readers
	return errors.Wrap(catcher.Resolve(), "downloading log artifacts")
}

func (i *batchedIterator) Next(ctx context.Context) bool {
	if i.closed {
		return false
	}

	for {
		if i.currentReader == nil && i.currentReverseReader == nil {
			if i.keyIndex >= len(i.chunks) {
				i.exhausted = true
				return false
			}

			reader, ok := i.readers[i.chunks[i.keyIndex].key()]
			if !ok {
				if err := i.getNextBatch(ctx); err != nil {
					i.catcher.Add(err)
					return false
				}
				continue
			}

			if i.reverse {
				i.currentReverseReader = newReverseLineReader(reader)
			} else {
				i.currentReader = bufio.NewReader(reader)
			}
		}

		var data string
		var err error
		if i.reverse {
			data, err = i.currentReverseReader.ReadLine()
		} else {
			data, err = i.currentReader.ReadString('\n')
		}
		if err == io.EOF {
			if i.lineCount != i.chunks[i.keyIndex].NumLines {
				i.catcher.Add(errors.New("corrupt data"))
			}

			i.currentReverseReader = nil
			i.currentReader = nil
			i.lineCount = 0
			i.keyIndex++

			return i.Next(ctx)
		} else if err != nil {
			i.catcher.Wrap(err, "getting line")
			return false
		}

		item, err := parseLogLineString(data)
		if err != nil {
			i.catcher.Wrap(err, "parsing timestamp")
			return false
		}
		item.TestId = i.testID
		i.lineCount++

		if item.Timestamp.After(i.timeRange.EndAt) && !i.reverse {
			i.exhausted = true
			return false
		}
		if item.Timestamp.Before(i.timeRange.StartAt) && i.reverse {
			i.exhausted = true
			return false
		}
		if (item.Timestamp.After(i.timeRange.StartAt) || item.Timestamp.Equal(i.timeRange.StartAt)) &&
			(item.Timestamp.Before(i.timeRange.EndAt) || item.Timestamp.Equal(i.timeRange.EndAt)) {
			i.currentItem = item
			break
		}
	}

	return true
}

func (i *batchedIterator) Exhausted() bool { return i.exhausted }

func (i *batchedIterator) Err() error { return i.catcher.Resolve() }

func (i *batchedIterator) Item() model.LogLineItem { return i.currentItem }

func (i *batchedIterator) Close() error {
	i.closed = true
	catcher := grip.NewBasicCatcher()

	for _, r := range i.readers {
		catcher.Add(r.Close())
	}

	return catcher.Resolve()
}

func (i *batchedIterator) Stream(ctx context.Context) chan *model.LogLineItem {
	return streamFromLogIterator(ctx, i)
}

///////////////////
// Merging Iterator
///////////////////

type mergingIterator struct {
	iterators    []LogIterator
	iteratorHeap *LogIteratorHeap
	currentItem  model.LogLineItem
	catcher      grip.Catcher
	started      bool
}

// NewMergeIterator returns a LogIterator that merges N buildlogger logs,
// passed in as LogIterators, respecting the order of each line's timestamp.
func NewMergingIterator(iterators ...LogIterator) LogIterator {
	return &mergingIterator{
		iterators:    iterators,
		iteratorHeap: &LogIteratorHeap{min: true},
		catcher:      grip.NewBasicCatcher(),
	}
}

func (i *mergingIterator) Reverse() LogIterator {
	for j := range i.iterators {
		if !i.iterators[j].IsReversed() {
			i.iterators[j] = i.iterators[j].Reverse()
		}
	}

	return &mergingIterator{
		iterators:    i.iterators,
		iteratorHeap: &LogIteratorHeap{min: false},
		catcher:      grip.NewBasicCatcher(),
	}
}

func (i *mergingIterator) IsReversed() bool { return !i.iteratorHeap.min }

func (i *mergingIterator) Next(ctx context.Context) bool {
	if !i.started {
		i.init(ctx)
	}

	it := i.iteratorHeap.SafePop()
	if it == nil {
		return false
	}
	i.currentItem = it.Item()

	if it.Next(ctx) {
		i.iteratorHeap.SafePush(it)
	} else {
		i.catcher.Add(it.Err())
		i.catcher.Add(it.Close())
		if i.catcher.HasErrors() {
			return false
		}
	}

	return true
}

func (i *mergingIterator) Exhausted() bool {
	exhaustedCount := 0
	for _, it := range i.iterators {
		if it.Exhausted() {
			exhaustedCount += 1
		}
	}

	return exhaustedCount == len(i.iterators)
}

func (i *mergingIterator) init(ctx context.Context) {
	heap.Init(i.iteratorHeap)

	for j := range i.iterators {
		if i.iterators[j].Next(ctx) {
			i.iteratorHeap.SafePush(i.iterators[j])
		}

		// Fail early.
		if i.iterators[j].Err() != nil {
			i.catcher.Add(i.iterators[j].Err())
			i.iteratorHeap = &LogIteratorHeap{}
			break
		}
	}

	i.started = true
}

func (i *mergingIterator) Err() error { return i.catcher.Resolve() }

func (i *mergingIterator) Item() model.LogLineItem { return i.currentItem }

func (i *mergingIterator) Close() error {
	catcher := grip.NewBasicCatcher()

	for {
		it := i.iteratorHeap.SafePop()
		if it == nil {
			break
		}
		catcher.Add(it.Close())
	}

	return catcher.Resolve()
}

func (i *mergingIterator) Stream(ctx context.Context) chan *model.LogLineItem {
	return streamFromLogIterator(ctx, i)
}

///////////////////
// Helper functions
///////////////////

func filterChunksByTimeRange(timeRange TimeRange, chunks []LogChunkInfo) []LogChunkInfo {
	filteredChunks := []LogChunkInfo{}
	for i := 0; i < len(chunks); i++ {
		if timeRange.Intersects(NewTimeRange(chunks[i].Start, chunks[i].End)) {
			filteredChunks = append(filteredChunks, chunks[i])
		}
	}

	return filteredChunks
}

func reverseChunks(chunks []LogChunkInfo) {
	for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	}
}

func streamFromLogIterator(ctx context.Context, iter LogIterator) chan *model.LogLineItem {
	logLines := make(chan *model.LogLineItem)
	go func() {
		defer recovery.LogStackTraceAndContinue("streaming lines from log iterator")
		defer close(logLines)

		for iter.Next(ctx) {
			item := iter.Item()
			logLines <- &item
		}

		if err := iter.Err(); err != nil {
			grip.Error(message.WrapError(err, message.Fields{
				"message": "streaming lines from log iterator",
			}))
		}
	}()

	return logLines
}

///////////////////
// LogIteratorHeap
///////////////////

// LogIteratorHeap is a heap of LogIterator items.
type LogIteratorHeap struct {
	its []LogIterator
	min bool
}

// Len returns the size of the heap.
func (h LogIteratorHeap) Len() int { return len(h.its) }

// Less returns true if the object at index i is less than the object at index
// j in the heap, false otherwise, when min is true. When min is false, the
// opposite is returned.
func (h LogIteratorHeap) Less(i, j int) bool {
	iItem := h.its[i].Item()
	jItem := h.its[j].Item()

	if iItem.Timestamp.Equal(jItem.Timestamp) {
		if iItem.TestId == nil {
			return h.min
		}
		return !h.min
	}

	return iItem.Timestamp.Before(jItem.Timestamp) && h.min || iItem.Timestamp.After(jItem.Timestamp) && !h.min
}

// Swap swaps the objects at indexes i and j.
func (h LogIteratorHeap) Swap(i, j int) { h.its[i], h.its[j] = h.its[j], h.its[i] }

// Push appends a new object of type LogIterator to the heap. Note that if x is
// not a LogIterator nothing happens.
func (h *LogIteratorHeap) Push(x interface{}) {
	it, ok := x.(LogIterator)
	if !ok {
		return
	}

	h.its = append(h.its, it)
}

// Pop returns the next object (as an empty interface) from the heap. Note that
// if the heap is empty this will panic.
func (h *LogIteratorHeap) Pop() interface{} {
	old := h.its
	n := len(old)
	x := old[n-1]
	h.its = old[0 : n-1]
	return x
}

// SafePush is a wrapper function around heap.Push that ensures, during compile
// time, that the correct type of object is put in the heap.
func (h *LogIteratorHeap) SafePush(it LogIterator) {
	heap.Push(h, it)
}

// SafePop is a wrapper function around heap.Pop that converts the returned
// interface into a LogIterator object before returning it.
func (h *LogIteratorHeap) SafePop() LogIterator {
	if h.Len() == 0 {
		return nil
	}

	i := heap.Pop(h)
	it := i.(LogIterator)
	return it
}

////////////////////
// reverseLineReader
////////////////////

type reverseLineReader struct {
	r     *bufio.Reader
	lines []string
	i     int
}

func newReverseLineReader(r io.Reader) *reverseLineReader {
	return &reverseLineReader{r: bufio.NewReader(r)}
}

func (r *reverseLineReader) ReadLine() (string, error) {
	if r.lines == nil {
		if err := r.getLines(); err != nil {
			return "", errors.Wrap(err, "reading lines")
		}
	}

	r.i--
	if r.i < 0 {
		return "", io.EOF
	}

	return r.lines[r.i], nil
}

func (r *reverseLineReader) getLines() error {
	r.lines = []string{}

	for {
		p, err := r.r.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.WithStack(err)
		}

		r.lines = append(r.lines, p)
	}

	r.i = len(r.lines)

	return nil
}
