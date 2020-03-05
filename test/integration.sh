#!/bin/bash

# Runs a semi-realistic integration test with two servers, a stub authorization server, a 
# prometheus that scrapes from them, and a single client that fetches "cluster" metrics.
# If no arguments are passed an integration test scenario is run. Otherwise $1 becomes 
# the upstream prometheus server to test against and $2 is an optional bearer token to 
# authenticate the request.

set -euo pipefail

result=1
trap 'kill $(jobs -p); exit $result' EXIT

( ./authorization-server localhost:9001 ./test/tokens.json ) &

( prometheus --config.file=./test/prom-local.conf --web.listen-address=localhost:9090 "--storage.tsdb.path=$(mktemp -d)" --log.level=warn ) &

( 
  sleep 5
  exec ./telemeter-client \
    --from "http://localhost:9090" \
    --to "http://localhost:9003" \
    --id "test" \
    --to-token a \
    --interval 15s \
    --anonymize-labels "instance" --anonymize-salt "a-unique-value" \
    --rename ALERTS=alerts --rename openshift_build_info=build_info --rename scrape_samples_scraped=scraped \
    --match '{__name__="ALERTS",alertstate="firing"}' \
    --match '{__name__="scrape_samples_scraped"}'
) &

( 
./telemeter-server \
    --ratelimit=15s \
    --authorize http://localhost:9001 \
    --shared-key=test/test.key \
    --listen localhost:9003 \
    --listen-internal localhost:9004 \
    --whitelist '{_id="test"}' \
    --elide-label '_elide' \
    --forward-url=http://localhost:9005/api/v1/receive \
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

sleep 1

retries=100
while true; do
  if [[ "${retries}" -lt 0 ]]; then
    echo "error: Did not successfully retrieve cluster metrics from the local Prometheus server" 1>&2
    exit 1
  fi
  # verify we scrape metrics from the test cluster and give it _id test
  if [[ "$( curl http://localhost:9008/api/v1/query --data-urlencode 'query=count({_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  # verify we rename scrape_samples_scraped to scraped
  if [[ "$( curl http://localhost:9008/api/v1/query --data-urlencode 'query=count(scraped{_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  # verify we got alerts as remapped from ALERTS
  if [[ "$( curl http://localhost:9008/api/v1/query --data-urlencode 'query=count(alerts{_id="test"})' -G 2>/dev/null | python3 -c 'import sys, json; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])' 2>/dev/null )" -eq 0 ]]; then
    retries=$((retries-1))
    sleep 1
    continue
  fi
  # verify we don't get elided labels
  if [[ "$( curl http://localhost:9008/api/v1/query --data-urlencode 'query=count(alerts{_id="test",_elide=~".+"})' -G 2>/dev/null | python3 -c 'import sys, json; print(len(json.load(sys.stdin)["data"]["result"]))' 2>/dev/null )" -gt 0 ]]; then
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
