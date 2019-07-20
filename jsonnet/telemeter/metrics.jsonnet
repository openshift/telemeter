[
  // up contains information relevant to the health of the registered
  // cluster monitoring sources on a cluster. This metric allows telemetry
  // to identify when an update causes a service to begin to crash-loop or
  // flake.
  '{__name__="up"}',
  // cluster_version reports what payload and version the cluster is being
  // configured to and is used to identify what versions are on a cluster
  // that is experiencing problems.
  '{__name__="cluster_version"}',
  // cluster_version_available_updates reports the channel and version
  // server the cluster is configured to use and how many updates are
  // available. This is used to ensure that updates are being properly
  // served to clusters.
  '{__name__="cluster_version_available_updates"}',
  // cluster_operator_up reports the health status of the core cluster
  // operators - like up, an upgrade that fails due to a configuration value
  // on the cluster will help narrow down which component is affected.
  '{__name__="cluster_operator_up"}',
  // cluster_operator_conditions exposes the status conditions cluster
  // operators report for debugging. The condition and status are reported.
  '{__name__="cluster_operator_conditions"}',
  // cluster_version_payload captures how far through a payload the cluster
  // version operator has progressed and can be used to identify whether
  // a particular payload entry is causing failures during upgrade.
  '{__name__="cluster_version_payload"}',
  // cluster_installer reports what installed the cluster, along with its
  // version number and invoker.
  '{__name__="cluster_installer"}',
  // instance:etcd_object_counts:sum identifies two key metrics:
  // - the rough size of the data stored in etcd and
  // - the consistency between the etcd instances.
  '{__name__="instance:etcd_object_counts:sum"}',
  // alerts are the key summarization of the system state. They are
  // reported via telemetry to assess their value in detecting
  // upgrade failure causes and also to prevent the need to gather
  // large sets of metrics that are already summarized on the cluster.
  // Reporting alerts also creates an incentive to improve per
  // cluster alerting for the purposes of preventing upgrades from
  // failing for end users.
  '{__name__="ALERTS",alertstate="firing"}',
  // the following three metrics will be used for SLA analysis reports.
  // code:apiserver_request_count:rate:sum identifies average of occurances
  // of each http status code over 10 minutes
  '{__name__="code:apiserver_request_count:rate:sum"}',
  // cluster:capacity_cpu_cores:sum is the total number of CPU cores
  // in the cluster labeled by node role and type.
  '{__name__="cluster:capacity_cpu_cores:sum"}',
  // cluster:capacity_memory_bytes:sum is the total bytes of memory
  // in the cluster labeled by node role and type.
  '{__name__="cluster:capacity_memory_bytes:sum"}',
  // cluster:cpu_usage_cores:sum is the current amount of CPU in
  // use across the whole cluster.
  '{__name__="cluster:cpu_usage_cores:sum"}',
  // cluster:memory_usage_bytes:sum is the current amount of memory in
  // use across the whole cluster.
  '{__name__="cluster:memory_usage_bytes:sum"}',
  // openshift:cpu_usage_cores:sum is the current amount of CPU
  // used by OpenShift components.
  '{__name__="openshift:cpu_usage_cores:sum"}',
  // openshift:memory_usage_bytes:sum is the current amount of memory
  // used by OpenShift components.
  '{__name__="openshift:memory_usage_bytes:sum"}',
  // cluster:node_instance_type_count:sum is the number of nodes
  // of each instance type and role.
  '{__name__="cluster:node_instance_type_count:sum"}',
  // cnv:vmi_status_running:count is the total number of VM instances running in the cluster.
  '{__name__="cnv:vmi_status_running:count"}',
  // subscription_sync_total is the number of times an OLM operator
  // Subscription has been synced, labelled by name and installed csv
  '{__name__="subscription_sync_total"}',
]
