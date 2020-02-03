(import 'client/kubernetes.libsonnet') + {
  _config+:: {
    jobs+: {
      TelemeterClient: 'job="telemeter-client"',
    },
    telemeterClient+: {
      matchRules+: [],
    },
  },
}
