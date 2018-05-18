#!/bin/bash

# Pass $1 as a Prometheus federation endpoint and $2 as an optional token.

trap 'kill $(jobs -p); exit 0' EXIT

( ./telemeter-client --to "http://localhost:9003/upload" --to-auth "http://localhost:9003/authorize?cluster=b" --to-token=b --from "$1" --from-token="${2-}" --interval 30s --match '{__name__="up"}' --match '{__name__="openshift_build_info"}' --match '{__name__="machine_cpu_cores"}' --match '{__name__="machine_memory_bytes"}' ) &
( ./telemeter-server "--storage-dir=$(mktemp -d)" --listen localhost:9003 --listen-internal localhost:9004 ) &

( prometheus --config.file=./test/prom-local.conf --web.listen-address=localhost:9005 "--storage.tsdb.path=$(mktemp -d)" --log.level=debug ) &

sleep 1

if ! curl http://localhost:9002/healthz &>/dev/null; then
  echo "error: client did not return healthy"
  exit 1
fi
if ! curl http://localhost:9003/healthz &>/dev/null; then
  echo "error: server external did not return healthy"
  exit 1
fi
if ! curl http://localhost:9004/healthz &>/dev/null; then
  echo "error: server internal did not return healthy"
  exit 1
fi
# Convert to go
#if [[ "$( curl http://localhost:9005/api/v1/query --data-urlencode 'query=count({cluster="b"})' -G 2>/dev/null | jq -r '.data.result[0].value[1]' )" -eq 0 ]]; then
#  exit 1
#fi
echo "tests: ok"

for i in `jobs -p`; do wait $i; done