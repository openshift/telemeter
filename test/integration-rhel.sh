#!/bin/bash

# Runs a semi-realistic integration test with one producer generating metrics,
# a telemeter server, a stub authorization server, a memcached instance,
# a thanos receive for ingestion, and a thanos query for querying the metrics.

set -euo pipefail

result=1
trap 'kill $(jobs -p); exit $result' EXIT

(
  thanos receive \
    --tsdb.path="$(mktemp -d)" \
    --remote-write.address=127.0.0.1:9105 \
    --grpc-address=127.0.0.1:9106 \
    --http-address=127.0.0.1:9116 \
    --log.level=warn \
    --receive.default-tenant-id="FB870BF3-9F3A-44FF-9BF7-D7A047A52F43"
) &

(
  thanos query \
    --grpc-address=127.0.0.1:9107 \
    --http-address=127.0.0.1:9108 \
    --log.level=warn \
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
  ./telemeter-rhel-server \
    --listen localhost:9103 \
    --listen-internal localhost:9104 \
    --forward-url=http://localhost:9105/api/v1/receive \
    --tls-key cmd/telemeter-rhel-server/testdata/server-private-key.pem \
    --tls-crt cmd/telemeter-rhel-server/testdata/server-cert.pem \
    --tls-ca-crt cmd/telemeter-rhel-server/testdata/ca-cert.pem \
    --log-level=warn \
    -v
) &

echo "up: waiting for dependencies to come up..."

until curl --output /dev/null --silent --connect-timeout 5 http://localhost:9103/healthz/ready; do
  printf '.'
  sleep 1
done

client_key=cmd/telemeter-rhel-server/testdata/client-private-key.pem
client_crt=cmd/telemeter-rhel-server/testdata/client-cert.pem
ca_crt=cmd/telemeter-rhel-server/testdata/ca-cert.pem
write_url=https://localhost:9103/metrics/v1/receive

if
  curl --cert "$client_crt" \
    --key "$client_key" \
    --cacert "$ca_crt" \
   "$write_url" \
   -o /dev/null -s -S
then
  echo "---> mTLS test: ok"
fi

if
  up \
    --endpoint-type=metrics \
    --endpoint-write=https://127.0.0.1:9103/metrics/v1/receive \
    --endpoint-read=http://127.0.0.1:9108/api/v1/query \
    --period=500ms \
    --initial-query-delay=250ms \
    --threshold=1 \
    --latency=10s \
    --duration=10s \
    --log.level=warn \
    --name cluster_installer \
    --labels '_id="test"' \
    --tls-ca-file "$ca_crt" \
    --tls-client-cert-file "$client_crt" \
    --tls-client-private-key-file "$client_key" 
then
  result=0
  echo "---> tests: ok"
  exit 0
fi

sleep 60

echo "tests: failed" 1>&2
result=1
exit 1
