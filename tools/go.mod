module github.com/openshift/telemeter/tools

go 1.14

require (
	github.com/brancz/gojsontoyaml v0.0.0-20200602132005-3697ded27e8c
	github.com/campoy/embedmd v1.0.0
	github.com/google/go-jsonnet v0.16.0
	github.com/jsonnet-bundler/jsonnet-bundler v0.4.0
	github.com/observatorium/up v0.0.0-20200615121732-d763595ede50
	github.com/thanos-io/thanos v0.13.0
)

// Mitigation for: https://github.com/Azure/go-autorest/issues/414
replace github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.3+incompatible
