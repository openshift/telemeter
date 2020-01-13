module github.com/observatorium/up

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20190620085101-78d2af792bab

require (
	github.com/go-kit/kit v0.9.0
	github.com/gogo/protobuf v1.2.2-0.20190730201129-28a6bbf47e48
	github.com/golang/snappy v0.0.1
	github.com/oklog/run v1.0.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.1.0
	github.com/prometheus/client_model v0.0.0-20190812154241-14fe0d1b01d4
	github.com/prometheus/common v0.6.0
	github.com/prometheus/prometheus v0.0.0-20190819201610-48b2c9c8eae2
)

go 1.13
