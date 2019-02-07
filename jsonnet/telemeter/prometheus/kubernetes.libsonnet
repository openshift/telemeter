local k = import 'ksonnet/ksonnet.beta.3/k.libsonnet';

{
  _config+:: {
    namespace: 'telemeter',

    versions+:: {
      openshiftOauthProxy: 'v1.1.0',
      prometheus: 'v2.3.2',
    },

    imageRepos+:: {
      openshiftOauthProxy: 'openshift/oauth-proxy',
      prometheus: 'quay.io/prometheus/prometheus',
    },

    prometheus+:: {
      name: 'telemeter',
      replicas: 2,
      rules: { groups: [] },
      htpasswdAuth: '',
      sessionSecret: '',
    },
  },

  prometheus+:: {
    // The proxy secret is there to encrypt session created by the oauth proxy.
    proxySecret:
      local secret = k.core.v1.secret;

      secret.new('prometheus-%s-proxy' % $._config.prometheus.name, {
        session_secret: std.base64($._config.prometheus.sessionSecret),
      }) +
      secret.mixin.metadata.withNamespace($._config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'prometheus-' + $._config.prometheus.name }),

    htpasswdSecret:
      local secret = k.core.v1.secret;

      secret.new('prometheus-%s-htpasswd' % $._config.prometheus.name, {
        auth: std.base64($._config.prometheus.htpasswdAuth),
      }) +
      secret.mixin.metadata.withNamespace($._config.namespace) +
      secret.mixin.metadata.withLabels({ 'k8s-app': 'prometheus-' + $._config.prometheus.name }),

    route: {
      apiVersion: 'v1',
      kind: 'Route',
      metadata: {
        annotations: {
          'kubernetes.io/tls-acme': 'true',
          'kubernetes.io/tls-acme-secretname': 'prometheus-%s-acme' % $._config.prometheus.name,
        },
        name: 'prometheus-' + $._config.prometheus.name,
        namespace: $._config.namespace,
      },
      spec: {
        to: {
          kind: 'Service',
          name: 'prometheus-' + $._config.prometheus.name,
        },
        port: {
          targetPort: 'https',
        },
        tls: {
          termination: 'Reencrypt',
        },
      },
    },

    serviceAccount:
      local serviceAccount = k.core.v1.serviceAccount;

      serviceAccount.new('prometheus-' + $._config.prometheus.name) +
      serviceAccount.mixin.metadata.withNamespace($._config.namespace) +
      serviceAccount.mixin.metadata.withAnnotations({
        'serviceaccounts.openshift.io/oauth-redirectreference.prometheus-k8s': '{"kind":"OAuthRedirectReference","apiVersion":"v1","reference":{"kind":"Route","name":"prometheus-telemeter"}}',
      }),

    service:
      local service = k.core.v1.service;
      local servicePort = k.core.v1.service.mixin.spec.portsType;

      local prometheusPort = servicePort.newNamed('https', 9091, 'https');

      service.new('prometheus-' + $._config.prometheus.name, { app: 'prometheus', prometheus: $._config.prometheus.name }, prometheusPort) +
      service.mixin.metadata.withNamespace($._config.namespace) +
      service.mixin.metadata.withLabels({ prometheus: $._config.prometheus.name }) +
      service.mixin.metadata.withAnnotations({
        'service.alpha.openshift.io/serving-cert-secret-name': 'prometheus-telemeter-tls',
      }) +
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

    clusterRole:
      local clusterRole = k.rbac.v1.clusterRole;
      local policyRule = clusterRole.rulesType;

      local metricsRule =
        policyRule.new() +
        policyRule.withNonResourceUrls('/metrics') +
        policyRule.withVerbs(['get']);

      local authenticationRule =
        policyRule.new() +
        policyRule.withApiGroups(['authentication.k8s.io']) +
        policyRule.withResources([
          'tokenreviews',
        ]) +
        policyRule.withVerbs(['create']);

      local authorizationRule =
        policyRule.new() +
        policyRule.withApiGroups(['authorization.k8s.io']) +
        policyRule.withResources([
          'subjectaccessreviews',
        ]) +
        policyRule.withVerbs(['create']);

      local namespacesRule =
        policyRule.new() +
        policyRule.withApiGroups(['']) +
        policyRule.withResources([
          'namespaces',
        ]) +
        policyRule.withVerbs(['get']);

      clusterRole.new() +
      clusterRole.mixin.metadata.withName('prometheus-' + $._config.prometheus.name) +
      clusterRole.withRules([metricsRule, authenticationRule, authorizationRule, namespacesRule]),

    clusterRoleBinding:
      local clusterRoleBinding = k.rbac.v1.clusterRoleBinding;

      clusterRoleBinding.new() +
      clusterRoleBinding.mixin.metadata.withName('prometheus-' + $._config.prometheus.name) +
      clusterRoleBinding.mixin.roleRef.withApiGroup('rbac.authorization.k8s.io') +
      clusterRoleBinding.mixin.roleRef.withName('prometheus-' + $._config.prometheus.name) +
      clusterRoleBinding.mixin.roleRef.mixinInstance({ kind: 'ClusterRole' }) +
      clusterRoleBinding.withSubjects([{ kind: 'ServiceAccount', name: 'prometheus-' + $._config.prometheus.name, namespace: $._config.namespace }]),

    prometheus:
      local container = k.core.v1.pod.mixin.spec.containersType;
      local resourceRequirements = container.mixin.resourcesType;
      local selector = k.apps.v1beta2.deployment.mixin.spec.selectorType;
      local pvc = k.core.v1.persistentVolumeClaim;

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
          secrets: [
            'prometheus-%s-tls' % $._config.prometheus.name,
            'prometheus-%s-proxy' % $._config.prometheus.name,
            'prometheus-%s-htpasswd' % $._config.prometheus.name,
          ],
          serviceMonitorSelector: selector.withMatchLabels({
            'k8s-app': 'telemeter-server',
            endpoint: 'federate',
          }),
          storage: {
            volumeClaimTemplate:
              pvc.new() +
              pvc.mixin.spec.withAccessModes('ReadWriteOnce') +
              pvc.mixin.spec.resources.withRequests({storage: '500Gi'}) +
              pvc.mixin.spec.withStorageClassName('gp2-encrypted'),
          },
          listenLocal: true,
          containers: [
            {
              name: 'prometheus-proxy',
              image: $._config.imageRepos.openshiftOauthProxy + ':' + $._config.versions.openshiftOauthProxy,
              resources: {},
              ports: [
                {
                  containerPort: 9091,
                  name: 'https',
                },
              ],
              args: [
                '-provider=openshift',
                '-https-address=:9091',
                '-http-address=',
                '-email-domain=*',
                '-upstream=http://localhost:9090',
                '-htpasswd-file=/etc/proxy/htpasswd/auth',
                '-openshift-service-account=prometheus-' + $._config.prometheus.name,
                '-openshift-sar={"resource": "namespaces", "verb": "get"}',
                '-openshift-delegate-urls={"/": {"resource": "namespaces", "verb": "get"}}',
                '-tls-cert=/etc/tls/private/tls.crt',
                '-tls-key=/etc/tls/private/tls.key',
                '-client-secret-file=/var/run/secrets/kubernetes.io/serviceaccount/token',
                '-cookie-secret-file=/etc/proxy/secrets/session_secret',
                '-openshift-ca=/etc/pki/tls/cert.pem',
                '-openshift-ca=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt',
                '-skip-auth-regex=^/metrics',
              ],
              volumeMounts: [
                {
                  mountPath: '/etc/tls/private',
                  name: 'secret-prometheus-%s-tls' % $._config.prometheus.name,
                },
                {
                  mountPath: '/etc/proxy/secrets',
                  name: 'secret-prometheus-%s-proxy' % $._config.prometheus.name,
                },
                {
                  mountPath: '/etc/proxy/htpasswd',
                  name: 'secret-prometheus-%s-htpasswd' % $._config.prometheus.name,
                },
              ],
            },
          ],
        },
      },

    serviceMonitor:
      {
        apiVersion: 'monitoring.coreos.com/v1',
        kind: 'ServiceMonitor',
        metadata: {
          name: 'prometheus-' + $._config.prometheus.name,
          namespace: $._config.namespace,
          labels: {
            'k8s-app': 'prometheus',
          },
        },
        spec: {
          selector: {
            matchLabels: {
              prometheus: $._config.prometheus.name,
            },
          },
          endpoints: [
            {
              port: 'https',
              interval: '30s',
              scheme: 'https',
              tlsConfig: {
                caFile: '/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt',
                serverName: 'prometheus-%s.%s.svc' % [$._config.prometheus.name, $._config.namespace],
              },
              bearerTokenFile: '/var/run/secrets/kubernetes.io/serviceaccount/token',
            },
          ],
        },
      },
  },
}
