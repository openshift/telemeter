local t = (import 'telemeter-client/telemeter-client.libsonnet');

{ [name]: t.telemeterClient[name] for name in std.objectFields(t.telemeterClient) }
