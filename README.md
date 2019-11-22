Telemeter
=========

Telemeter implements a Prometheus federation push client and server
to allow isolated Prometheus instances that cannot be scraped from a
central Prometheus to instead perform push federation to a central
location.

1. The local client scrapes `/federate` on a given Prometheus instance.
2. The local client performs cleanup and anonymization and then pushes the metrics to the server.
3. The server authenticates the client, validates and verifies that the metrics are "safe", and then ensures they have a label uniquely identifying the source client.
4. The server holds the metrics in a local disk store until scraped.
5. A centralized Prometheus scrapes each server instance and aggregates all the metrics.

Since that push is across security boundaries, the server must perform
authentication, authorization, and data integrity checks as well as being
resilient to denial of service.

Each client is uniquely identified by a cluster ID and all metrics
federated are labelled with that ID.

Since Telemeter is dependent on Prometheus federation, each server
instance must ensure that all metrics for a given cluster ID are routed
to the same instance, otherwise Prometheus will mark those metrics
series as stale. To do this, the server instances form a cluster using
a secure gossip transport and build a consistent hash ring so that
pushed client metrics are routed internally to the same server.

For resiliency, each server instance stores the received metrics on disk
hashed by cluster ID until they are accessed by a federation endpoint.

note: Telemeter is alpha and may change significantly

Get started
-----------

To see this in action, run

```
make
./test/integration.sh http://localhost:9005
```

The command launches a two instance `telemeter-server` cluster and a single
`telemeter-client` to talk to that server, along with a Prometheus
instance running on http://localhost:9005 that shows the federated metrics.
The client will scrape metrics from the local prometheus, then send those
to the telemeter server cluster, which will then be scraped by that instance.

To run this test against another Prometheus server, change the URL (and if necessary,
specify the bearer token necessary to talk to that server as the second argument).

To build binaries, run

```
make
```

To execute the unit test suite, run

```
make check
```

To launch a self contained integration test, run:

```
make test-integration
```

Adding new metrics to send via telemeter
-----------

Clone repository locally and in the root of the directory make the following changes:

1. Add the metric to the [jsonnet/telemeter/metrics.jsonnet](./jsonnet/telemeter/metrics.jsonnet) file.
2. Commit the changes.
3. In the root of the directory run the following make target to regenerate the files:
```
make generate-in-docker
```
4. Commit the generated files.

*Note:* Further docs on the process on why and how to send these metrics are available [here](https://docs.google.com/document/d/1a6n5iBGM2QaIQRg9Lw4-Npj6QY9--Hpx3XYut-BrUSY/edit?usp=sharing).