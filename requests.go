package logkeeper

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/context"
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
	if int(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.R.Read(p)
	l.N -= n
	return
}

func readJSON(body io.ReadCloser, maxSize int, out interface{}) *apiError {
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
	requestCtxKey ctxKey = iota
)

func SetCtxRequestId(reqId int, req *http.Request) {
	context.Set(req, requestCtxKey, reqId)
}

func GetCtxRequestId(req *http.Request) int {
	val, ok := context.GetOk(req, requestCtxKey)
	if !ok {
		return 0
	}
	return val.(int)
}
