function(metrics=[])
  local t = (import 'telemeter/benchmark.libsonnet') { config+: { telemeterServer+: { whitelist+: metrics } } };
  { [name + 'TelemeterServer']: t.telemeterServer[name] for name in std.objectFields(t.telemeterServer) } +
  { [name + 'ThanosReceiveController']: t.thanosReceiveController[name] for name in std.objectFields(t.thanosReceiveController) } +
  {
    [name + 'ThanosReceive' + hashring]: t.receivers[hashring][name]
    for hashring in std.objectFields(t.receivers)
    for name in std.objectFields(t.receivers[hashring])
  } +
  { [name + 'ThanosQuery']: t.query[name] for name in std.objectFields(t.query) }
