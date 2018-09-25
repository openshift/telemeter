(import 'kubernetes/kubernetes.libsonnet') + {
  _config+:: {
    namespace: 'default',

    telemeterClientSelector: 'job="telemeter-client"',

    jobs: {
      TelemeterClient: $._config.telemeterClientSelector,
    },
  },
}
