#!/bin/bash

set -exv

IMAGE_BUILD="telemeter-build"
DOCKERFILE_BUILD="dockerfiles/Dockerfile.build"

IMAGE_DEPLOY="quay.io/app-sre/app-interface"
DOCKERFILE_DEPLOY="dockerfiles/Dockerfile.deploy"
IMAGE_TAG=$(git rev-parse --short=7 HEAD)

BINARIES="telemeter-client telemeter-server authorization-server"

mkdir -p tmp

docker build -f $DOCKERFILE_BUILD -t $IMAGE_BUILD .

docker run --rm $IMAGE_BUILD \
    tar -C /go/src/github.com/openshift/telemeter/ -cf - $BINARIES | \
    tar -C tmp -xf -

docker build -f $DOCKERFILE_DEPLOY -t "${IMAGE}:${IMAGE_TAG}" .

if [[ -n "$QUAY_USER" && -n "$QUAY_TOKEN" ]]; then
    DOCKER_CONF="$PWD/.docker"
    mkdir -p "$DOCKER_CONF"
    docker --config="$DOCKER_CONF" login -u="$QUAY_USER" -p="$QUAY_TOKEN" quay.io
    docker --config="$DOCKER_CONF" push "${IMAGE}:${IMAGE_TAG}"
fi
