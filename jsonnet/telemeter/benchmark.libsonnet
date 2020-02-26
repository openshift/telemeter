(import 'benchmark/kubernetes.libsonnet') {
  local b = self,
  config+:: {
    local defaultConfig = self,
    namespace: 'telemeter-benchmark',
    name: 'benchmark',
    thanosVersion: 'master-2020-02-13-adfef4b5',
    thanosImage: 'quay.io/thanos/thanos:' + defaultConfig.thanosVersion,
    hashrings: [
      {
        hashring: 'default',
        tenants: [
        ],
      },
    ],
    objectStorageConfig: {
      name: 'thanos-objectstorage',
      key: 'thanos.yaml',
    },
    commonLabels: {
      'app.kubernetes.io/part-of': 'telemeter-benchmark',
    },
    thanosReceiveController+: {
      local trcConfig = self,
      version: 'master-2020-02-06-b66e0c8',
      image: 'quay.io/observatorium/thanos-receive-controller:' + trcConfig.version,
      hashrings: defaultConfig.hashrings,
    },
    receivers+: {
      image: defaultConfig.thanosImage,
      version: defaultConfig.thanosVersion,
      hashrings: defaultConfig.hashrings,
      objectStorageConfig: defaultConfig.objectStorageConfig,
      replicas: 3,
    },
    query: {
      image: defaultConfig.thanosImage,
      version: defaultConfig.thanosVersion,
    },
    telemeterServer+: {
      image: 'quay.io/openshift/origin-telemeter:v4.0',
      replicas: 10,
      whitelist: [],
    },
  },

  thanosReceiveController+:: {
    config+:: b.config.thanosReceiveController,
  },

  telemeterServer+:: {
    config+:: b.config.telemeterServer,
  },

  receivers+:: {
    [hashring.hashring]+: {
      config+:: b.config.receivers,
    }
    for hashring in b.config.hashrings
  },

  query+:: {
    config+:: b.config.query,
  },
}
