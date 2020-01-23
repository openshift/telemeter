.PHONY: all build image check test-generate test-integration test-benchmark vendor dependencies manifests
SHELL=/usr/bin/env bash -o pipefail

GO_PKG=github.com/openshift/telemeter
REPO?=quay.io/openshift/telemeter
TAG?=$(shell git rev-parse --short HEAD)

PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test/(?!e2e)')
GOLANG_FILES:=$(shell find . -name \*.go -print)
FIRST_GOPATH:=$(firstword $(subst :, ,$(shell go env GOPATH)))
BIN_DIR?=./_output/bin
LIB_DIR?=./_output/lib
GOLANGCI_LINT_BIN=$(BIN_DIR)/golangci-lint
GOLANGCI_LINT_VERSION=v1.18.0
EMBEDMD_BIN=$(BIN_DIR)/embedmd
THANOS_BIN=$(BIN_DIR)/thanos
UP_BIN=$(BIN_DIR)/up
MEMCACHED_BIN=$(BIN_DIR)/memched
PROMETHEUS_BIN=$(BIN_DIR)/prometheus
GOJSONTOYAML_BIN=$(BIN_DIR)/gojsontoyaml
JSONNET?=$(BIN_DIR)/jsonnet
# We need jsonnet on CI; here we default to the user's installed jsonnet binary; if nothing is installed, then install go-jsonnet.
JSONNET_BIN=$(if $(shell which jsonnet 2>/dev/null),$(shell which jsonnet 2>/dev/null),$(JSONNET))
JB_BIN=$(BIN_DIR)/jb
JSONNET_SRC=$(shell find ./jsonnet -type f)
BENCHMARK_RESULTS=$(shell find ./benchmark -type f -name '*.json')
BENCHMARK_GOAL?=5000
JSONNET_VENDOR=jsonnet/jsonnetfile.lock.json jsonnet/vendor
DOCS=$(shell grep -rlF [embedmd] docs)

GO_BUILD_RECIPE=GOOS=linux CGO_ENABLED=0 go build
CONTAINER_CMD:=docker run --rm \
		-u="$(shell id -u):$(shell id -g)" \
		-v "$(shell go env GOCACHE):/.cache/go-build" \
		-v "$(PWD):/go/src/$(GO_PKG):Z" \
		-w "/go/src/$(GO_PKG)" \
		-e GO111MODULE=on \
		quay.io/coreos/jsonnet-ci


.PHONY: all
all: build manifests $(DOCS)

.PHONY: clean
clean:
	# Remove all files and directories ignored by git.
	git clean -Xfd .

############
# Building #
############

.PHONY: build-in-docker
build-in-docker:
	$(CONTAINER_CMD) $(MAKE) $(MFLAGS) build

.PHONY: build
build:
	go build ./cmd/telemeter-client
	go build ./cmd/telemeter-server
	go build ./cmd/authorization-server
	go build ./cmd/telemeter-benchmark

.PHONY: image
image: .hack-operator-image

.hack-operator-image: Dockerfile
# Create empty target file, for the sole purpose of recording when this target
# was last executed via the last-modification timestamp on the file. See
# https://www.gnu.org/software/make/manual/make.html#Empty-Targets
	docker build -t $(REPO):$(TAG) .
	touch $@

##############
# Generating #
##############

vendor:
	go mod vendor
	go mod tidy
	go mod verify

.PHONY: generate
generate: $(DOCS) manifests

.PHONY: generate-in-docker
generate-in-docker:
	$(CONTAINER_CMD) $(MAKE) $(MFLAGS) generate

$(JSONNET_VENDOR): jsonnet/jsonnetfile.json $(JB_BIN)
	cd jsonnet && ../$(JB_BIN) install

# Can't add test/timeseries.txt as a dependency, otherwise
# running make --always-make will try to regenerate the timeseries
# on CI, which will fail because there is no OpenShift cluster.
$(DOCS): $(JSONNET_SRC) $(EMBEDMD_BIN) docs/telemeter_query
	$(EMBEDMD_BIN) -w $@

docs/telemeter_query: $(JSONNET_SRC)
	query=""; \
	for rule in $$(jsonnet metrics.json | jq -r '.[]'); do \
	    [ ! -z "$$query" ] && query="$$query or "; \
	    query="$$query$$rule"; \
	done; \
	echo "$$query" > $@

manifests: $(JSONNET_BIN) $(JSONNET_SRC) $(JSONNET_VENDOR) $(GOJSONTOYAML_BIN)
	rm -rf manifests
	mkdir -p manifests/{benchmark,client,server,prometheus}
	$(JSONNET_BIN) jsonnet/benchmark.jsonnet -J jsonnet/vendor -m manifests/benchmark
	$(JSONNET_BIN) jsonnet/client.jsonnet -J jsonnet/vendor -m manifests/client
	$(JSONNET_BIN) jsonnet/prometheus.jsonnet -J jsonnet/vendor -m manifests/prometheus
	@for f in $$(find manifests -type f); do\
	    cat $$f | $(GOJSONTOYAML_BIN) > $$f.yaml && rm $$f;\
	done

benchmark.pdf: $(BENCHMARK_RESULTS)
	find ./benchmark -type f -name '*.json' -print0 | xargs -l -0 python3 test/plot.py && gs -dBATCH -dNOPAUSE -q -sDEVICE=pdfwrite -sOutputFile=$@ benchmark/*.pdf


##############
# Formatting #
##############

.PHONY: lint
lint: $(GOLANGCI_LINT_BIN)
	# Check .golangci.yml for configuration
	$(GOLANGCI_LINT_BIN) run -c .golangci.yml

.PHONY: format
format: go-fmt shellcheck

.PHONY: go-fmt
go-fmt:
	go fmt $(PKGS)

.PHONY: shellcheck
shellcheck:
	docker run -v "${PWD}:/mnt" koalaman/shellcheck:stable $(shell find . -type f -name "*.sh" -not -path "*vendor*")

###########
# Testing #
###########

.PHONY: test
test: test-unit test-integration test-benchmark

.PHONY: test-unit
test-unit:
	go test -race -short $(PKGS) -count=1

# TODO(paulfantom): remove this target after removing it from Prow.
test-generate:
	make --always-make && git diff --exit-code

test-integration: build $(THANOS_BIN) $(UP_BIN) $(MEMCACHED_BIN) $(PROMETHEUS_BIN)
	PATH=$$PATH:$$(pwd)/$(BIN_DIR) ./test/integration.sh
	PATH=$$PATH:$$(pwd)/$(BIN_DIR) LD_LIBRARY_PATH=$$LD_LIBRARY_PATH:$$(pwd)/$(LIB_DIR) ./test/integration-v2.sh

test-benchmark: build
	./test/benchmark.sh "" "" $(BENCHMARK_GOAL) "" $(BENCHMARK_GOAL)

test/timeseries.txt:
	oc port-forward -n openshift-monitoring prometheus-k8s-0 9090 > /dev/null & \
	sleep 5 ; \
	query="curl --fail --silent -G http://localhost:9090/federate"; \
	for rule in $$(jsonnet metrics.json | jq -r '.[]'); do \
	    query="$$query $$(printf -- "--data-urlencode match[]=%s" $$rule)"; \
	done; \
	echo '# This file was generated using `make $@`.' > $@ ; \
	$$query >> $@ ; \
	jobs -p | xargs -r kill


############
# Binaries #
############

dependencies: $(JB_BIN) $(GOLANGCI_LINT_BIN) $(THANOS_BIN) $(UP_BIN) $(MEMCACHED_BIN) $(PROMETHEUS_BIN) $(EMBEDMD_BIN) $(GOJSONTOYAML_BIN)

$(BIN_DIR):
	mkdir -p $@

$(LIB_DIR):
	mkdir -p $@

$(THANOS_BIN): $(BIN_DIR)
	GO111MODULE=on go build -mod=vendor -o $@ github.com/thanos-io/thanos/cmd/thanos

$(UP_BIN): $(BIN_DIR)
	GO111MODULE=on go build -mod=vendor -o $@ github.com/observatorium/up

$(MEMCACHED_BIN): $(BIN_DIR) $(LIB_DIR)
	@echo "Downloading Memcached"
	curl -L https://www.archlinux.org/packages/core/x86_64/libevent/download/ | tar --strip-components=2 -xJf - -C $(LIB_DIR) usr/lib
	curl -L https://www.archlinux.org/packages/extra/x86_64/memcached/download/ | tar --strip-components=2 -xJf - -C $(BIN_DIR) usr/bin/memcached

$(PROMETHEUS_BIN): $(BIN_DIR)
	@echo "Downloading Prometheus"
	curl -L "https://github.com/prometheus/prometheus/releases/download/v2.3.2/prometheus-2.3.2.$$(go env GOOS)-$$(go env GOARCH).tar.gz" | tar --strip-components=1 -xzf - -C $(BIN_DIR)

$(EMBEDMD_BIN): $(BIN_DIR)
	GO111MODULE=on go build -mod=vendor -o $@ github.com/campoy/embedmd

$(JSONNET): $(BIN_DIR)
	GO111MODULE=on go build -mod=vendor -o $@ github.com/google/go-jsonnet/cmd/jsonnet

$(JB_BIN): $(BIN_DIR)
	GO111MODULE=on go build -mod=vendor -o $@ github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb

$(GOJSONTOYAML_BIN): $(BIN_DIR)
	GO111MODULE=on go build -mod=vendor -o $@ github.com/brancz/gojsontoyaml

$(GOLANGCI_LINT_BIN): $(BIN_DIR)
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh \
		| sed -e '/install -d/d' \
		| sh -s -- -b $(BIN_DIR) $(GOLANGCI_LINT_VERSION)
