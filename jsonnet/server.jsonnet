local t = (import 'telemeter/server.libsonnet');

{ [name]: t.telemeterServer[name] for name in std.objectFields(t.telemeterServer) } +
{ [name + 'Memcached']: t.memcached[name] for name in std.objectFields(t.memcached) if t.memcached.replicas > 0 }
