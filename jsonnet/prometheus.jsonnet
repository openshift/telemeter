local t = (import 'prometheus-telemeter/prometheus-telemeter.libsonnet');

{ [name]: t.prometheus[name] for name in std.objectFields(t.prometheus) }
