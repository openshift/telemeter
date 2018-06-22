FROM openshift/origin-release:golang-1.9
COPY . /go/src/github.com/openshift/telemeter
RUN cd /go/src/github.com/openshift/telemeter && go build ./cmd/telemeter-client && go build ./cmd/telemeter-server

FROM centos:7
COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-client /usr/bin/telemeter-client
COPY --from=0 /go/src/github.com/openshift/telemeter/telemeter-server /usr/bin/telemeter-server