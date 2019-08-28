FROM openshift/origin-release:golang-1.12
COPY . /go/src/github.com/openshift/telemeter
RUN cd /go/src/github.com/openshift/telemeter && \
    go build ./cmd/telemeter-benchmark && \
    go build ./cmd/telemeter-client && \
    go build ./cmd/telemeter-server && \
    go build ./cmd/authorization-server

FROM centos:7
COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-benchmark /usr/bin/
COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-client /usr/bin/
COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-server /usr/bin/
COPY --from=0 /go/src/github.com/openshift/telemeter/authorization-server /usr/bin/
