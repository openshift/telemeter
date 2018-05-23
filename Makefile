build:
	go build ./cmd/telemeter-client
	go build ./cmd/telemeter-server
.PHONY: build

image:
	imagebuilder -t openshift/telemeter:latest .
.PHONY: image

vendor:
	glide update -v --skip-test
.PHONY: vendor