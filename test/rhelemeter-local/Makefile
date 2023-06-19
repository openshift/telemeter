TEST_E2E_CERTS_DIR=./certs
RHEL_USERNAME?=redhat
RHEL_PASSWORD?=redhat

.PHONY: run-prometheus
run-prometheus:
	docker run -p 9090:9090 -v prometheus.yml:/etc/prometheus/prometheus.yml prom/prometheus --web.enable-remote-write-receiver --config.file=/etc/prometheus/prometheus.yml

.PHONY: generate-certs
generate-certs:
	../generate-e2e-certs.sh $(TEST_E2E_CERTS_DIR)

run-rhel:
	RHEL_USERNAME=$(RHEL_USERNAME) RHEL_PASSWORD=$(RHEL_PASSWORD)  vagrant up --provider virtualbox

add-vagrant-plugins:
	vagrant plugin install vagrant-registration
