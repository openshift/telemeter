#!/bin/bash

# Runs a semi-realistic integration test with one producer generating metrics,
# a telemeter server, a stub authorization server, a memcached instance,
# a thanos receive for ingestion, and a thanos query for querying the metrics.

set -euo pipefail

source .bingo/variables.env

result=1
trap 'kill $(jobs -p); exit $result' EXIT

(./authorization-server localhost:9101 ./test/tokens.json) &

(memcached -u "$(whoami)") &

(
  thanos receive \
    --tsdb.path="$(mktemp -d)" \
    --label "receive_replica=\"0\"" \
    --remote-write.address=127.0.0.1:9105 \
    --grpc-address=127.0.0.1:9106 \
    --http-address=127.0.0.1:9116 \
    --receive.local-endpoint 127.0.0.1:9106 \
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
    --elide-label '_elide' \
    --whitelist '{_id="test"}' \
    --whitelist '{__name__="alerts"}' \
    --whitelist '{__name__="scraped"}' \
    --whitelist '{__name__="build_info"}' \
    -v
) &

( 
  prometheus \
    --config.file=./test/prom-remote.conf \
    --web.listen-address=localhost:9090 \
    --storage.tsdb.path="$(mktemp -d)" \
    --log.level=debug
) &

echo "querying: waiting for dependencies to come up..."

until curl --output /dev/null --silent --fail http://localhost:9103/healthz/ready; do
  printf '.'
  sleep 1
done

until curl --output /dev/null --silent --fail http://localhost:9090/-/ready; do
  printf '.'
  sleep 1
done


sleep 40

retries=100
while true; do
  if [[ "${retries}" -lt 0 ]]; then
    echo "error: Did not successfully retrieve cluster metrics from the local Prometheus server" 1>&2
    exit 1
  fi
  # verify we scrape metrics from the test cluster and give it _id test
  if [[ "$( curl http://localhost:9108/api/v1/query --data-urlencode 'query=count({_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  # verify we rename scrape_samples_scraped to scraped
  if [[ "$( curl http://localhost:9108/api/v1/query --data-urlencode 'query=count(scraped{_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  # verify we got alerts as remapped from ALERTS
  if [[ "$( curl http://localhost:9108/api/v1/query --data-urlencode 'query=count(alerts{_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  # verify we don't get elided labels
  if [[ "$( curl http://localhost:9108/api/v1/query --data-urlencode 'query=count(alerts{_id="test",_elide=~".+"})' -G 2>/dev/null | python3 -c 'import sys, json; print(len(json.load(sys.stdin)["data"]["result"]))' 2>/dev/null )" -gt 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  break
done
echo "tests: ok"
result=0
exit 0

echo "tests: failed" 1>&2
result=1
exit 1
