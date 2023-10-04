#!/usr/bin/env sh

docker run --rm -p 4317:4317 -p 4318:4318 -v $(pwd)/hack/otelcol.yaml:/config.yaml otel/opentelemetry-collector --config /config.yaml
