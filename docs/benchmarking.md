# Benchmarking Telemeter

The goal of the benchmarking suite is to determine the capability of the Telemeter stack to successfully handle client requests.
Identifying the functional limit of a given configuration allows the Telemeter team to predict the stack's ability to meet its design requirements as well as quantify the acceptance of new metrics into the Telemeter pipeline.
This document explains the design and operation of the Telemeter benchmarking suite as well as the results and analysis of current benchmarking tests.

## Prerequisites

An existing OpenShift 4.0 cluster is required in order to run the benchmarking suite.
The cluster must have a properly configured wildcard DNS record, or DNS controller, and ingress controller so that any created routes correctly forward traffic to their corresponding services.
Without this, the stack cannot equitably distribute client requests across the replicas in the StatefulSet and the test could result in one overloaded instance forwarding requests to the rest of the cluster, causing the test to fail prematurely.
A standard 4.0 cluster created with the [OpenShift Installer](https://github.com/openshift/installer) on AWS is sufficient.

Generating the CPU and memory utilization plots for Telemeter server requires the following tools:
* jq
* python3
* ghostscript
* matplotlib

## Running

To run the benchmarking suite:
1. create an OpenShift cluster
2. `export KUBECONFIG=...`
3. `make test-benchmark`

The benchmarking suite will generate CPU and memory utilization results for the test run.
To generate plots for these results, run invoke the following Makefile target:
```shell
make benchmark.pdf
```

## Further Reading

The test/timeseries.txt file contains the time series used by the telemeter-benchmark binary to create payloads to send to the Telemeter server.

To regenerate the time series list:
1. create an OpenShift cluster
2. `export KUBECONFIG=...`
3. `make test/timeseries.txt`
