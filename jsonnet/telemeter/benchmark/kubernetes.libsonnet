local k = import 'ksonnet/ksonnet.beta.4/k.libsonnet';
local t = (import 'kube-thanos/thanos.libsonnet');
local secretName = 'telemeter-server';
local secretMountPath = '/etc/telemeter';
local secretVolumeName = 'secret-telemeter-server';
local tlsSecret = 'telemeter-server-shared';
local tlsVolumeName = 'telemeter-server-tls';
local tlsMountPath = '/etc/pki/service';
local authorizePort = 8083;
local externalPort = 8080;
local internalPort = 8081;
local tokensFileName = 'tokens.json';

{
  local b = self,

  config+:: {
    telemeterServer+:: {
      authorizeURL: 'http://localhost:' + authorizePort,
    },
  },

  telemeterServer+:: {
    local ts = self,
    route: {
      apiVersion: 'v1',
      kind: 'Route',
      metadata: {
        name: 'telemeter-server',
        namespace: b.config.namespace,
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
      local statefulSet = k.apps.v1.statefulSet;
      local container = k.apps.v1.statefulSet.mixin.spec.template.spec.containersType;
      local volume = k.apps.v1.statefulSet.mixin.spec.template.spec.volumesType;
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
        ts.config.whitelist
      );

      local telemeterServer =
        container.new('telemeter-server', ts.config.image) +
        container.withCommand([
          '/usr/bin/telemeter-server',
          '--listen=0.0.0.0:' + externalPort,
          '--listen-internal=0.0.0.0:' + internalPort,
          '--shared-key=%s/tls.key' % tlsMountPath,
          '--authorize=' + b.config.telemeterServer.authorizeURL,
          '--forward-url=http://%s.%s.svc:19291/api/v1/receive' % [b.receivers[b.config.hashrings[0].hashring].config.name, b.config.namespace],
        ] + whitelist) +
        container.withPorts([
          containerPort.newNamed(externalPort, 'external'),
          containerPort.newNamed(internalPort, 'internal'),
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
        container.new('authorization-server', ts.config.image) +
        container.withCommand([
          '/usr/bin/authorization-server',
          'localhost:' + authorizePort,
          '%s/%s' % [secretMountPath, tokensFileName],
        ]) +
        container.withVolumeMounts([secretMount]);

      statefulSet.new('telemeter-server', ts.config.replicas, [telemeterServer, authorizationServer], [], podLabels) +
      statefulSet.mixin.metadata.withNamespace(b.config.namespace) +
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
      secret.mixin.metadata.withNamespace(b.config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-server' }),

    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local servicePortExternal = servicePort.newNamed('external', externalPort, 'external');
      local servicePortInternal = servicePort.newNamed('internal', internalPort, 'internal');

      service.new('telemeter-server', ts.statefulSet.spec.selector.matchLabels, [servicePortExternal, servicePortInternal]) +
      service.mixin.metadata.withNamespace(b.config.namespace) +
      service.mixin.metadata.withLabels({ 'k8s-app': 'telemeter-server' }) +
      service.mixin.spec.withClusterIp('None') +
      service.mixin.metadata.withAnnotations({
        'service.alpha.openshift.io/serving-cert-secret-name': tlsSecret,
      }),

    serviceAccount:
      local serviceAccount = k.core.v1.serviceAccount;

      serviceAccount.new('telemeter-server') +
      serviceAccount.mixin.metadata.withNamespace(b.config.namespace),
  },

  thanosReceiveController:: (import 'thanos-receive-controller/thanos-receive-controller.libsonnet') + {
    config+:: {
      local cfg = self,
      name: b.config.name + '-' + cfg.commonLabels['app.kubernetes.io/name'],
      namespace: b.config.namespace,
      replicas: 1,
      commonLabels+:: b.config.commonLabels,
    },
  },

  receivers:: {
    [hashring.hashring]:
      t.receive +
      t.receive.withRetention +
      t.receive.withHashringConfigMap + {
        config+:: {
          local cfg = self,
          name: b.config.name + '-' + cfg.commonLabels['app.kubernetes.io/name'] + '-' + hashring.hashring,
          namespace: b.config.namespace,
          replicas: 3,
          replicationFactor: 3,
          retention: '6h',
          hashringConfigMapName: '%s-generated' % b.thanosReceiveController.configmap.metadata.name,
          commonLabels+::
            b.config.commonLabels {
              'controller.receive.thanos.io/hashring': hashring.hashring,
            },
        },
        statefulSet+: {
          metadata+: {
            labels+: {
              'controller.receive.thanos.io': 'thanos-receive-controller',
            },
          },
          spec+: {
            template+: {
              spec+: {
                containers: [
                  if c.name == 'thanos-receive' then c {
                    args: std.filter(function(a) !std.startsWith(a, '--objstore'), super.args),
                    env: std.filter(function(e) e.name != 'OBJSTORE_CONFIG', super.env),
                  } else c
                  for c in super.containers
                ],
              },
            },
          },
        },
      }
    for hashring in b.config.hashrings
  },

  query:: t.query {
    config+:: {
      local cfg = self,
      name: b.config.name + '-' + cfg.commonLabels['app.kubernetes.io/name'],
      namespace: b.config.namespace,
      commonLabels+:: b.config.commonLabels,
      replicas: 1,
      stores: [
        'dnssrv+_grpc._tcp.%s.%s.svc.cluster.local' % [service.metadata.name, service.metadata.namespace]
        for service in
          [b.receivers[hashring].service for hashring in std.objectFields(b.receivers)]
      ],
      replicaLabels: ['prometheus_replica', 'ruler_replica', 'replica'],
    },
    route: {
      apiVersion: 'v1',
      kind: 'Route',
      metadata: {
        name: b.query.config.name,
        namespace: b.config.namespace,
        labels: b.query.config.commonLabels,
      },
      spec: {
        to: {
          kind: 'Service',
          name: b.query.config.name,
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
