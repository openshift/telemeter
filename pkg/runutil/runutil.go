// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

// The main use case for runutil package is when you want to close a `Closer` interface. As we all know, we should close all implements of `Closer`, such as *os.File. Commonly we will use:
//
// 	defer closer.Close()
//
// The problem is that Close() usually can return important error e.g for os.File the actual file flush might happen (and fail) on `Close` method. It's important to *always* check error. Thanos provides utility functions to log every error like those, allowing to put them in convenient `defer`:
//
// 	defer runutil.CloseWithLogOnErr(logger, closer, "log format message")
//
// For capturing error, use CloseWithErrCapture:
//
// 	var err error
// 	defer runutil.CloseWithErrCapture(&err, closer, "log format message")
//
// 	// ...
//
// If Close() returns error, err will capture it and return by argument.
//
// The rununtil.Exhaust* family of functions provide the same functionality but
// they take an io.ReadCloser and they exhaust the whole reader before closing
// them. They are useful when trying to use http keep-alive connections because
// for the same connection to be re-used the whole response body needs to be
// exhausted.
package runutil

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/efficientgo/tools/core/pkg/merrors"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	pkgerrors "github.com/pkg/errors"
)

// CloseWithLogOnErr is making sure we log every error, even those from best effort tiny closers.
func CloseWithLogOnErr(logger log.Logger, closer io.Closer, format string, a ...interface{}) {
	err := closer.Close()
	if err == nil {
		return
	}

	// Not a problem if it has been closed already.
	if errors.Is(err, os.ErrClosed) {
		return
	}

	if logger == nil {
		logger = log.NewLogfmtLogger(os.Stderr)
	}

	level.Warn(logger).Log("msg", "detected close error", "err", pkgerrors.Wrapf(err, fmt.Sprintf(format, a...)))
}

// ExhaustCloseWithLogOnErr closes the io.ReadCloser with a log message on error but exhausts the reader before.
func ExhaustCloseWithLogOnErr(logger log.Logger, r io.ReadCloser, format string, a ...interface{}) {
	_, err := io.Copy(ioutil.Discard, r)
	if err != nil {
		level.Warn(logger).Log("msg", "failed to exhaust reader, performance may be impeded", "err", err)
	}

	CloseWithLogOnErr(logger, r, format, a...)
}

// CloseWithErrCapture runs function and on error return error by argument including the given error (usually
// from caller function).
func CloseWithErrCapture(err *error, closer io.Closer, format string, a ...interface{}) {
	merr := merrors.NilOrMultiError{}

	merr.Add(*err)
	merr.Add(pkgerrors.Wrapf(closer.Close(), format, a...))

	*err = merr.Err()
}

// ExhaustCloseWithErrCapture closes the io.ReadCloser with error capture but exhausts the reader before.
func ExhaustCloseWithErrCapture(err *error, r io.ReadCloser, format string, a ...interface{}) {
	_, copyErr := io.Copy(ioutil.Discard, r)

	CloseWithErrCapture(err, r, format, a...)

	// Prepend the io.Copy error.
	merr := merrors.NilOrMultiError{}
	merr.Add(copyErr)
	merr.Add(*err)

	*err = merr.Err()
}

// ExhaustCloseRequestBodyHandler ensures that request body is well closed and exhausted at the end of server call.
func ExhaustCloseRequestBodyHandler(logger log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := r.Body
		r.Body = ioutil.NopCloser(r.Body)
		next.ServeHTTP(w, r)
		ExhaustCloseWithLogOnErr(logger, b, "close request body")
	})
}
