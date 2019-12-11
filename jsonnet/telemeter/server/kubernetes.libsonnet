local k = import 'ksonnet/ksonnet.beta.3/k.libsonnet';
local secretName = 'telemeter-server';
local secretVolumeName = 'secret-telemeter-server';
local tlsSecret = 'telemeter-server-shared';
local tlsVolumeName = 'telemeter-server-tls';
local tlsMountPath = '/etc/pki/service';
local externalPort = 8443;
local internalPort = 8081;
local clusterPort = 8082;

{
  _config+:: {
    namespace: 'telemeter',

    telemeterServer+:: {
      authorizeURL: 'https://api.openshift.com/api/accounts_mgmt/v1/cluster_registrations',
      replicas: 10,
      oidcIssuer: '',
      clientSecret: '',
      clientID: '',
      whitelist: [],
      elideLabels: [],
      resourceLimits: {},
      resourceRequests: {},
    },

    versions+:: {
      telemeterServer: 'v4.0',
    },

    imageRepos+:: {
      telemeterServer: 'quay.io/openshift/origin-telemeter',
    },
  },

  telemeterServer+:: {
    statefulSet:
      local statefulSet = k.apps.v1beta2.statefulSet;
      local container = k.apps.v1beta2.statefulSet.mixin.spec.template.spec.containersType;
      local volume = k.apps.v1beta2.statefulSet.mixin.spec.template.spec.volumesType;
      local containerPort = container.portsType;
      local containerVolumeMount = container.volumeMountsType;
      local containerEnv = container.envType;

      local podLabels = { 'k8s-app': 'telemeter-server' };
      local tlsMount = containerVolumeMount.new(tlsVolumeName, tlsMountPath);
      local tlsVolume = volume.fromSecret(tlsVolumeName, tlsSecret);
      local name = containerEnv.fromFieldPath('NAME', 'metadata.name');
      local oidcIssuer = containerEnv.fromSecretRef('OIDC_ISSUER', secretName, 'oidc_issuer');
      local clientSecret = containerEnv.fromSecretRef('CLIENT_SECRET', secretName, 'client_secret');
      local clientID = containerEnv.fromSecretRef('CLIENT_ID', secretName, 'client_id');
      local secretVolume = volume.fromSecret(secretVolumeName, secretName);

      local whitelist = std.map(
        function(rule) '--whitelist=%s' % std.strReplace(rule, 'ALERTS', 'alerts'),
        $._config.telemeterServer.whitelist
      );

      local elide = std.map(
        function(label) '--elide-label=%s' % label,
        $._config.telemeterServer.elideLabels
      );

      local memcachedReplicas = std.range(1, $.memcached.replicas);
      local memcached = [
        '--memcached=%s-%d.%s.%s.svc.cluster.local:%d' % [
          $.memcached.statefulSet.metadata.name,
          i,
          $.memcached.service.metadata.name,
          $.memcached.service.metadata.namespace,
          $.memcached.service.spec.ports[0].port,
        ]
        for i in memcachedReplicas
      ];


      local telemeterServer =
        container.new('telemeter-server', $._config.imageRepos.telemeterServer + ':' + $._config.versions.telemeterServer) +
        container.withCommand([
          '/usr/bin/telemeter-server',
          '--join=telemeter-server',
          '--name=$(NAME)',
          '--listen=0.0.0.0:8443',
          '--listen-internal=0.0.0.0:8081',
          '--listen-cluster=0.0.0.0:8082',
          '--shared-key=%s/tls.key' % tlsMountPath,
          '--tls-key=%s/tls.key' % tlsMountPath,
          '--tls-crt=%s/tls.crt' % tlsMountPath,
          '--internal-tls-key=%s/tls.key' % tlsMountPath,
          '--internal-tls-crt=%s/tls.crt' % tlsMountPath,
          '--authorize=' + $._config.telemeterServer.authorizeURL,
          '--oidc-issuer=$(OIDC_ISSUER)',
          '--client-id=$(CLIENT_ID)',
          '--client-secret=$(CLIENT_SECRET)',
        ] + memcached + whitelist + elide) +
        container.withPorts([
          containerPort.newNamed('external', externalPort),
          containerPort.newNamed('internal', internalPort),
          containerPort.newNamed('cluster', clusterPort),
        ]) +
        container.mixin.resources.withLimitsMixin($._config.telemeterServer.resourceLimits) +
        container.mixin.resources.withRequestsMixin($._config.telemeterServer.resourceRequests) +
        container.withVolumeMounts([tlsMount]) +
        container.withEnv([name, oidcIssuer, clientSecret, clientID]) + {
          livenessProbe: {
            httpGet: {
              path: '/healthz',
              port: externalPort,
              scheme: 'HTTPS',
            },
          },
          readinessProbe: {
            httpGet: {
              path: '/healthz/ready',
              port: externalPort,
              scheme: 'HTTPS',
            },
          },
        };

      statefulSet.new('telemeter-server', $._config.telemeterServer.replicas, [telemeterServer], [], podLabels) +
      statefulSet.mixin.metadata.withNamespace($._config.namespace) +
      statefulSet.mixin.spec.selector.withMatchLabels(podLabels) +
      statefulSet.mixin.spec.withPodManagementPolicy('Parallel') +
      statefulSet.mixin.spec.withServiceName('telemeter-server') +
      statefulSet.mixin.spec.template.spec.withServiceAccountName('telemeter-server') +
      statefulSet.mixin.spec.template.spec.withVolumes([secretVolume, tlsVolume]) +
      {
        spec+: {
          volumeClaimTemplates:: null,
        },
      },

    secret:
      local secret = k.core.v1.secret;

      secret.new(secretName, {
        oidc_issuer: std.base64($._config.telemeterServer.oidcIssuer),
        client_secret: std.base64($._config.telemeterServer.clientSecret),
        client_id: std.base64($._config.telemeterServer.clientID),
      }) +
      secret.mixin.metadata.withNamespace($._config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-server' }),


    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local servicePortExternal = servicePort.newNamed('external', externalPort, 'external');
      local servicePortInternal = servicePort.newNamed('internal', internalPort, 'internal');
      local servicePortCluster = servicePort.newNamed('cluster', clusterPort, 'cluster');

      service.new('telemeter-server', $.telemeterServer.statefulSet.spec.selector.matchLabels, [servicePortExternal, servicePortInternal, servicePortCluster]) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-server' }) +
      service.mixin.spec.withClusterIp('None') +
      service.mixin.metadata.withAnnotations({
        'service.alpha.openshift.io/serving-cert-secret-name': tlsSecret,
      }),

    serviceAccount:
      local serviceAccount = k.core.v1.serviceAccount;

      serviceAccount.new('telemeter-server') +
      serviceAccount.mixin.metadata.withNamespace($._config.namespace),

    serviceMonitor:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'telemeter-server',
          namespace: $._config.namespace,
          labels: {
            'k8s-app': 'telemeter-server',
            endpoint: 'metrics',
          },
        },
        spec: {
          jobLabel: 'k8s-app',
          selector: {
            matchLabels: {
              'k8s-app': 'telemeter-server',
            },
          },
          endpoints: [
            {
              bearerTokenFile: '/var/run/secrets/kubernetes.io/serviceaccount/token',
              interval: '30s',
              port: 'internal',
              scheme: 'https',
              tlsConfig: {
                caFile: '/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt',
                serverName: 'telemeter-server.%s.svc' % $._config.namespace,
              },
            },
          ],
        },
      },
    serviceMonitorFederate:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'telemeter-server-federate',
          namespace: $._config.namespace,
          labels: {
            'k8s-app': 'telemeter-server',
            endpoint: 'federate',
          },
        },
        spec: {
          jobLabel: 'k8s-app',
          selector: {
            matchLabels: {
              'k8s-app': 'telemeter-server',
            },
          },
          endpoints: [
            {
              bearerTokenFile: '/var/run/secrets/kubernetes.io/serviceaccount/token',
              honorLabels: true,
              interval: '15s',
              params: {
                'match[]': ['{__name__=~".*"}'],
              },
              path: '/federate',
              port: 'internal',
              scheme: 'https',
              tlsConfig: {
                caFile: '/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt',
                serverName: 'telemeter-server.%s.svc' % $._config.namespace,
              },
            },
          ],
        },
      },
  },

  memcached+:: {
    image:: 'docker.io/memcached:1.5.20-alpine',
    replicas:: 3,

    service:
      local service = k.core.v1.service;
      local ports = service.mixin.spec.portsType;

      service.new(
        'memcached',
        $.memcached.statefulSet.metadata.labels,
        [
          ports.newNamed('memcached', 11211, 11211),
        ]
      ) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ 'app.kubernetes.io/name': $.memcached.service.metadata.name }) +
      service.mixin.spec.withClusterIp('None'),

    statefulSet:
      local sts = k.apps.v1beta2.statefulSet;
      local volume = sts.mixin.spec.template.spec.volumesType;
      local container = sts.mixin.spec.template.spec.containersType;
      local containerEnv = container.envType;
      local containerVolumeMount = container.volumeMountsType;

      local c =
        container.new($.memcached.statefulSet.metadata.name, $.memcached.image) +
        container.withPorts([
          { name: 'memcached', containerPort: $.memcached.service.spec.ports[0].port },
        ]) +
        container.mixin.resources.withRequests({ cpu: '100m', memory: '512Mi' }) +
        container.mixin.resources.withLimits({ cpu: '1', memory: '1Gi' });

      sts.new('memcached', $.memcached.replicas, c, [], $.memcached.statefulSet.metadata.labels) +
      sts.mixin.metadata.withNamespace($._config.namespace) +
      sts.mixin.metadata.withLabels({ 'app.kubernetes.io/name': $.memcached.statefulSet.metadata.name }) +
      sts.mixin.spec.withServiceName($.memcached.service.metadata.name) +
      sts.mixin.spec.selector.withMatchLabels($.memcached.statefulSet.metadata.labels) +
      {
        spec+: {
          volumeClaimTemplates:: null,
        },
      },
  },
}
