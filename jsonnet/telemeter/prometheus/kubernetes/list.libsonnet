{
  asList(name, data, parameters):: {
    apiVersion: 'v1',
    kind: 'Template',
    metadata: {
      name: name,
    },
    objects: [data[k] for k in std.objectFields(data)],
    parameters: parameters,
  },

  withImage(_config):: {
    local setImage(object) =
      if object.kind == 'Prometheus' then {
        spec+: {
          baseImage: '${IMAGE}',
          version: ':${IMAGE_TAG}',
          containers: [
            if c.name == 'prometheus-proxy' then c {
              image: '${PROXY_IMAGE}:${PROXY_IMAGE_TAG}',
            } else c
            for c in super.containers
          ],
        },
      }
      else {},
    objects: [
      o + setImage(o)
      for o in super.objects
    ],
    parameters+: [
      { name: 'IMAGE', value: _config.imageRepos.prometheus },
      { name: 'IMAGE_TAG', value: _config.versions.prometheus },
      { name: 'PROXY_IMAGE', value: _config.imageRepos.openshiftOauthProxy },
      { name: 'PROXY_IMAGE_TAG', value: _config.versions.openshiftOauthProxy },
    ],
  },

  withNamespace(_config):: {
    local setNamespace(object) =
      if std.objectHas(object, 'metadata') && std.objectHas(object.metadata, 'namespace') then {
        metadata+: {
          namespace: '${NAMESPACE}',
        },
      }
      else {},
    local setSubjectNamespace(object) =
      if std.endsWith(object.kind, 'Binding') then {
        subjects: [
          s { namespace: '${NAMESPACE}' }
          for s in super.subjects
        ],
      }
      else {},
    objects: [
      o + setNamespace(o) + setSubjectNamespace(o)
      for o in super.objects
    ],
    parameters+: [
      { name: 'NAMESPACE', value: _config.namespace },
    ],
  },
}
