local k = import 'ksonnet/ksonnet.beta.4/k.libsonnet';
local secretName = 'telemeter-server';
local secretVolumeName = 'secret-telemeter-server';
local tlsSecret = 'telemeter-server-shared';
local tlsVolumeName = 'telemeter-server-tls';
local tlsMountPath = '/etc/pki/service';
local externalPort = 8443;
local internalPort = 8081;

{
  _config+:: {
    namespace: 'telemeter',

    telemeterServer+:: {
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
      local statefulSet = k.apps.v1.statefulSet;
      local container = k.apps.v1.statefulSet.mixin.spec.template.spec.containersType;
      local volume = k.apps.v1.statefulSet.mixin.spec.template.spec.volumesType;
      local containerPort = container.portsType;
      local containerVolumeMount = container.volumeMountsType;
      local containerEnv = container.envType;

      local podLabels = { 'k8s-app': 'telemeter-server' };
      local tlsMount = containerVolumeMount.new(tlsVolumeName, tlsMountPath);
      local tlsVolume = volume.fromSecret(tlsVolumeName, tlsSecret);
      local authorizeURL = containerEnv.fromSecretRef('AUTHORIZE_URL', secretName, 'authorize_url');
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

      local memcachedReplicas = std.range(0, $.memcached.replicas - 1);
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
          '--listen=0.0.0.0:8443',
          '--listen-internal=0.0.0.0:8081',
          '--shared-key=%s/tls.key' % tlsMountPath,
          '--tls-key=%s/tls.key' % tlsMountPath,
          '--tls-crt=%s/tls.crt' % tlsMountPath,
          '--internal-tls-key=%s/tls.key' % tlsMountPath,
          '--internal-tls-crt=%s/tls.crt' % tlsMountPath,
          '--authorize=$(AUTHORIZE_URL)',
          '--oidc-issuer=$(OIDC_ISSUER)',
          '--client-id=$(CLIENT_ID)',
          '--client-secret=$(CLIENT_SECRET)',
        ] + memcached + whitelist + elide) +
        container.withPorts([
          containerPort.newNamed(externalPort, 'external'),
          containerPort.newNamed(internalPort, 'internal'),
        ]) +
        container.mixin.resources.withLimitsMixin($._config.telemeterServer.resourceLimits) +
        container.mixin.resources.withRequestsMixin($._config.telemeterServer.resourceRequests) +
        container.withVolumeMounts([tlsMount]) +
        container.withEnv([authorizeURL, oidcIssuer, clientSecret, clientID]) + {
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
        authorize_url: '',
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

      service.new('telemeter-server', $.telemeterServer.statefulSet.spec.selector.matchLabels, [servicePortExternal, servicePortInternal]) +
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
                caFile: '/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt',
                serverName: 'telemeter-server.%s.svc' % $._config.namespace,
              },
            },
          ],
        },
      },
  },

  memcached+:: {
    images:: {
      memcached: 'docker.io/memcached',
      exporter: 'docker.io/prom/memcached-exporter',
    },
    tags:: {
      memcached: '1.5.20-alpine',
      exporter: 'v0.6.0',
    },
    replicas:: 3,
    maxItemSize:: '1m',
    memoryLimitMB:: 1024,
    overprovisionFactor:: 1.2,
    connectionLimit:: 1024,
    resourceLimits:: {
      cpu: '3',
      memory: std.ceil($.memcached.memoryLimitMB * $.memcached.overprovisionFactor * 1.5) + 'Mi',
    },
    resourceRequests:: {
      cpu: '500m',
      memory: std.ceil(($.memcached.memoryLimitMB * $.memcached.overprovisionFactor) + 100) + 'Mi',
    },

    service:
      local service = k.core.v1.service;
      local ports = service.mixin.spec.portsType;

      service.new(
        'memcached',
        $.memcached.statefulSet.metadata.labels,
        [
          ports.newNamed('client', 11211, 11211),
          ports.newNamed('metrics', 9150, 9150),
        ]
      ) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ 'app.kubernetes.io/name': $.memcached.service.metadata.name }) +
      service.mixin.spec.withClusterIp('None'),

    statefulSet:
      local sts = k.apps.v1.statefulSet;
      local container = k.apps.v1.statefulSet.mixin.spec.template.spec.containersType;
      local containerPort = container.portsType;

      local c =
        container.new('memcached', $.memcached.images.memcached + ':' + $.memcached.tags.memcached) +
        container.withPorts([containerPort.newNamed($.memcached.service.spec.ports[0].port, $.memcached.service.spec.ports[0].name)]) +
        container.withArgs([
          '-m %(memoryLimitMB)s' % self,
          '-I %(maxItemSize)s' % self,
          '-c %(connectionLimit)s' % self,
          '-v',
        ]) +
        container.mixin.resources.withLimitsMixin($.memcached.resourceLimits) +
        container.mixin.resources.withRequestsMixin($.memcached.resourceRequests);

      local exporter =
        container.new('exporter', $.memcached.images.exporter + ':' + $.memcached.tags.exporter) +
        container.withPorts([containerPort.newNamed($.memcached.service.spec.ports[1].port, $.memcached.service.spec.ports[1].name)]) +
        container.withArgs([
          '--memcached.address=localhost:%d' % $.memcached.service.spec.ports[0].port,
          '--web.listen-address=0.0.0.0:%d' % $.memcached.service.spec.ports[1].port,
        ]);

      sts.new('memcached', $.memcached.replicas, [c, exporter], [], $.memcached.statefulSet.metadata.labels) +
      sts.mixin.metadata.withNamespace($._config.namespace) +
      sts.mixin.metadata.withLabels({ 'app.kubernetes.io/name': $.memcached.statefulSet.metadata.name }) +
      sts.mixin.spec.withServiceName($.memcached.service.metadata.name) +
      sts.mixin.spec.selector.withMatchLabels($.memcached.statefulSet.metadata.labels) +
      {
        spec+: {
          volumeClaimTemplates:: null,
        },
      },

    serviceMonitor:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'memcached',
          namespace: $._config.namespace,
          labels: {
            'app.kubernetes.io/name': $.memcached.statefulSet.metadata.name,
          },
        },
        spec: {
          jobLabel: 'app.kubernetes.io/name',
          selector: {
            matchLabels: {
              'app.kubernetes.io/name': $.memcached.statefulSet.metadata.name,
            },
          },
          endpoints: [
            {
              interval: '30s',
              port: $.memcached.service.spec.ports[1].name,
            },
          ],
        },
      },
  },
}
