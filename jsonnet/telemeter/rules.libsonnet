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
            {
              record: 'id_version_ebs_account_internal:cluster_subscribed',
              expr: |||
                topk by (_id) (1, max by (_id, managed, ebs_account, internal) (label_replace(label_replace((subscription_labels{support=~"Standard|Premium|Layered"} * 0 + 1) or subscription_labels * 0, "internal", "true", "email_domain", "redhat.com|(.*\\.|^)ibm.com"), "managed", "", "managed", "false")) + on(_id) group_left(version) (topk by (_id) (1, 0*cluster_version{type="current"})))
              |||,
            },
            {
              record: 'subscription_labels:not_redhat_not_ibm',
              expr: |||
                topk by (_id) (1, subscription_labels{email_domain!~"redhat.com|(^|.*\\.)ibm.com"})
              |||,
            },
            {
              record: 'subscription_labels:not_redhat_not_ibm:short_version',
              expr: |||
                (
                  ((time() - topk by (_id) (1,
                    label_replace(
                      label_replace(cluster_version{type="current"}, "version", "4.$1-0.$2", "version", "4\\.(\\d+\\.\\d+)-0.(ci|nightly|okd).*"),
                      "version",
                      "ci-pull-requests",
                      "version",
                      "0.0.1-.*"
                    )
                  )))
                  + on (_id) group_left() (0 * subscription_labels:not_redhat_not_ibm) + 0
                ))
              |||,
            },
          ],
        },
      ],
    },
  },
}
