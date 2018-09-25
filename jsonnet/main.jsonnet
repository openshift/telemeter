local t = (import 'telemeter-client/telemeter-client.libsonnet');

{ ['telemeter-client-' + name]: t.telemeterClient[name] for name in std.objectFields(t.telemeterClient) }
