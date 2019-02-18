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
  // cluster_version_payload_errors counts the errors that occur while
  // attempting to apply the payload to a cluster. This measurement
  // can be used to identify whether a set of errors that occur during
  // an upgrade are trending up or down from previous updates.
  '{__name__="cluster_version_payload_errors"}',
  // machine_cpu_cores helps estimate the size of a cluster and the
  // instances in use. Some errors on upgrades may only manifest at
  // certain scale and sizes.
  '{__name__="machine_cpu_cores"}',
  // machine_memory_bytes helps estimate the size of a cluster and
  // the instances in use. Some errors on upgrades may only manifest at
  // certain scale and sizes.
  '{__name__="machine_memory_bytes"}',
  // etcd_object_counts identifies two key metrics - the rough size of
  // the data stored in etcd and the features in use - both of which
  // may cause upgrades to exhibit failures. For instance, an upgrade
  // which only fails when service catalog objects are present may
  // identify a regression in that specific component.
  '{__name__="etcd_object_counts"}',
  // alerts are the key summarization of the system state. They are
  // reported via telemetry to assess their value in detecting
  // upgrade failure causes and also to prevent the need to gather
  // large sets of metrics that are already summarized on the cluster.
  // Reporting alerts also creates an incentive to improve per
  // cluster alerting for the purposes of preventing upgrades from
  // failing for end users.
  '{__name__="ALERTS",alertstate="firing"}',
]
