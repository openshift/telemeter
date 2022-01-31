FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.17-openshift-4.11
ENV GOFLAGS="-mod=vendor"
COPY . /go/src/github.com/openshift/telemeter
RUN cd /go/src/github.com/openshift/telemeter && \
    go build ./cmd/telemeter-client && \
    go build ./cmd/telemeter-server && \
    go build ./cmd/authorization-server

FROM registry.ci.openshift.org/ocp/4.11:base
LABEL io.k8s.display-name="OpenShift Telemeter" \
      io.k8s.description="" \
      io.openshift.tags="openshift,monitoring" \
      summary="" \
      maintainer="OpenShift Monitoring Team <team-monitoring@redhat.com>"

COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-client /usr/bin/
COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-server /usr/bin/
COPY --from=0 /go/src/github.com/openshift/telemeter/authorization-server /usr/bin/
