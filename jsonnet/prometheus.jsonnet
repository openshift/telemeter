local t = (import 'telemeter/prometheus.libsonnet');

{ [name]: t.prometheus[name] for name in std.objectFields(t.prometheus) }
