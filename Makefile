include .bingo/Variables.mk

.PHONY: all build image check test-generate test-integration test-benchmark vendor dependencies manifests
SHELL=/usr/bin/env bash -o pipefail

GO_PKG=github.com/openshift/telemeter
REPO?=quay.io/openshift/telemeter
TAG?=$(shell git rev-parse --short HEAD)

PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test/(?!e2e)')
GOLANG_FILES:=$(shell find . -name \*.go -print)
FIRST_GOPATH:=$(firstword $(subst :, ,$(shell go env GOPATH)))
BIN_DIR?=$(shell pwd)/_output/bin
LIB_DIR?=./_output/lib
CACHE_DIR?=$(shell pwd)/_output/cache
METRICS_JSON=./_output/metrics.json
THANOS_BIN=$(BIN_DIR)/thanos
MEMCACHED_BIN=$(BIN_DIR)/memcached
PROMETHEUS_BIN=$(BIN_DIR)/prometheus
# We need jsonnet on CI; here we default to the user's installed jsonnet binary; if nothing is installed, then install go-jsonnet.
JSONNET_LOCAL_OR_INSTALLED=$(if $(shell which jsonnet 2>/dev/null),$(shell which jsonnet 2>/dev/null),$(JSONNET))
JSONNETFMT_LOCAL_OR_INSTALLED=$(if $(shell which jsonnetfmt 2>/dev/null),$(shell which jsonnetfmt 2>/dev/null),$(JSONNETFMT))
JSONNET_SRC=$(shell find ./jsonnet -path ./jsonnet/vendor -prune -false -o -type f \( -name "*.jsonnet" -o -name "*.libsonnet" \))
BENCHMARK_RESULTS=$(shell find ./benchmark -type f -name '*.json')
BENCHMARK_GOAL?=5000
JSONNET_VENDOR=jsonnet/jsonnetfile.lock.json jsonnet/vendor
DOCS=$(shell grep -rlF [embedmd] docs)

export PATH := $(BIN_DIR):$(PATH)

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

$(JSONNET_VENDOR): $(JB) jsonnet/jsonnetfile.json
	cd jsonnet && $(JB) install

# Can't add test/timeseries.txt as a dependency, otherwise
# running make --always-make will try to regenerate the timeseries
# on CI, which will fail because there is no OpenShift cluster.
$(DOCS): $(JSONNET_SRC) $(EMBEDMD) docs/telemeter_query
	$(EMBEDMD) -w $@

docs/telemeter_query: $(JSONNET_SRC)
	query=""; \
	for rule in $$(jsonnet metrics.json | jq -r '.[]'); do \
	    [ ! -z "$$query" ] && query="$$query or "; \
	    query="$$query$$rule"; \
	done; \
	echo "$$query" > $@

$(METRICS_JSON): $(GOJSONTOYAML)
	curl -L https://raw.githubusercontent.com/openshift/cluster-monitoring-operator/844e7afabfcfa4162c716ea18cd8e2d010789de1/manifests/0000_50_cluster_monitoring_operator_04-config.yaml | \
	    $(GOJSONTOYAML) --yamltojson | jq -r '.data."metrics.yaml"' | $(GOJSONTOYAML) --yamltojson | jq -r '.matches' > $@

manifests: $(BIN_DIR) $(JSONNET_LOCAL_OR_INSTALLED) $(JSONNET_SRC) $(JSONNET_VENDOR) $(GOJSONTOYAML) $(METRICS_JSON) 
	rm -rf manifests
	mkdir -p manifests/{benchmark,client}
	$(JSONNET_LOCAL_OR_INSTALLED) jsonnet/benchmark.jsonnet -J jsonnet/vendor -m manifests/benchmark --tla-code metrics="$$(cat $(METRICS_JSON))"
	$(JSONNET_LOCAL_OR_INSTALLED) jsonnet/client.jsonnet -J jsonnet/vendor -m manifests/client
	@for f in $$(find manifests -type f); do\
	    cat $$f | $(GOJSONTOYAML) > $$f.yaml && rm $$f;\
	done

benchmark.pdf: $(BENCHMARK_RESULTS)
	find ./benchmark -type f -name '*.json' -print0 | xargs -l -0 python3 test/plot.py && gs -dBATCH -dNOPAUSE -q -sDEVICE=pdfwrite -sOutputFile=$@ benchmark/*.pdf


##############
# Formatting #
##############

.PHONY: lint
lint:  $(BIN_DIR) $(GOLANGCI_LINT)
	# Check .golangci.yml for configuration
	GOLANGCI_LINT_CACHE=$(CACHE_DIR)/golangci-lint $(GOLANGCI_LINT) run --fix -c .golangci.yml

.PHONY: format
format: go-fmt jsonnet-fmt

.PHONY: go-fmt
go-fmt:
	go fmt $(PKGS)

.PHONY: jsonnet-fmt
jsonnet-fmt: $(JSONNETFMT_LOCAL_OR_INSTALLED)
	$(JSONNETFMT_LOCAL_OR_INSTALLED) $(JSONNET_SRC) -i

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

test-format:
	make --always-make format && git diff --exit-code

test-integration: build | $(THANOS_BIN) $(UP) $(MEMCACHED_BIN) $(PROMETHEUS_BIN)
	@echo "Running integration tests: V2"
	PATH=$$PATH:$$(pwd)/$(BIN_DIR) LD_LIBRARY_PATH=$$LD_LIBRARY_PATH:$$(pwd)/$(LIB_DIR) ./test/integration-v2.sh

test-benchmark: build $(GOJSONTOYAML)
	# Allow the image to be overridden when running in CI.
	# The CI_TELEMETER_IMAGE environment variable is injected by the
	# ci-operator. Check the configuration of the job in
	# https://github.com/openshift/release.
	if [ -n "$$CI_TELEMETER_IMAGE" ]; then \
	    echo "Overriding telemeter image to $${CI_TELEMETER_IMAGE}"; \
	    f=$$(mktemp) && cat ./manifests/benchmark/statefulSetTelemeterServer.yaml | $(GOJSONTOYAML) --yamltojson | jq '.spec.template.spec.containers[].image="'"$${CI_TELEMETER_IMAGE}"'"' | $(GOJSONTOYAML) > $$f && mv $$f ./manifests/benchmark/statefulSetTelemeterServer.yaml; \
	fi
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

dependencies: $(JB) $(THANOS_BIN) $(UP) $(EMBEDMD) $(GOJSONTOYAML) | $(PROMETHEUS_BIN) $(GOLANGCI_LINT) $(MEMCACHED_BIN)

$(BIN_DIR):
	mkdir -p $@

$(LIB_DIR):
	mkdir -p $@

$(MEMCACHED_BIN): | $(BIN_DIR) $(LIB_DIR)
	@echo "Downloading Memcached"
	curl -L https://archive.org/download/archlinux_pkg_libevent/libevent-2.1.10-1-x86_64.pkg.tar.xz | tar --strip-components=2 --xz -xf - -C $(LIB_DIR) usr/lib
	curl -L https://archive.org/download/archlinux_pkg_memcached/memcached-1.5.8-1-x86_64.pkg.tar.xz | tar --strip-components=2 --xz -xf - -C $(BIN_DIR) usr/bin/memcached

$(PROMETHEUS_BIN): $(BIN_DIR)
	@echo "Downloading Prometheus"
	curl -L "https://github.com/prometheus/prometheus/releases/download/v2.37.0/prometheus-2.37.0.$$(go env GOOS)-$$(go env GOARCH).tar.gz" | tar --strip-components=1 -xzf - -C $(BIN_DIR)

$(THANOS_BIN): $(BIN_DIR)
	@echo "Downloading Thanos"
	curl -L "https://github.com/thanos-io/thanos/releases/download/v0.27.0/thanos-0.27.0.$$(go env GOOS)-$$(go env GOARCH).tar.gz" | tar --strip-components=1 -xzf - -C $(BIN_DIR)
