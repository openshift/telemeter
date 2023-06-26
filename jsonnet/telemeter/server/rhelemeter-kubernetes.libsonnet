local k = import 'ksonnet/ksonnet.beta.4/k.libsonnet';
local secretName = 'rhelemeter-server';
local secretVolumeName = 'secret-rhelemeter-server';
local caSecretName = 'rhelemeter-server-ca';
local caSecretVolumeName = 'secret-rhelemeter-server-ca';
local caMountPath = '/etc/pki/ca';
local tlsSecret = 'rhelemeter-server-shared';
local tlsVolumeName = 'rhelemeter-server-tls';
local tlsMountPath = '/etc/pki/service';
local externalPort = 8443;
local internalPort = 8081;

{
  _config+:: {
    namespace: 'rhelemeter',

    rhelemeterServer+:: {
      replicas: 2,
      oidcIssuer: '',
      clientSecret: '',
      clientID: '',
      whitelist: [],
      elideLabels: [],
      resourceLimits: {},
      resourceRequests: {},
    },

    versions+:: {
      rhelemeterServer: 'v4.0',
    },

    imageRepos+:: {
      rhelemeterServer: 'quay.io/openshift/origin-telemeter',
    },
  },


  rhelemeterServer+:: {
    deployment:
      local deployment = k.apps.v1.deployment;
      local container = k.apps.v1.deployment.mixin.spec.template.spec.containersType;
      local volume = k.apps.v1.deployment.mixin.spec.template.spec.volumesType;
      local containerPort = container.portsType;
      local containerVolumeMount = container.volumeMountsType;
      local containerEnv = container.envType;

      local podLabels = { 'k8s-app': 'rhelemeter-server' };
      local caMount = containerVolumeMount.new(caSecretVolumeName, caMountPath);
      local caVolume = volume.fromSecret(caSecretVolumeName, caSecretName);
      local tlsMount = containerVolumeMount.new(tlsVolumeName, tlsMountPath);
      local tlsVolume = volume.fromSecret(tlsVolumeName, tlsSecret);
      local oidcIssuer = containerEnv.fromSecretRef('OIDC_ISSUER', secretName, 'oidc_issuer');
      local clientSecret = containerEnv.fromSecretRef('CLIENT_SECRET', secretName, 'client_secret');
      local clientID = containerEnv.fromSecretRef('CLIENT_ID', secretName, 'client_id');
      local secretVolume = volume.fromSecret(secretVolumeName, secretName);

      local whitelist = std.map(
        function(rule) '--whitelist=%s' % std.strReplace(rule, 'ALERTS', 'alerts'),
        $._config.rhelemeterServer.whitelist
      );

      local elide = std.map(
        function(label) '--elide-label=%s' % label,
        $._config.rhelemeterServer.elideLabels
      );


      local rhelemeterServer =
        container.new('rhelemeter-server', $._config.imageRepos.rhelemeterServer + ':' + $._config.versions.rhelemeterServer) +
        container.withCommand([
          '/usr/bin/rhelemeter-server',
          '--listen=0.0.0.0:8443',
          '--listen-internal=0.0.0.0:8081',
          '--tls-key=%s/tls.key' % tlsMountPath,
          '--tls-crt=%s/tls.crt' % tlsMountPath,
          '--tls-ca-crt=%s/ca.crt' % caMountPath,
          '--internal-tls-key=%s/tls.key' % tlsMountPath,
          '--internal-tls-crt=%s/tls.crt' % tlsMountPath,
          '--oidc-issuer=$(OIDC_ISSUER)',
          '--client-id=$(CLIENT_ID)',
          '--client-secret=$(CLIENT_SECRET)',
        ] + whitelist + elide) +
        container.withPorts([
          containerPort.newNamed(externalPort, 'external'),
          containerPort.newNamed(internalPort, 'internal'),
        ]) +
        container.mixin.resources.withLimitsMixin($._config.rhelemeterServer.resourceLimits) +
        container.mixin.resources.withRequestsMixin($._config.rhelemeterServer.resourceRequests) +
        container.withVolumeMounts([tlsMount, caMount]) +
        container.withEnv([oidcIssuer, clientSecret, clientID]) + {
          livenessProbe: {
            httpGet: {
              path: '/healthz',
              port: internalPort,
              scheme: 'HTTPS',
            },
          },
          readinessProbe: {
            httpGet: {
              path: '/healthz/ready',
              port: internalPort,
              scheme: 'HTTPS',
            },
          },
        };

      deployment.new('rhelemeter-server', $._config.rhelemeterServer.replicas, [rhelemeterServer], podLabels) +
      deployment.mixin.metadata.withNamespace($._config.namespace) +
      deployment.mixin.spec.selector.withMatchLabels(podLabels) +
      deployment.mixin.spec.template.spec.withServiceAccountName('rhelemeter-server') +
      deployment.mixin.spec.template.spec.withVolumes([secretVolume, tlsVolume, caVolume]) +
      {
        spec+: {
          volumeClaimTemplates:: null,
        },
      },

    secret:
      local secret = k.core.v1.secret;
      secret.new(secretName) +
      secret.withStringData({
        oidc_issuer: $._config.rhelemeterServer.oidcIssuer,
        client_id: $._config.rhelemeterServer.clientID,
        client_secret: $._config.rhelemeterServer.clientSecret,
      }) +
      secret.mixin.metadata.withNamespace($._config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'rhelemeter-server' }),

    externalMtlsSecret:
      local caSecret = k.core.v1.secret;
      caSecret.new(caSecretName) +
      caSecret.withStringData({
        'ca.crt': $._config.rhelemeterServer.externalMtlsCa,
        'tls.key': $._config.rhelemeterServer.externalMtlsKey,
        'tls.crt': $._config.rhelemeterServer.externalMtlsCrt,
      }) +
      caSecret.mixin.metadata.withNamespace($._config.namespace) +
      caSecret.mixin.metadata.withLabels({ 'k8s-app': 'rhelemeter-server' }),

    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local servicePortExternal = servicePort.newNamed('external', externalPort, 'external');
      local servicePortInternal = servicePort.newNamed('internal', internalPort, 'internal');

      service.new('rhelemeter-server', $.rhelemeterServer.deployment.spec.selector.matchLabels, [servicePortExternal, servicePortInternal]) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ 'k8s-app': 'rhelemeter-server' }) +
      service.mixin.spec.withClusterIp('None') +
      service.mixin.metadata.withAnnotations({
        'service.alpha.openshift.io/serving-cert-secret-name': tlsSecret,
      }),

    serviceAccount:
      local serviceAccount = k.core.v1.serviceAccount;

      serviceAccount.new('rhelemeter-server') +
      serviceAccount.mixin.metadata.withNamespace($._config.namespace),

    serviceMonitor:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'rhelemeter-server',
          namespace: $._config.namespace,
          labels: {
            'k8s-app': 'rhelemeter-server',
            endpoint: 'metrics',
          },
        },
        spec: {
          jobLabel: 'k8s-app',
          selector: {
            matchLabels: {
              'k8s-app': 'rhelemeter-server',
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
                serverName: 'rhelemeter-server.%s.svc' % $._config.namespace,
              },
            },
          ],
        },
      },
  },
}
