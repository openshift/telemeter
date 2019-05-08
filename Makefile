.PHONY: all build image check test-generate test-integration test-benchmark vendor dependencies manifests

BIN=bin
GOLANGCI_LINT_BIN=$(BIN)/golangci-lint
EMBEDMD_BIN=$(GOPATH)/bin/embedmd
GOJSONTOYAML_BIN=$(GOPATH)/bin/gojsontoyaml
# We need jsonnet on CI; here we default to the user's installed jsonnet binary; if nothing is installed, then install go-jsonnet.
JSONNET_BIN=$(if $(shell which jsonnet 2>/dev/null),$(shell which jsonnet 2>/dev/null),$(GOPATH)/bin/jsonnet)
JB_BIN=$(GOPATH)/bin/jb
JSONNET_SRC=$(shell find ./jsonnet -type f)
BENCHMARK_RESULTS=$(shell find ./benchmark -type f -name '*.json')
JSONNET_VENDOR=jsonnet/jsonnetfile.lock.json jsonnet/vendor
DOCS=$(shell grep -rlF [embedmd] docs)

all: build manifests $(DOCS)

build:
	go build ./cmd/telemeter-client
	go build ./cmd/telemeter-server
	go build ./cmd/authorization-server
	go build ./cmd/telemeter-benchmark

image:
	imagebuilder -t openshift/telemeter:latest .

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

test-benchmark: build
	./test/benchmark.sh

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

vendor:
	glide update -v --skip-test

manifests: $(JSONNET_SRC) $(JSONNET_VENDOR) $(JSONNET_BIN) $(GOJSONTOYAML_BIN)
	rm -rf manifests
	mkdir -p manifests/{benchmark,client,server,prometheus}
	$(JSONNET_BIN) jsonnet/benchmark.jsonnet -J jsonnet/vendor -m manifests/benchmark
	$(JSONNET_BIN) jsonnet/client.jsonnet -J jsonnet/vendor -m manifests/client
	$(JSONNET_BIN) jsonnet/server.jsonnet -J jsonnet/vendor -m manifests/server
	$(JSONNET_BIN) jsonnet/prometheus.jsonnet -J jsonnet/vendor -m manifests/prometheus
	@for f in $$(find manifests -type f); do\
	    cat $$f | $(GOJSONTOYAML_BIN) > $$f.yaml && rm $$f;\
	done

benchmark.pdf: $(BENCHMARK_RESULTS)
	find ./benchmark -type f -name '*.json' -print0 | xargs -l -0 python3 test/plot.py && gs -dBATCH -dNOPAUSE -q -sDEVICE=pdfwrite -sOutputFile=$@ benchmark/*.pdf

$(JSONNET_VENDOR): jsonnet/jsonnetfile.json $(JB_BIN)
	cd jsonnet && jb install

dependencies: $(JB_BIN) $(JSONNET_BIN) $(GOLANGCI_LINT_BIN)

$(JB_BIN):
	go get -u github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb

$(JSONNET_BIN):
	go get -u -d github.com/google/go-jsonnet/cmd/jsonnet
	cd $(GOPATH)/src/github.com/google/go-jsonnet && git checkout v0.12.1 && git submodule update && go install -a ./jsonnet

$(GOLANGCI_LINT_BIN):
	curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | sh -s -- -b $(BIN) v1.10.2

$(EMBEDMD_BIN):
	go get -u github.com/campoy/embedmd

$(GOJSONTOYAML_BIN):
	go get -u github.com/brancz/gojsontoyaml
