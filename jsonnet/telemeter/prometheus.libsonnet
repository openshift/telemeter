local list = import 'lib/list.libsonnet';

(import 'prometheus/kubernetes.libsonnet') + {
  local p = super.prometheus,
  prometheus+:: {
    list: list.asList('prometheus-telemeter', p, [])
          + list.withNamespace($._config)
          + list.withPrometheusImage($._config),
  },
} + {
  _config+:: {
    jobs+: {
      PrometheusTelemeter: 'job="prometheus-telemeter"',
    },
  },
}
