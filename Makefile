build:
	go build ./cmd/telemeter-client
	go build ./cmd/telemeter-server
	go build ./cmd/authorization-server
.PHONY: build

image:
	imagebuilder -t openshift/telemeter:latest .
.PHONY: image

check:
	go test -race ./...
.PHONY: check

test-integration: build
	./test/integration.sh
.PHONY: test-integration

vendor:
	glide update -v --skip-test
.PHONY: vendor