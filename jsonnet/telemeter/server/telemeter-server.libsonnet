local list = import 'kubernetes/list.libsonnet';

(import 'kubernetes/kubernetes.libsonnet') + {
  local ts = super.telemeterServer,
  telemeterServer+:: {
    list: list.asList('telemeter', ts, []) + list.withImage($._config) + list.withAuthorizeURL($._config),
  },
} + {
  _config+:: {
    jobs+: {
      TelemeterServer: 'job="telemeter-server"',
    },
  },
}
