# UP

UP is a simple client that makes Prometheus remote-write requests.
The client writes the specified metric at the chosen interval.
The value of the metric will always be the current timestamp in milliseconds.

It can also read the value back and compare it against the current time.
If the duration is greater than 0s, it will evaluate number of errors and success
in the given duration against a percentage threshold and exit with zero or non-zero.

[![Build Status](https://cloud.drone.io/api/badges/observatorium/up/status.svg)](https://cloud.drone.io/observatorium/up)

## Getting Started

The easiest way to begin making remote write requests is to run the UP container.
For example, to report an `up` metric every 10 seconds, run:

```shell
docker run --rm -p 8080:8080 quay.io/observatorium/up --endpoint=https://example.com/api/v1/receive --period=10s
```

Note that the metric name and labels are customizable.
For example, to report a metric named `foo` with a custom `bar` label, run:

```shell
docker run --rm -p 8080:8080 quay.io/observatorium/up --endpoint=https://example.com/api/v1/receive --period=10s --name foo --labels 'bar="baz"'
```

## Usage

[embedmd]:# (tmp/help.txt)
```txt
Usage of ./up:
  -duration duration
    	The duration of the up command to run until it stops. (default 5m0s)
  -endpoint-read string
    	The endpoint to which to make query requests.
  -endpoint-write string
    	The endpoint to which to make remote-write requests.
  -initial-query-delay duration
    	The time to wait before executing the first query. (default 5s)
  -labels value
    	The labels in addition to '__name__' that should be applied to remote-write requests.
  -latency duration
    	The maximum allowable latency between writing and reading. (default 15s)
  -listen string
    	The address on which internal server runs. (default ":8080")
  -log.level string
    	The log filtering level. Options: 'error', 'warn', 'info', 'debug'. (default "info")
  -name string
    	The name of the metric to send in remote-write requests. (default "up")
  -period duration
    	The time to wait between remote-write requests. (default 5s)
  -threshold float
    	The percentage of successful requests needed to succeed overall. 0 - 1. (default 0.9)
  -token string
    	The bearer token to set in the authorization header on remote-write requests.
```
