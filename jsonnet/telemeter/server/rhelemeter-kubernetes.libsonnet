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
local caCert = |||
  -----BEGIN CERTIFICATE-----
  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
  E6/lvbR33zc=
  -----END CERTIFICATE-----
|||;

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

    caSecret:
      local caSecret = k.core.v1.secret;
      caSecret.new(caSecretName) +
      caSecret.withStringData({
        'ca.crt': caCert,
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
      service.mixin.spec.withClusterIp('None'),


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
