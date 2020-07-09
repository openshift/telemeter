// +build tools

// Package tools tracks dependencies for tools that used in the build process.
// See https://github.com/golang/go/issues/25922
package tools

import (
	_ "github.com/brancz/gojsontoyaml"
	_ "github.com/campoy/embedmd"
	_ "github.com/google/go-jsonnet/cmd/jsonnet"
	_ "github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb"
	_ "github.com/observatorium/up"
	_ "github.com/thanos-io/thanos/cmd/thanos"
)
