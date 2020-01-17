{
  prometheus+:: {
    recordingrules+: {
      groups+: [
        {
          name: 'telemeter.rules',
          rules: [
            {
              record: 'name_reason:cluster_operator_degraded:count',
              expr: |||
                count by (name,reason) (cluster_operator_conditions{condition="Degraded"} == 1)
              |||,
            },
            {
              record: 'name_reason:cluster_operator_unavailable:count',
              expr: |||
                count by (name,reason) (cluster_operator_conditions{condition="Available"} == 0)
              |||,
            },
            {
              record: 'id_code:apiserver_request_error_rate_sum:max',
              expr: |||
                sort_desc(max by (_id,code) (code:apiserver_request_count:rate:sum{code=~"(4|5)\\d\\d"}) > 0.5)
              |||,
            },
            {
              record: 'id_version:cluster_available',
              expr: |||
                bottomk by (_id) (1, max by (_id, version) (0 * cluster_version{type="failure"}) or max by (_id, version) (1 + 0 * cluster_version{type="current"}))
              |||,
            },
          ],
        },
      ],
    },
  },
}
