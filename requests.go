package logkeeper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

var ErrReadSizeLimitExceeded = errors.New("read size limit exceeded")

// A LimitedReader reads from R but limits the amount of
// data returned to just N bytes. Each call to Read
// updates N to reflect the new amount remaining.
// Note: this is identical to io.LimitedReader, but throws ErrReadSizeLimitExceeded
// so it can be distinguished from a normal EOF.
type LimitedReader struct {
	R io.Reader // underlying reader
	N int       // max bytes remaining
}

// Read returns an error if the bytes in the reader exceed the maximum size
// threshold for the reader, but fail to
func (l *LimitedReader) Read(p []byte) (n int, err error) {
	if l.N <= 0 {
		return 0, ErrReadSizeLimitExceeded
	}
	if len(p) > l.N {
		p = p[0:l.N]
	}
	n, err = l.R.Read(p)
	l.N -= n
	return
}

func readJSON(body io.Reader, maxSize int, out interface{}) *apiError {
	decoder := json.NewDecoder(&LimitedReader{body, maxSize})

	err := decoder.Decode(out)
	if err == ErrReadSizeLimitExceeded {
		return &apiError{
			Err:     err.Error(),
			MaxSize: maxSize,
			code:    http.StatusRequestEntityTooLarge,
		}
	} else if err != nil {
		return &apiError{
			Err:  err.Error(),
			code: http.StatusBadRequest,
		}
	}

	return nil
}

type ctxKey int

const (
	requestIDKey ctxKey = iota
	startAtKey
)

func setCtxRequestId(reqID int, r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestIDKey, reqID))
}

func getCtxRequestId(r *http.Request) int {
	if val := r.Context().Value(requestIDKey); val != nil {
		if id, ok := val.(int); ok {
			return id
		}
	}

	return 0
}

func setStartAtTime(r *http.Request, startAt time.Time) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), startAtKey, startAt))
}

func getRequestStartAt(ctx context.Context) time.Time {
	if rv := ctx.Value(startAtKey); rv != nil {
		if t, ok := rv.(time.Time); ok {
			return t
		}
	}

	return time.Time{}
}
