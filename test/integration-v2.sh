#!/bin/bash

# Runs a semi-realistic integration test with one producer generating metrics,
# a telemeter server, a stub authorization server, a memcached instance,
# a thanos receive for ingestion, and a thanos query for querying the metrics.

set -euo pipefail

trap 'kill $(jobs -p); exit 1' EXIT

ss -tlpn

( ./authorization-server localhost:9001 ./test/tokens.json ) &

( memcached ) &

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
up \
    --endpoint=http://127.0.0.1:9003/metrics/v1/receive \
    --period=1s \
    --name cluster_installer \
    --labels '_id="test"' \
    --token="$(echo '{"authorization_token":"a","cluster_id":"test"}' | base64)"
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

sleep 1

retries=100
while true; do 
  if [[ "${retries}" -lt 0 ]]; then
    echo "error: Did not successfully retrieve cluster metrics from the local Thanos query server" 1>&2
    exit 1
  fi
  # verify we scrape metrics from the test cluster and give it _id test
  if [[ "$( curl http://localhost:9008/api/v1/query --data-urlencode 'query=count({_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  break
done
echo "tests: ok"
exit 1

for i in `jobs -p`; do wait $i; done
