# OpenShift 4 Data Collection

Red Hat values our customer experience and privacy. It is important to us that our customers understand exactly what we are sending back to engineering and why. During the developer preview or beta release of our software, we want to be able to make changes to our designs and coding practices in real-time based on customer environments. The faster the feedback loop during these development stages the better. 

For the OpenShift 4 Developer Preview we will be sending back these exact attributes based on your cluster ID and pull secret from Red Hat:

[embedmd]:# (../metrics.json jsonnet)
```jsonnet
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
  // cluster_installer reports what installed the cluster, along with its
  // version number and invoker.
  '{__name__="cluster_installer"}',
  // cluster_infrastructure_provider reports the configured cloud provider
  // if any, along with the infrastructure region when running in the public
  // cloud.
  '{__name__="cluster_infrastructure_provider"}',
  // cluster_feature_set reports the configured cluster feature set and
  // whether the feature set is considered supported or unsupported.
  '{__name__="cluster_feature_set"}',
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
  // kube_pod_status_ready:etcd:sum is the total number of 'etcd.*' pods
  // in the namespace "kube-system" whose condition is true
  '{__name__="kube_pod_status_ready:etcd:sum"}',
  // kube_pod_status_ready:image_registry:sum is the total number
  // of 'image_registry' pods in the namespace 'openshift-image-registry'
  // whose condition is true
  '{__name__="kube_pod_status_ready:image_registry:sum"}',
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
  // subscription_sync_total is the number of times an OLM operator
  // Subscription has been synced, labelled by name and installed csv
  '{__name__="subscription_sync_total"}',
]
```

These attributes are focused on the health of the cluster based on the CPU/MEM environmental attributes. From this telemetry we hope to be able to determine the immediate functionality of the framework components and whether or not we have a correlation of issues across similar developer preview environmental characteristics. This information will allow us to immediately make changes to the OpenShift solution to improve our customer's experience and software resiliency.

We are extremely excited about showing you where the product is headed during this developer preview and we hope you will allow us this information to enhance the solution for all those involved.
