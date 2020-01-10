module github.com/openshift/telemeter

go 1.13

require (
	github.com/bradfitz/gomemcache v0.0.0-20190913173617-a41fca850d0b
	github.com/brancz/gojsontoyaml v0.0.0-20191212081931-bf2969bbd742
	github.com/campoy/embedmd v1.0.0
	github.com/coreos/go-oidc v2.0.0+incompatible
	github.com/go-kit/kit v0.9.0
	github.com/gogo/protobuf v1.3.1
	github.com/golang/protobuf v1.3.2
	github.com/golang/snappy v0.0.1
	github.com/hashicorp/go-msgpack v0.5.5
	github.com/hashicorp/memberlist v0.1.5
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/jsonnet-bundler/jsonnet-bundler v0.2.0
	github.com/observatorium/up v0.0.0-20191211124247-2187e5c6701d
	github.com/oklog/run v1.0.0
	github.com/pkg/errors v0.8.1
	github.com/pquerna/cachecontrol v0.0.0-20180517163645-1555304b9b35 // indirect
	github.com/prometheus/client_golang v1.2.1
	github.com/prometheus/client_model v0.0.0-20190812154241-14fe0d1b01d4
	github.com/prometheus/common v0.7.0
	github.com/prometheus/prometheus v1.8.2-0.20191126064551-80ba03c67da1 // Prometheus master v2.14.0
	github.com/satori/go.uuid v1.2.1-0.20181028125025-b2ce2384e17b
	github.com/serialx/hashring v0.0.0-20180504054112-49a4782e9908
	github.com/spf13/cobra v0.0.3
	github.com/thanos-io/thanos v0.9.0
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0
	gopkg.in/square/go-jose.v2 v2.0.0-20180411045311-89060dee6a84
)

// Mitigation for: https://github.com/Azure/go-autorest/issues/414
replace github.com/Azure/go-autorest => github.com/Azure/go-autorest v12.3.0+incompatible
