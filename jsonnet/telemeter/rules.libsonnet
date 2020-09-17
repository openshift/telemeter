{
  prometheus+:: {
    recordingrules+: {
      groups+: [
        {
          name: 'telemeter.rules',
          interval: '1m',
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
                topk by (_id) (1, max by (_id, managed, ebs_account, internal) (label_replace(label_replace((subscription_labels{support=~"Standard|Premium|Layered"} * 0 + 1) or subscription_labels * 0, "internal", "true", "email_domain", "redhat.com|(.*\\.|^)ibm.com"), "managed", "", "managed", "false")) + on(_id) group_left(version) (topk by (_id) (1, id_version*0)) + on(_id) group_left(install_type) (topk by (_id) (1, id_install_type*0)) + on(_id) group_left(host_type) (topk by (_id) (1, id_primary_host_type*0)) + on(_id) group_left(provider) (topk by (_id) (1, id_provider*0)))
              |||,
            },
            {
              record: 'id_primary_host_type',
              expr: |||
                0 * (max by (_id,host_type) (topk by (_id) (1, label_replace(label_replace(label_replace(label_replace(label_replace(label_replace(cluster:virt_platform_nodes:sum, "host_type", "$1", "type", "(aws|ibm_.*|ovirt|none|rhev|gcp|openstack|hyperv|vmware)"), "host_type", "virt-unknown", "host_type", ""), "host_type", "kvm-unknown", "type", "kvm"), "host_type", "xen-unknown", "type", "xen.*"), "host_type", "metal", "host_type", "none"), "host_type", "ibm-$1", "host_type", "ibm[_-](power|systemz).*"))) or on(_id) label_replace(max by (_id) (cluster_version{type="current"}), "host_type", "", "host_type", ""))
              |||,
            },
            {
              record: 'id_provider',
              expr: |||
                0 * topk by (_id) (1, count by (_id, provider) (label_replace(cluster_infrastructure_provider * 0 + 2, "provider", "$1", "type", "(.*)")) or on(_id) label_replace(max by (_id) (cluster_version{type="current"}*0+1), "provider", "", "provider", "") or on(_id) label_replace(max by (_id) (cluster:node_instance_type_count:sum*0), "provider", "hypershift-unknown", "provider", ""))
              |||,
            },
            {
              record: 'id_version',
              expr: |||
                0 * (max by (_id,version) (topk by (_id) (1, cluster_version{type="current"})) or on(_id) label_replace(max by (_id) (cluster:node_instance_type_count:sum*0), "version", "", "unknown", ""))
              |||,
            },
            {
              record: 'id_install_type',
              expr: |||
                0 * (count by (_id, install_type) (label_replace(label_replace(label_replace(label_replace(topk by (_id) (1, cluster_installer), "install_type", "upi", "type", "other"), "install_type", "ipi", "type", "openshift-install"), "install_type", "hive", "invoker", "hive"), "install_type", "assisted-installer", "invoker", "assisted-installer")) or on(_id) (label_replace(count by (_id) (cluster:virt_platform_nodes:sum), "install_type", "hypershift-unknown", "install_type", ""))*0)
              |||,
            },
          ],
        },
      ],
    },
  },
}
