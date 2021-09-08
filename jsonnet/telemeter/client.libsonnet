(import 'client/kubernetes.libsonnet') + {
  _config+:: {
    jobs+: {
      TelemeterClient: 'job="telemeter-client"',
    },
    telemeterClient+: {
      matchRules+: [],
    },
    commonLabels+:: {
      'app.kubernetes.io/component': 'telemetry-metrics-collector',
      'app.kubernetes.io/name': 'telemeter-client',
    },
  },
}
