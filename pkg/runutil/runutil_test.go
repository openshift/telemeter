// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package runutil

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/efficientgo/core/testutil"
	"github.com/pkg/errors"
)

type testCloser struct {
	err error
}

func (c testCloser) Close() error {
	return c.err
}

type loggerCapturer struct {
	// WasCalled is true if the Log() function has been called.
	WasCalled bool
}

func (lc *loggerCapturer) Log(keyvals ...interface{}) error {
	lc.WasCalled = true
	return nil
}

type emulatedCloser struct {
	io.Reader

	calls int
}

func (e *emulatedCloser) Close() error {
	e.calls++
	if e.calls == 1 {
		return nil
	}
	if e.calls == 2 {
		return errors.Wrap(os.ErrClosed, "can even be a wrapped one")
	}
	return errors.New("something very bad happened")
}

// newEmulatedCloser returns a ReadCloser with a Close method
// that at first returns success but then returns that
// it has been closed already. After that, it returns that
// something very bad had happened.
func newEmulatedCloser(r io.Reader) io.ReadCloser {
	return &emulatedCloser{Reader: r}
}

func TestCloseMoreThanOnce(t *testing.T) {
	lc := &loggerCapturer{}
	r := newEmulatedCloser(strings.NewReader("somestring"))

	CloseWithLogOnErr(lc, r, "should not be called")
	CloseWithLogOnErr(lc, r, "should not be called")
	testutil.Equals(t, false, lc.WasCalled)

	CloseWithLogOnErr(lc, r, "should be called")
	testutil.Equals(t, true, lc.WasCalled)
}
