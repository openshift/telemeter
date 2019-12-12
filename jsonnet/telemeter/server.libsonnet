local list = import 'lib/list.libsonnet';

(import 'server/kubernetes.libsonnet') + {
  local ts = super.telemeterServer,
  local m = super.memcached,
  telemeterServer+:: {
    list: list.asList('telemeter', ts, [])
          + list.withAuthorizeURL($._config)
          + list.withNamespace($._config)
          + list.withServerImage($._config)
          + list.withResourceRequestsAndLimits('telemeter-server', $._config.telemeterServer.resourceRequests, $._config.telemeterServer.resourceLimits),
  },
  memcached+:: {
    service+: {
      metadata+: {
        namespace: '${NAMESPACE}',
      },
    },
    list: list.asList('memcached', m, [
            {
              name: 'MEMCACHED_IMAGE',
              value: m.image,
            },
          ])
          + list.withNamespace($._config),
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
