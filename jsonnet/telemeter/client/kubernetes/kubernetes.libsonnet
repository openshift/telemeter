local k = import 'ksonnet/ksonnet.beta.3/k.libsonnet';
local credentialsSecret = 'telemeter-client';
local credentialsVolumeName = 'credentials';
local credentialsMountPath = '/etc/telemeter';
local fromCAFile = '/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt';
local fromTokenFile = '/var/run/secrets/kubernetes.io/serviceaccount/token';
local metricsPort = 8080;

{
  _config+:: {
    namespace: 'openshift-monitoring',

    telemeterClient+:: {
      from: 'https://prometheus-k8s.%(namespace)s.svc:9091' % $._config,
      to: '',
    },

    versions+:: {
      telemeterClient: 'v4.0',
    },

    imageRepos+:: {
      telemeterClient: 'quay.io/openshift/origin-telemeter',
    },
  },

  telemeterClient+:: {
    clusterRoleBinding:
      local clusterRoleBinding = k.rbac.v1.clusterRoleBinding;

      clusterRoleBinding.new() +
      clusterRoleBinding.mixin.metadata.withName('telemeter-client') +
      clusterRoleBinding.mixin.roleRef.withApiGroup('rbac.authorization.k8s.io') +
      clusterRoleBinding.mixin.roleRef.withName('telemeter-client') +
      clusterRoleBinding.mixin.roleRef.mixinInstance({ kind: 'ClusterRole' }) +
      clusterRoleBinding.withSubjects([{ kind: 'ServiceAccount', name: 'telemeter-client', namespace: $._config.namespace }]),

    clusterRole:
      local clusterRole = k.rbac.v1.clusterRole;
      local policyRule = clusterRole.rulesType;

      local rule = policyRule.new() +
                   policyRule.withApiGroups(['']) +
                   policyRule.withResources([
                     'namespaces',
                   ]) +
                   policyRule.withVerbs(['get']);


      clusterRole.new() +
      clusterRole.mixin.metadata.withName('telemeter-client') +
      clusterRole.withRules([rule]),

    deployment:
      local deployment = k.apps.v1beta2.deployment;
      local container = k.apps.v1beta2.deployment.mixin.spec.template.spec.containersType;
      local volume = k.apps.v1beta2.deployment.mixin.spec.template.spec.volumesType;
      local containerPort = container.portsType;
      local containerVolumeMount = container.volumeMountsType;
      local containerEnv = container.envType;

      local podLabels = { 'k8s-app': 'telemeter-client' };
      local credentialsMount = containerVolumeMount.new(credentialsVolumeName, credentialsMountPath);
      local credentialsVolume = volume.fromSecret(credentialsVolumeName, credentialsSecret);
      local id = containerEnv.fromSecretRef('ID', credentialsSecret, 'id');
      local to = containerEnv.fromSecretRef('TO', credentialsSecret, 'to');

      local telemeterClient =
        container.new('telemeter-client', $._config.imageRepos.telemeterClient + ':' + $._config.versions.telemeterClient) +
        container.withCommand([
          '/usr/bin/telemeter-client',
          '--id=$(ID)',
          '--from=' + $._config.telemeterClient.from,
          '--from-ca-file=' + fromCAFile,
          '--from-token-file=' + fromTokenFile,
          '--to=$(TO)',
          '--to-token-file=' + credentialsMountPath + '/token',
          '--listen=localhost:' + metricsPort,
        ]) +
        container.withPorts(containerPort.newNamed('http', metricsPort)) +
        container.withVolumeMounts([credentialsMount]) +
        container.withEnv([id, to]);

      deployment.new('telemeter-client', 1, [telemeterClient], podLabels) +
      deployment.mixin.metadata.withNamespace($._config.namespace) +
      deployment.mixin.metadata.withLabels(podLabels) +
      deployment.mixin.spec.selector.withMatchLabels(podLabels) +
      deployment.mixin.spec.template.spec.withServiceAccountName('telemeter-client') +
      deployment.mixin.spec.template.spec.withVolumes([credentialsVolume]),

    secret:
      local secret = k.core.v1.secret;

      secret.new(credentialsSecret, {
        to: std.base64($._config.telemeterClient.to),
      }) +
      secret.mixin.metadata.withNamespace($._config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-client' }),

    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local servicePortHTTP = servicePort.newNamed('http', metricsPort, 'http');

      service.new('telemeter-client', $.telemeterClient.deployment.spec.selector.matchLabels, [servicePortHTTP]) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-client' }) +
      service.mixin.spec.withClusterIp('None'),

    serviceAccount:
      local serviceAccount = k.core.v1.serviceAccount;

      serviceAccount.new('telemeter-client') +
      serviceAccount.mixin.metadata.withNamespace($._config.namespace),

    serviceMonitor:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'telemeter-client',
          namespace: $._config.namespace,
          labels: {
            'k8s-app': 'telemeter-client',
          },
        },
        spec: {
          jobLabel: 'k8s-app',
          selector: {
            matchLabels: {
              'k8s-app': 'telemeter-client',
            },
          },
          endpoints: [
            {
              port: 'http',
              scheme: 'http',
              interval: '30s',
            },
          ],
        },
      },
  },
}
