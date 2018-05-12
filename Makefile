build:
	go build ./cmd/telemeter-client
	go build ./cmd/telemeter-server
.PHONY: build

vendor:
	glide update -v --skip-test
.PHONY: vendor