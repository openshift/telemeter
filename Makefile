.PHONY: all build image check test-generate test-integration vendor dependencies manifests

BIN=bin
GOLANGCI_LINT_BIN=$(BIN)/golangci-lint
EMBEDMD_BIN=$(GOPATH)/bin/embedmd
MIXTOOL_BIN=$(GOPATH)/bin/mixtool
# We need jsonnet on CI; here we default to the user's installed jsonnet binary; if nothing is installed, then install go-jsonnet.
JSONNET_BIN=$(if $(shell which jsonnet 2>/dev/null),$(shell which jsonnet 2>/dev/null),$(GOPATH)/bin/jsonnet)
JB_BIN=$(GOPATH)/bin/jb
JSONNET_SRC=$(shell find ./jsonnet -type f)
JSONNET_VENDOR=jsonnet/jsonnetfile.lock.json jsonnet/vendor
DOCS=$(shell grep -rlF [embedmd] docs)

all: build manifests $(DOCS)

build:
	go build ./cmd/telemeter-client
	go build ./cmd/telemeter-server
	go build ./cmd/authorization-server

image:
	imagebuilder -t openshift/telemeter:latest .

$(DOCS): $(JSONNET_SRC) $(EMBEDMD_BIN)
	$(EMBEDMD_BIN) -w $@

test-generate:
	make --always-make && git diff --exit-code

lint: $(GOLANGCI_LINT_BIN)
	# megacheck fails to respect build flags, causing compilation failure during linting.
	# instead, use the unused, gosimple, and staticcheck linters directly
	$(BIN)/golangci-lint run -D megacheck -E unused,gosimple,staticcheck

check: lint
	go test -race ./...

test-integration: build
	./test/integration.sh

vendor:
	glide update -v --skip-test

manifests: $(JSONNET_SRC) $(JSONNET_VENDOR) $(MIXTOOL_BIN)
	rm -rf manifests
	mixtool build jsonnet/client.jsonnet -J jsonnet/vendor -m manifests/client
	mixtool build jsonnet/server.jsonnet -J jsonnet/vendor -m manifests/server
	mixtool build jsonnet/prometheus.jsonnet -J jsonnet/vendor -m manifests/prometheus

$(JSONNET_VENDOR): jsonnet/jsonnetfile.json $(JB_BIN)
	cd jsonnet && jb install

dependencies: $(JB_BIN) $(JSONNET_BIN) $(MIXTOOL_BIN) $(GOLANGCI_LINT_BIN)

$(MIXTOOL_BIN):
	go get -u github.com/metalmatze/mixtool/cmd/mixtool

$(JB_BIN):
	go get -u github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb

$(JSONNET_BIN):
	go get -u github.com/google/go-jsonnet/jsonnet

$(GOLANGCI_LINT_BIN):
	curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | sh -s -- -b $(BIN) v1.10.2

$(EMBEDMD_BIN):
	go get -u github.com/campoy/embedmd
