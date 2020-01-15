#!/bin/bash

# Runs a semi-realistic integration test with one producer generating metrics,
# a telemeter server, a stub authorization server, a memcached instance,
# a thanos receive for ingestion, and a thanos query for querying the metrics.

set -euo pipefail

result=1
trap 'kill $(jobs -p); exit $result' EXIT

( ./authorization-server localhost:9001 ./test/tokens.json ) &

( memcached -u "$(whoami)") &

( 
./telemeter-server \
    --ttl=24h \
    --authorize http://localhost:9001 \
    --listen localhost:9003 \
    --listen-internal localhost:9004 \
    --forward-url=http://localhost:9005/api/v1/receive \
    --memcached=localhost:11211 \
    -v
) &

(
thanos receive \
    --tsdb.path="$(mktemp -d)" \
    --remote-write.address=127.0.0.1:9005 \
    --grpc-address=127.0.0.1:9006
) &

(
thanos query \
    --grpc-address=127.0.0.1:9007 \
    --http-address=127.0.0.1:9008 \
    --store=127.0.0.1:9006
) &

echo "waiting for dependencies to come up..."
sleep 5

if up \
    --endpoint-write=http://127.0.0.1:9003/metrics/v1/receive \
    --endpoint-read=http://127.0.0.1:9008/api/v1/query \
    --period=500ms \
    --initial-query-delay=250ms \
    --threshold=1 \
    --latency=10s \
    --duration=10s \
    --log.level=debug \
    --name cluster_installer \
    --labels '_id="test"' \
    --token="$(echo '{"authorization_token":"a","cluster_id":"test"}' | base64)"; then
    result=0
    echo "tests: ok"
    exit 0
fi

echo "tests: failed" 1>&2
result=1
exit 1
