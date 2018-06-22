Telemeter
=========

Telemeter implements a Prometheus federation push client and server -
allowing isolated Prometheus instances that cannot be scraped from a
central Prometheus to instead perform push federation to a central
location. The local client scrapes `/federate` on a given instance,
pushes that to a remote server, which then validates and verifies that
the metrics are "safe" before offering them up to be scraped by a
centralized Prometheus. Since that push is across security boundaries,
the server must perform authentication, authorization, and data
integrity checks as well as being resilient to denial of service.

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

To see this run locally, run

```
make
```

to build the binaries, then

```
hack/test.sh <URL_TO_PROMETHEUS_FEDERATE_ENDPOINT>
[<AUTH_TOKEN_TO_PROMETHEUS>|""]
```

to launch a two instance `telemeter-server` cluster and a single
`telemeter-client` to talk to that server, along with a Prometheus
instance running on `localhost:9005` that shows the federated metrics.
