#!/bin/bash

# Pass $1 as a Prometheus federation endpoint and $2 as an optional token.

trap 'kill $(jobs -p); exit 0' EXIT

( ./telemeter-client --to "http://localhost:9003/upload" --to-auth "http://localhost:9003/authorize?cluster=b" --to-token=b --from "$1" --from-token="${2-}" ) &
( ./telemeter-server --listen localhost:9003 --listen-internal localhost:9004 ) &

sleep 3
out="$( curl http://localhost:9004/federate -s 2>/dev/null )"
if [[ "$( echo "${out}" | wc -l )" -lt 2 ]]; then
  echo "${out}"
  echo "error: expected to see federated data" &1>2
  exit 1
fi
if ! curl http://localhost:9002/healthz &>/dev/null; then
  exit 1
fi
if ! curl http://localhost:9003/healthz &>/dev/null; then
  exit 1
fi
if ! curl http://localhost:9004/healthz &>/dev/null; then
  exit 1
fi
echo "tests: ok"

for i in `jobs -p`; do wait $i; done