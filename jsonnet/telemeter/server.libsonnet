local list = import 'lib/list.libsonnet';

(import 'server/kubernetes.libsonnet') + {
  local ts = super.telemeterServer,
  telemeterServer+:: {
    list: list.asList('telemeter', ts, [])
          + list.withAuthorizeURL($._config)
          + list.withNamespace($._config)
          + list.withServerImage($._config)
          + list.withResourceRequestsAndLimits('telemeter-server', $._config.telemeterServer.resourceRequests, $._config.telemeterServer.resourceLimits)
  },
} + {
  _config+:: {
    jobs+: {
      TelemeterServer: 'job="telemeter-server"',
    },
    telemeterServer+: {
      whitelist+: (import 'metrics.jsonnet'),
      elideLabels+: [
        'prometheus_replica',
      ],
    },
  },
}
