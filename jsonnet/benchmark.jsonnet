function(metrics=[])
  local t = (import 'telemeter/benchmark.libsonnet') { _config+: { telemeterServer+: { whitelist+: metrics } } };
  { [name + 'TelemeterServer']: t.telemeterServer[name] for name in std.objectFields(t.telemeterServer) } +
  { [name + 'PrometheusOperator']: t.prometheusOperator[name] for name in std.objectFields(t.prometheusOperator) } +
  { [name + 'Prometheus']: t.prometheus[name] for name in std.objectFields(t.prometheus) }
