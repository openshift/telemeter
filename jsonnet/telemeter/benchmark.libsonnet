// We have to import the entire Prometheus Operator dependency first
// before selecting the fields we want so that the config overrides
// the settings in the import.
local b = (import 'prometheus-operator/prometheus-operator.libsonnet') +
          (import 'benchmark/kubernetes.libsonnet') + {
  _config+:: {
    namespace: 'telemeter-benchmark',
    telemeterServer+: {
      whitelist+: [],
    },
  },
};

{
  prometheusOperator+:: {
    clusterRoleBinding: b.prometheusOperator.clusterRoleBinding { metadata+: { name: 'telemeter-benchmark' } },
    serviceAccount: b.prometheusOperator.serviceAccount,
    deployment: b.prometheusOperator.deployment {
      spec+: {
        template+: {
          spec+: {
            containers: [
              c {
                securityContext:: super.securityContext,
              }
              for c in super.containers
            ],
            securityContext:: super.securityContext,
          },
        },
      },
    },
  },
  telemeterServer+:: b.telemeterServer,
  prometheus+:: b.prometheus,
}
