local k = import 'ksonnet/ksonnet.beta.3/k.libsonnet';
local secretName = 'telemeter-server';
local localVolumeName = 'local';
local localMountPath = '/var/lib/telemeter';
local tlsSecret = 'telemeter-server-shared';
local tlsVolumeName = 'telemeter-server-tls';
local tlsMountPath = '/etc/pki/service';
local externalPort = 8443;
local internalPort = 8082;
local clusterPort = 8081;

{
  _config+:: {
    namespace: 'telemeter',

    telemeterServer+:: {
      authorizeURL: 'https://api.openshift.com/api/accounts_mgmt/v1/cluster_registrations',
      rhdURL: '',
      rhdUsername: '',
      rhdPassword: '',
      rhdClientID: '',
      serverName: 'server-name-replaced-at-runtime',
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
      local localMount = containerVolumeMount.new(localVolumeName, localMountPath);
      local localVolume = volume.fromEmptyDir(localVolumeName, {});
      local tlsMount = containerVolumeMount.new(tlsVolumeName, tlsMountPath);
      local tlsVolume = volume.fromSecret(tlsVolumeName, tlsSecret);
      local name = containerEnv.fromFieldPath('NAME', 'metadata.name');
      local namespace = containerEnv.fromFieldPath('NAMESPACE', 'metadata.namespace');
      local rhdURL = containerEnv.fromSecretRef('RHD_URL', secretName, 'rhd.url');
      local rhdUsername = containerEnv.fromSecretRef('RHD_USERNAME', secretName, 'rhd.username');
      local rhdPassword = containerEnv.fromSecretRef('RHD_PASSWORD', secretName, 'rhd.password');
      local rhdClientID = containerEnv.fromSecretRef('RHD_CLIENT_ID', secretName, 'rhd.client_id');

      local telemeterServer =
        container.new('telemeter-server', $._config.imageRepos.telemeterServer + ':' + $._config.versions.telemeterServer) +
        container.withCommand([
          '/usr/bin/telemeter-server',
          '--join=telemeter-cluster',
          '--name=$(NAME)',
          '--listen=0.0.0.0:8443',
          '--listen-internal=0.0.0.0:8081',
          '--listen-cluster=0.0.0.0:8082',
          '--storage-dir=' + localMountPath,
          '--shared-key=%s/tls.key' % tlsMountPath,
          '--tls-key=%s/tls.key' % tlsMountPath,
          '--tls-crt=%s/tls.crt' % tlsMountPath,
          '--authorize=' + $._config.telemeterServer.authorizeURL,
          '--authorize-issuer-url=$(RHD_URL)',
          '--authorize-client-id=$(RHD_CLIENT_ID)',
          '--authorize-username=$(RHD_USERNAME)',
          '--authorize-password=$(RHD_PASSWORD)',
        ]) +
        container.withPorts([
          containerPort.newNamed('external', externalPort),
          containerPort.newNamed('internal', internalPort),
          containerPort.newNamed('cluster', clusterPort),
        ]) +
        container.withVolumeMounts([tlsMount, localMount]) +
        container.withEnv([rhdURL, rhdUsername, rhdPassword, rhdClientID]) + {
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

      statefulSet.new('telemeter-server', 3, [telemeterServer], {}, podLabels) +
      statefulSet.mixin.metadata.withNamespace($._config.namespace) +
      statefulSet.mixin.spec.selector.withMatchLabels(podLabels) +
      statefulSet.mixin.spec.withPodManagementPolicy('Parallel') +
      statefulSet.mixin.spec.withServiceName('telemeter-server') +
      statefulSet.mixin.spec.template.spec.withServiceAccountName('telemeter-server') +
      statefulSet.mixin.spec.template.spec.withVolumes([localVolume, tlsVolume]),

    secret:
      local secret = k.core.v1.secret;

      secret.new(secretName, {
        'rhd.url': std.base64($._config.telemeterServer.rhdURL),
        'rhd.username': std.base64($._config.telemeterServer.rhdUsername),
        'rhd.password': std.base64($._config.telemeterServer.rhdPassword),
        'rhd.client_id': std.base64($._config.telemeterServer.rhdClientID),
      }) +
      secret.mixin.metadata.withNamespace($._config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-server' }),


    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local servicePortExternal = servicePort.newNamed('external;', externalPort, 'external');
      local servicePortInternal = servicePort.newNamed('internal', internalPort, 'internal');
      local servicePortCluster = servicePort.newNamed('cluster', clusterPort, 'cluster');

      service.new('telemeter-server', $.telemeterServer.statefulSet.spec.selector.matchLabels, [servicePortExternal, servicePortInternal, servicePortCluster]) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-server' }) +
      service.mixin.spec.withClusterIp('None') +
      service.mixin.metadata.withAnnotations({
        'service.alpha.openshift.io/serving-cert-secret-name': tlsSecret,
      }),

    serviceMonitor:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'telemeter-server',
          namespace: $._config.namespace,
          labels: {
            'k8s-app': 'telemeter-server',
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
              port: 'external',
              scheme: 'https',
              tlsConfig: {
                caFile: '/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt',
                serverName: $._config.telemeterServer.serverName,
              },
            },
          ],
        },
      },
  },
}
