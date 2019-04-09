# Sample Metrics

To understand what metrics are collected by the Telemeter service, we can replicate the request that the Telemeter client makes against a running OpenShift cluster's Prometheus service.
The Telemeter client makes an HTTP GET request to Prometheus' `/federate` endpoint with the [metrics match rules](https://github.com/openshift/telemeter/blob/master/jsonnet/telemeter/metrics.jsonnet) URL encoded as query parameters.

To start, find the URL for the Prometheus service running in the OpenShift cluster:
```shell
$ oc get route prometheus-k8s -n openshift-monitoring -o jsonpath="{.spec.host}"
```

Next, navigate to this URL and run the following query, which will
return the full set of metrics that the Telemeter client captures:
[embedmd]:# (telemeter_query txt)

For reference, here is an example response produced by a running OpenShift cluster:
[embedmd]:# (../test/timeseries.txt)
