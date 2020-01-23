// +build tools

package main

import (
	_ "github.com/brancz/gojsontoyaml"
	_ "github.com/campoy/embedmd"
	_ "github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb"
	_ "github.com/observatorium/up"
	_ "github.com/thanos-io/thanos/cmd/thanos"
	_ "github.com/google/go-jsonnet/cmd/jsonnet"
)
