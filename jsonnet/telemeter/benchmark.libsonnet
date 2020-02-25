local config = {
  _config+:: {
    namespace: 'telemeter-benchmark',
  },
};

// We have to import the entire Prometheus Operator dependency first
// before selecting the fields we want so that the config overrides
// the settings in the import.
local po = (import 'prometheus-operator/prometheus-operator.libsonnet') + config;

(import 'benchmark/kubernetes.libsonnet') + config + {
  prometheusOperator+:: {
    clusterRoleBinding: po.prometheusOperator.clusterRoleBinding { metadata+: { name: 'telemeter-benchmark' } },
    serviceAccount: po.prometheusOperator.serviceAccount,
    deployment: po.prometheusOperator.deployment {
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
}
