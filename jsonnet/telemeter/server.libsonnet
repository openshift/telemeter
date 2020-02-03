local list = import 'lib/list.libsonnet';

(import 'server/kubernetes.libsonnet') + {
  local ts = super.telemeterServer,
  local m = super.memcached,
  local tsList = list.asList('telemeter', ts, [])
                 + list.withAuthorizeURL($._config)
                 + list.withNamespace($._config)
                 + list.withServerImage($._config)
                 + list.withResourceRequestsAndLimits('telemeter-server', $._config.telemeterServer.resourceRequests, $._config.telemeterServer.resourceLimits),
  local mList = list.asList('memcached', m, [
                  {
                    name: 'MEMCACHED_IMAGE',
                    value: m.images.memcached,
                  },
                  {
                    name: 'MEMCACHED_IMAGE_TAG',
                    value: m.tags.memcached,
                  },
                  {
                    name: 'MEMCACHED_EXPORTER_IMAGE',
                    value: m.images.exporter,
                  },
                  {
                    name: 'MEMCACHED_EXPORTER_IMAGE_TAG',
                    value: m.tags.exporter,
                  },
                ])
                + list.withResourceRequestsAndLimits('memcached', $.memcached.resourceRequests, $.memcached.resourceLimits)
                + list.withNamespace($._config),

  telemeterServer+:: {
    list: list.asList('telemeter', {}, []) + {
      objects:
        tsList.objects +
        mList.objects,

      parameters:
        tsList.parameters +
        mList.parameters,
    },
  },
} + {
  _config+:: {
    jobs+: {
      TelemeterServer: 'job="telemeter-server"',
    },
    telemeterServer+: {
      whitelist+: [],
      elideLabels+: [
        'prometheus_replica',
      ],
    },
  },
}
