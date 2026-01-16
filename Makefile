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
GOLANGCI_LINT_BIN=$(BIN_DIR)/golangci-lint
GOLANGCI_LINT_VERSION=v2.0.2
THANOS_BIN=$(BIN_DIR)/thanos
OTELCOL_BIN=$(BIN_DIR)/otelcol
PROMETHEUS_BIN=$(BIN_DIR)/prometheus
PROMTOOL_BIN=$(BIN_DIR)/promtool
GOJSONTOYAML_BIN=$(BIN_DIR)/gojsontoyaml
JSONNET_BIN?=$(BIN_DIR)/jsonnet
JSONNETFMT_BIN?=$(BIN_DIR)/jsonnetfmt
# We need jsonnet on CI; here we default to the user's installed jsonnet binary; if nothing is installed, then install go-jsonnet.
JSONNET_LOCAL_OR_INSTALLED=$(if $(shell which jsonnet 2>/dev/null),$(shell which jsonnet 2>/dev/null),$(JSONNET_BIN))
JSONNETFMT_LOCAL_OR_INSTALLED=$(if $(shell which jsonnetfmt 2>/dev/null),$(shell which jsonnetfmt 2>/dev/null),$(JSONNETFMT_BIN))
JB_BIN=$(BIN_DIR)/jb
JSONNET_SRC=$(shell find ./jsonnet -path ./jsonnet/vendor -prune -false -o -type f \( -name "*.jsonnet" -o -name "*.libsonnet" \))
BENCHMARK_RESULTS=$(shell find ./telemeter-benchmark -type f -name '*.json')
BENCHMARK_GOAL?=5000
JSONNET_VENDOR=jsonnet/jsonnetfile.lock.json jsonnet/vendor
TEST_E2E_CERTS_DIR=./cmd/rhelemeter-server/testdata
PROMETHEUS_VERSION=2.49.1

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
all: build manifests

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
	go build ./cmd/rhelemeter-server

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

.PHONY: generate-in-docker
generate-in-docker:
	$(CONTAINER_CMD) $(MAKE) $(MFLAGS) generate

$(JSONNET_VENDOR): $(JB_BIN) jsonnet/jsonnetfile.json
	cd jsonnet && $(JB_BIN) install

-$(METRICS_JSON):
	curl -L https://raw.githubusercontent.com/openshift/cluster-monitoring-operator/844e7afabfcfa4162c716ea18cd8e2d010789de1/manifests/0000_50_cluster_monitoring_operator_04-config.yaml | \
		$(GOJSONTOYAML_BIN) --yamltojson | jq -r '.data."metrics.yaml"' | $(GOJSONTOYAML_BIN) --yamltojson | jq -r '.matches' > $@

manifests: $(JSONNET_LOCAL_OR_INSTALLED) $(JSONNET_SRC) $(JSONNET_VENDOR) $(GOJSONTOYAML_BIN) $(METRICS_JSON)
	rm -rf manifests
	mkdir -p manifests/{benchmark,client}
	$(JSONNET_LOCAL_OR_INSTALLED) jsonnet/benchmark.jsonnet -J jsonnet/vendor -m manifests/benchmark --tla-code metrics="$$(cat $(METRICS_JSON))"
	$(JSONNET_LOCAL_OR_INSTALLED) jsonnet/client.jsonnet -J jsonnet/vendor -m manifests/client
	@for f in $$(find manifests -type f); do\
	    cat $$f | $(GOJSONTOYAML_BIN) > $$f.yaml && rm $$f;\
	done

benchmark.pdf: $(BENCHMARK_RESULTS)
	find ./telemeter-benchmark -type f -name '*.json' -print0 | xargs -l -0 python3 test/plot.py && gs -dBATCH -dNOPAUSE -q -sDEVICE=pdfwrite -sOutputFile=$@ benchmark/*.pdf


##############
# Formatting #
##############

.PHONY: lint
lint: $(GOLANGCI_LINT_BIN)
	# Check .golangci.yml for configuration
	GOLANGCI_LINT_CACHE=$(CACHE_DIR)/golangci-lint $(GOLANGCI_LINT_BIN) run -c .golangci.yml

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
test: test-unit test-rules test-integration test-benchmark

.PHONY: test-unit
test-unit:
	go test -race -short $(PKGS) -count=1

tmp:
	mkdir tmp

tmp/rules.yaml: $(JSONNET_LOCAL_OR_INSTALLED) jsonnet/telemeter/rules.libsonnet tmp
	$(JSONNET_LOCAL_OR_INSTALLED) -e "(import 'jsonnet/telemeter/rules.libsonnet')['prometheus']['recordingrules']" > tmp/rules.yaml

.PHONY: check-rules
check-rules: $(PROMTOOL_BIN) tmp/rules.yaml
	rm -f tmp/"$@".out
	$(PROMTOOL_BIN) check rules tmp/rules.yaml | tee "tmp/$@.out"

.PHONY: test-rules
test-rules: check-rules
	$(PROMTOOL_BIN) test rules test/rulestests.yaml | tee "tmp/$@.out"

# TODO(paulfantom): remove this target after removing it from Prow.
test-generate:
	make --always-make && git diff --exit-code

test-format:
	make --always-make format && git diff --exit-code

test-integration: build | $(THANOS_BIN) $(PROMETHEUS_BIN)
	@echo "================================="
	@echo ">>> Running integration tests"
	@echo "================================="
	PATH=$$PATH:$$(pwd)/$(BIN_DIR) ./test/integration.sh 2>&1


test-benchmark: build $(GOJSONTOYAML_BIN)
	# Allow the image to be overridden when running in CI.
	# The CI_TELEMETER_IMAGE environment variable is injected by the
	# ci-operator. Check the configuration of the job in
	# https://github.com/openshift/release.
	if [ -n "$$CI_TELEMETER_IMAGE" ]; then \
	    echo "Overriding telemeter image to $${CI_TELEMETER_IMAGE}"; \
	    f=$$(mktemp) && cat ./manifests/benchmark/statefulSetTelemeterServer.yaml | $(GOJSONTOYAML_BIN) --yamltojson | jq '.spec.template.spec.containers[].image="'"$${CI_TELEMETER_IMAGE}"'"' | $(GOJSONTOYAML_BIN) > $$f && mv $$f ./manifests/benchmark/statefulSetTelemeterServer.yaml; \
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

.PHONY: test-generate-e2e-certs
test-rhel-generate-e2e-certs:
	./test/generate-e2e-certs.sh $(TEST_E2E_CERTS_DIR)

test-rhelemeter-integration: build | $(THANOS_BIN) $(PROMETHEUS_BIN) $(OTELCOL_BIN)
	@echo "Running rhelemeter integration tests"
	PATH=$$PATH:$$(pwd)/$(BIN_DIR) LD_LIBRARY_PATH=$$LD_LIBRARY_PATH:$$(pwd)/$(LIB_DIR) ./test/integration-rhelemeter.sh

############
# Binaries #
############

dependencies: $(JB_BIN) $(THANOS_BIN) $(GOJSONTOYAML_BIN) | $(PROMETHEUS_BIN) $(GOLANGCI_LINT_BIN)

$(BIN_DIR):
	mkdir -p $@

$(LIB_DIR):
	mkdir -p $@

PROM_BIN := $(shell $(BIN_DIR)/prometheus --version 2>/dev/null)
$(PROMETHEUS_BIN): $(BIN_DIR)
ifdef PROM_BIN
	@echo "Found version of Prometheus"
else
	@echo "Downloading Prometheus"
	curl -L "https://github.com/prometheus/prometheus/releases/download/v$(PROMETHEUS_VERSION)/prometheus-$(PROMETHEUS_VERSION).$$(go env GOOS)-$$(go env GOARCH).tar.gz" | tar --strip-components=1 -xzf - -C $(BIN_DIR)
endif

$(PROMTOOL_BIN): $(PROMETHEUS_BIN)

$(OTELCOL_BIN): $(BIN_DIR)
	@echo "Downloading the OTEL collector"
	curl -L "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v0.96.0/otelcol_0.96.0_$$(go env GOOS)_$$(go env GOARCH).tar.gz" | tar -xzf - -C $(BIN_DIR)

$(GOLANGCI_LINT_BIN): $(BIN_DIR)
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh \
		| sed -e '/install -d/d' \
		| sh -s -- -b $(BIN_DIR) $(GOLANGCI_LINT_VERSION)

$(THANOS_BIN): $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install -mod=readonly -modfile=tools/go.mod github.com/thanos-io/thanos/cmd/thanos

$(JSONNET_BIN): $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install -mod=readonly -modfile=tools/go.mod github.com/google/go-jsonnet/cmd/jsonnet

$(JSONNETFMT_BIN): $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install -mod=readonly -modfile=tools/go.mod github.com/google/go-jsonnet/cmd/jsonnetfmt

$(JB_BIN): $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install -mod=readonly -modfile=tools/go.mod github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb

$(GOJSONTOYAML_BIN): $(BIN_DIR)
	GOBIN=$(BIN_DIR) go install -mod=readonly -modfile=tools/go.mod github.com/brancz/gojsontoyaml
