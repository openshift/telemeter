local list = import 'kubernetes/list.libsonnet';

(import 'kubernetes/kubernetes.libsonnet') + {
  local p = super.prometheus,
  prometheus+:: {
    list: list.asList('prometheus-telemeter', p, []) + list.withImage($._config) + list.withNamespace($._config),
  },
} + {
  _config+:: {
    jobs+: {
      PrometheusTelemeter: 'job="prometheus-telemeter"',
    },
  },
}
