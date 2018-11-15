local list = import 'lib/list.libsonnet';

(import 'prometheus/kubernetes.libsonnet') + {
  local p = super.prometheus,
  prometheus+:: {
    list: list.asList('prometheus-telemeter', p, [
            // Saasherder requires an `IMAGE_TAG` variable
            // to be defined in the template, but we don't
            // want to use the generated build tag for Prometheus.
            // Use this placeholder until Saasherder fixes
            // their semantics.
            // TODO(squat): eliminate this once Saasherder improves.
            { name: 'IMAGE_TAG', value: '' },
          ])
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
