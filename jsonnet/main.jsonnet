local t = (import 'telemeter-client/telemeter-client.libsonnet') + {
  _config+:: {
    namespace: 'openshift-monitoring',
  },
};

{ ['telemeter-client-' + name]: t.telemeterClient[name] for name in std.objectFields(t.telemeterClient) }
