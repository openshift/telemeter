local list = import 'lib/list.libsonnet';

(import 'server/kubernetes.libsonnet') + {
  local ts = super.rhelemeterServer,
  local tsList = list.asList('rhelemeter', ts, [])
                 + list.withNamespace($._config)
                 + list.withServerImage($._config)
                 + list.withResourceRequestsAndLimits('rhelemeter-server', $._config.rhelemeterServer.resourceRequests, $._config.rhelemeterServer.resourceLimits),

  rhelemeterServer+:: {
    list: list.asList('rhelemeter', {}, []) + {
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
      RhelemeterServer: 'job="rhelemeter-server"',
    },
    rhelemeterServer+: {
      whitelist+: [],
      elideLabels+: [],
    },
  },
}
