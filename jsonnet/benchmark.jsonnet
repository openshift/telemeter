local t = (import 'telemeter/benchmark.libsonnet');

{ [name + 'TelemeterBenchmark']: t.telemeterBenchmark[name] for name in std.objectFields(t.telemeterBenchmark) } +
{ [name + 'TelemeterServer']: t.telemeterServer[name] for name in std.objectFields(t.telemeterServer) } +
{ [name + 'PrometheusOperator']: t.prometheusOperator[name] for name in std.objectFields(t.prometheusOperator) } +
{ [name + 'Prometheus']: t.prometheus[name] for name in std.objectFields(t.prometheus) }
