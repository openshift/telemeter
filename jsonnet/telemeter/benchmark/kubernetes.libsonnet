local k = import 'ksonnet/ksonnet.beta.3/k.libsonnet';
local secretName = 'telemeter-server';
local secretMountPath = '/etc/telemeter';
local secretVolumeName = 'secret-telemeter-server';
local tlsSecret = 'telemeter-server-shared';
local tlsVolumeName = 'telemeter-server-tls';
local tlsMountPath = '/etc/pki/service';
local authorizePort = 8083;
local externalPort = 8080;
local internalPort = 8081;
local clusterPort = 8082;
local tokensFileName = 'tokens.json';

{
  _config+:: {
    namespace: 'telemeter-benchmark',

    telemeterServer+:: {
      authorizeURL: 'http://localhost:' + authorizePort,
      replicas: 10,
      whitelist: [],
    },

    prometheus+:: {
      name: 'benchmark',
      replicas: 1,
      rules: { groups: [] },
    },

    versions+:: {
      prometheus: 'v2.7.1',
      telemeterServer: 'v4.0',
    },

    imageRepos+:: {
      prometheus: 'quay.io/prometheus/prometheus',
      telemeterServer: 'quay.io/openshift/origin-telemeter',
    },
  },

  telemeterServer+:: {
    route: {
      apiVersion: 'v1',
      kind: 'Route',
      metadata: {
        name: 'telemeter-server',
        namespace: $._config.namespace,
      },
      spec: {
        to: {
          kind: 'Service',
          name: 'telemeter-server',
        },
        port: {
          targetPort: 'external',
        },
        tls: {
          termination: 'edge',
          insecureEdgeTerminationPolicy: 'Allow',
        },
      },
    },

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
      local secretMount = containerVolumeMount.new(secretVolumeName, secretMountPath);
      local secretVolume = volume.fromSecret(secretVolumeName, secretName);

      local whitelist = std.map(
        function(rule) '--whitelist=%s' % std.strReplace(rule, 'ALERTS', 'alerts'),
        $._config.telemeterServer.whitelist
      );

      local telemeterServer =
        container.new('telemeter-server', $._config.imageRepos.telemeterServer + ':' + $._config.versions.telemeterServer) +
        container.withCommand([
          '/usr/bin/telemeter-server',
          '--join=telemeter-server',
          '--name=$(NAME)',
          '--listen=0.0.0.0:' + externalPort,
          '--listen-internal=0.0.0.0:' + internalPort,
          '--listen-cluster=0.0.0.0:' + clusterPort,
          '--shared-key=%s/tls.key' % tlsMountPath,
          '--authorize=' + $._config.telemeterServer.authorizeURL,
        ] + whitelist) +
        container.withPorts([
          containerPort.newNamed('external', externalPort),
          containerPort.newNamed('internal', internalPort),
          containerPort.newNamed('cluster', clusterPort),
        ]) +
        container.withVolumeMounts([secretMount, tlsMount]) +
        container.withEnv([name]) + {
          livenessProbe: {
            httpGet: {
              path: '/healthz',
              port: externalPort,
              scheme: 'HTTP',
            },
          },
          readinessProbe: {
            httpGet: {
              path: '/healthz/ready',
              port: externalPort,
              scheme: 'HTTP',
            },
          },
        };

      local authorizationServer =
        container.new('authorization-server', $._config.imageRepos.telemeterServer + ':' + $._config.versions.telemeterServer) +
        container.withCommand([
          '/usr/bin/authorization-server',
          'localhost:' + authorizePort,
          '%s/%s' % [secretMountPath, tokensFileName],
        ]) +
        container.withVolumeMounts([secretMount]);

      statefulSet.new('telemeter-server', $._config.telemeterServer.replicas, [telemeterServer, authorizationServer], [], podLabels) +
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
        [tokensFileName]: std.base64(std.toString([{ token: 'benchmark' }])),
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
              interval: '30s',
              port: 'internal',
              scheme: 'http',
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
              honorLabels: true,
              interval: '15s',
              params: {
                'match[]': ['{__name__=~".*"}'],
              },
              path: '/federate',
              port: 'internal',
              scheme: 'http',
            },
          ],
        },
      },
  },

  prometheus+:: {
    serviceAccount:
      local serviceAccount = k.core.v1.serviceAccount;

      serviceAccount.new('prometheus-' + $._config.prometheus.name) +
      serviceAccount.mixin.metadata.withNamespace($._config.namespace),

    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local prometheusPort = servicePort.newNamed('web', 9090, 'web');

      service.new('prometheus-' + $._config.prometheus.name, { app: 'prometheus', prometheus: $._config.prometheus.name }, prometheusPort) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ prometheus: $._config.prometheus.name }) +
      service.mixin.spec.withType('ClusterIP'),

    rules:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'PrometheusRule',
        metadata: {
          labels: {
            prometheus: $._config.prometheus.name,
            role: 'alert-rules',
          },
          name: 'prometheus-' + $._config.prometheus.name + '-rules',
          namespace: $._config.namespace,
        },
        spec: {
          groups: $._config.prometheus.rules.groups,
        },
      },

    roleSpecificNamespaces:
      local role = k.rbac.v1.role;
      local policyRule = role.rulesType;
      local coreRule =
        policyRule.new() +
        policyRule.withApiGroups(['']) +
        policyRule.withResources([
          'services',
          'endpoints',
          'pods',
        ]) +
        policyRule.withVerbs(['get', 'list', 'watch']);

      role.new() +
      role.mixin.metadata.withName('prometheus-' + $._config.prometheus.name) +
      role.mixin.metadata.withNamespace($._config.namespace) +
      role.withRules(coreRule),

    roleBindingSpecificNamespaces:
      local roleBinding = k.rbac.v1.roleBinding;

      roleBinding.new() +
      roleBinding.mixin.metadata.withName('prometheus-' + $._config.prometheus.name) +
      roleBinding.mixin.metadata.withNamespace($._config.namespace) +
      roleBinding.mixin.roleRef.withApiGroup('rbac.authorization.k8s.io') +
      roleBinding.mixin.roleRef.withName('prometheus-' + $._config.prometheus.name) +
      roleBinding.mixin.roleRef.mixinInstance({ kind: 'Role' }) +
      roleBinding.withSubjects([{ kind: 'ServiceAccount', name: 'prometheus-' + $._config.prometheus.name, namespace: $._config.namespace }]),

    roleConfig:
      local role = k.rbac.v1.role;
      local policyRule = role.rulesType;

      local configmapRule =
        policyRule.new() +
        policyRule.withApiGroups(['']) +
        policyRule.withResources([
          'configmaps',
        ]) +
        policyRule.withVerbs(['get']);

      role.new() +
      role.mixin.metadata.withName('prometheus-' + $._config.prometheus.name + '-config') +
      role.mixin.metadata.withNamespace($._config.namespace) +
      role.withRules(configmapRule),

    roleBindingConfig:
      local roleBinding = k.rbac.v1.roleBinding;

      roleBinding.new() +
      roleBinding.mixin.metadata.withName('prometheus-' + $._config.prometheus.name + '-config') +
      roleBinding.mixin.metadata.withNamespace($._config.namespace) +
      roleBinding.mixin.roleRef.withApiGroup('rbac.authorization.k8s.io') +
      roleBinding.mixin.roleRef.withName('prometheus-' + $._config.prometheus.name + '-config') +
      roleBinding.mixin.roleRef.mixinInstance({ kind: 'Role' }) +
      roleBinding.withSubjects([{ kind: 'ServiceAccount', name: 'prometheus-' + $._config.prometheus.name, namespace: $._config.namespace }]),

    prometheus:
      local container = k.core.v1.pod.mixin.spec.containersType;
      local resourceRequirements = container.mixin.resourcesType;
      local selector = k.apps.v1beta2.deployment.mixin.spec.selectorType;

      local resources =
        resourceRequirements.new() +
        resourceRequirements.withRequests({ memory: '400Mi' });

      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'Prometheus',
        metadata: {
          name: $._config.prometheus.name,
          namespace: $._config.namespace,
          labels: {
            prometheus: $._config.prometheus.name,
          },
        },
        spec: {
          replicas: $._config.prometheus.replicas,
          version: $._config.versions.prometheus,
          baseImage: $._config.imageRepos.prometheus,
          securityContext: {},
          serviceAccountName: 'prometheus-' + $._config.prometheus.name,
          nodeSelector: { 'beta.kubernetes.io/os': 'linux' },
          resources: resources,
          ruleSelector: selector.withMatchLabels({
            role: 'alert-rules',
            prometheus: $._config.prometheus.name,
          }),
          serviceMonitorSelector: selector.withMatchLabels({
            'k8s-app': 'telemeter-server',
          }),
        },
      },

    route: {
      apiVersion: 'v1',
      kind: 'Route',
      metadata: {
        name: 'prometheus-' + $._config.prometheus.name,
        namespace: $._config.namespace,
      },
      spec: {
        to: {
          kind: 'Service',
          name: 'prometheus-' + $._config.prometheus.name,
        },
        port: {
          targetPort: 'web',
        },
        tls: {
          termination: 'edge',
          insecureEdgeTerminationPolicy: 'Allow',
        },
      },
    },
  },
}
