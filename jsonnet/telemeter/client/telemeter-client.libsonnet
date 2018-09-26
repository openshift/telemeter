(import 'kubernetes/kubernetes.libsonnet') + {
  _config+:: {
    jobs+: {
      TelemeterClient: 'job="telemeter-client"',
    },
  },
}
