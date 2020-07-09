#!/bin/bash

# Runs a semi-realistic integration test with one producer generating metrics,
# a telemeter server, a stub authorization server, a memcached instance,
# a thanos receive for ingestion, and a thanos query for querying the metrics.

set -euo pipefail

result=1
trap 'kill $(jobs -p); exit $result' EXIT

(./authorization-server localhost:9101 ./test/tokens.json) &

(memcached -u "$(whoami)") &

(
  thanos receive \
    --tsdb.path="$(mktemp -d)" \
    --remote-write.address=127.0.0.1:9105 \
    --grpc-address=127.0.0.1:9106 \
    --http-address=127.0.0.1:9116 \
    --receive.default-tenant-id="FB870BF3-9F3A-44FF-9BF7-D7A047A52F43"
) &

(
  thanos query \
    --grpc-address=127.0.0.1:9107 \
    --http-address=127.0.0.1:9108 \
    --store=127.0.0.1:9106
) &

echo "telemeter: waiting for dependencies to come up..."
sleep 10

until curl --output /dev/null --silent --fail http://localhost:9116/-/ready; do
  printf '.'
  sleep 1
done

until curl --output /dev/null --silent --fail http://localhost:9108/-/ready; do
  printf '.'
  sleep 1
done

(
  ./telemeter-server \
    --authorize http://localhost:9101 \
    --listen localhost:9103 \
    --listen-internal localhost:9104 \
    --forward-url=http://localhost:9105/api/v1/receive \
    --memcached=localhost:11211 \
    -v
) &

echo "up: waiting for dependencies to come up..."

until curl --output /dev/null --silent --fail http://localhost:9103/healthz/ready; do
  printf '.'
  sleep 1
done

if
  up \
    --endpoint-type=metrics \
    --endpoint-write=http://127.0.0.1:9103/metrics/v1/receive \
    --endpoint-read=http://127.0.0.1:9108/api/v1/query \
    --period=500ms \
    --initial-query-delay=250ms \
    --threshold=1 \
    --latency=10s \
    --duration=10s \
    --log.level=debug \
    --name cluster_installer \
    --labels '_id="test"' \
    --token="$(echo '{"authorization_token":"a","cluster_id":"test"}' | base64)"
then
  result=0
  echo "tests: ok"
  exit 0
fi

echo "tests: failed" 1>&2
result=1
exit 1
