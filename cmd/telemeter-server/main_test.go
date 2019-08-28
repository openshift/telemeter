package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/http/server"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/store/memstore"
	"github.com/openshift/telemeter/pkg/validate"
)

const (
	sampleMetrics = `
openshift_build_info{app="openshift-web-console",gitCommit="d911956",gitVersion="v3.10.0-alpha.0+d911956-1-dirty",instance="172.16.0.14:8443",job="kubernetes-service-endpoints",kubernetes_name="webconsole",kubernetes_namespace="openshift-web-console",major="3",minor="10+"} 1 1526160578685
openshift_build_info{gitCommit="32ac7fa",gitVersion="v3.10.0-alpha.0+32ac7fa-390",instance="10.142.0.3:1936",job="openshift-router",major="3",minor="10+"} 1 1526160588751
openshift_build_info{gitCommit="865022c",gitVersion="v3.10.0-alpha.0+865022c-1018",instance="10.142.0.3:8443",job="kubernetes-apiservers",major="3",minor="10+"} 1 1526160587593
openshift_build_info{gitCommit="865022c",gitVersion="v3.10.0-alpha.0+865022c-1018",instance="10.142.0.3:8444",job="kubernetes-controllers",major="3",minor="10+"} 1 1526160600448
`
	missingTimestamp = `
openshift_build_info{app="openshift-web-console",gitCommit="d911956",gitVersion="v3.10.0-alpha.0+d911956-1-dirty",instance="172.16.0.14:8443",job="kubernetes-service-endpoints",kubernetes_name="webconsole",kubernetes_namespace="openshift-web-console",major="3",minor="10+"} 1
`
)

func TestPost(t *testing.T) {
	registry := prometheus.NewRegistry()

	validator := validate.New("cluster", 0, 0)
	labels := map[string]string{"cluster": "test"}
	testPost(t, registry, validator, withLabels(sort(mustReadString(sampleMetrics)), labels), withLabels(sort(mustReadString(sampleMetrics)), labels))
}

func TestPostError(t *testing.T) {
	registry := prometheus.NewRegistry()

	validator := validate.New("cluster", 4096, 0)
	ttl := 10 * time.Minute
	store := memstore.New(registry, ttl)
	server := server.New(store, validator, nil, ttl)
	labels := map[string]string{"cluster": "test"}

	s := httptest.NewServer(fakeAuthorizeHandler(http.HandlerFunc(server.Post), &authorize.Client{ID: "test", Labels: labels}))
	defer s.Close()

	longName := strings.Repeat("abcd", 2048)

	testCases := []struct {
		name   string
		send   []*clientmodel.MetricFamily
		expect string
	}{
		{name: "without cluster ID", send: sort(mustReadString(sampleMetrics)), expect: "a required label is missing from the metric"},
		{name: "out of order", send: withLabels(mustReadString(sampleMetrics), labels), expect: "are not in increasing timestamp order"},
		{name: "lack timestamp", send: withLabels(mustReadString(missingTimestamp), labels), expect: "do not have a timestamp"},
		{name: "too large", send: []*clientmodel.MetricFamily{{Name: &longName}}, expect: "incoming sample data is too long"},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			code, body := mustPostError(s.URL, expfmt.FmtProtoDelim, test.send)
			if code != http.StatusInternalServerError {
				t.Errorf("unexpected code: %d", code)
			}
			if !strings.Contains(body, test.expect) {
				t.Errorf("unexpected body: %s", body)
			}
		})
	}

}

func testPost(t *testing.T, registry *prometheus.Registry, validator validate.Validator, send, expect []*clientmodel.MetricFamily) {
	t.Helper()

	ttl := 10 * time.Minute
	memStore := memstore.New(registry, ttl)
	server := server.New(memStore, validator, nil, ttl)

	s := httptest.NewServer(fakeAuthorizeHandler(http.HandlerFunc(server.Post), &authorize.Client{ID: "test", Labels: map[string]string{"cluster": "test"}}))
	defer s.Close()

	mustPost(s.URL, expfmt.FmtProtoDelim, send)

	var actual []*clientmodel.MetricFamily
	ps, err := memStore.ReadMetrics(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	p := ps[0]
	if p.PartitionKey != "test" {
		t.Fatalf("unexpected partition key: %s", p.PartitionKey)
	}
	actual = p.Families

	if e, a := metricsAsStringOrDie(expect), metricsAsStringOrDie(actual); e != a {
		t.Errorf("expected:\n%s\nactual:\n%s", e, a)
	}
}

func TestGet(t *testing.T) {
	registry := prometheus.NewRegistry()

	ttl := 10 * time.Minute
	memStore := memstore.New(registry, ttl)
	validator := validate.New("cluster", 0, 0)
	server := server.NewNonExpiring(memStore, validator, nil, ttl)
	srv := httptest.NewServer(http.HandlerFunc(server.Get))
	defer srv.Close()

	if err := memStore.WriteMetrics(context.Background(), &store.PartitionedMetrics{
		PartitionKey: "test",
		Families:     mustReadString(sampleMetrics),
	}); err != nil {
		t.Fatal(err)
	}

	actual := mustGet(srv.URL, expfmt.FmtText)
	expected := mustReadString(sampleMetrics)

	for _, mf := range expected {
		for _, m := range mf.Metric {
			m.TimestampMs = nil
		}
	}

	if e, a := metricsAsStringOrDie(expected), metricsAsStringOrDie(actual); e != a {
		t.Errorf("unexpected output metrics:\n%s\n%s", e, a)
	}
}

func sort(families []*clientmodel.MetricFamily) []*clientmodel.MetricFamily {
	_ = metricfamily.Filter(families, metricfamily.TransformerFunc(metricfamily.SortMetrics))
	return metricfamily.Pack(families)
}

func withLabels(families []*clientmodel.MetricFamily, labels map[string]string) []*clientmodel.MetricFamily {
	_ = metricfamily.Filter(families, metricfamily.NewLabel(labels, nil))
	return families
}

func metricsAsStringOrDie(families []*clientmodel.MetricFamily) string {
	buf := &bytes.Buffer{}
	encoder := expfmt.NewEncoder(buf, expfmt.FmtText)
	for _, family := range families {
		if family == nil {
			continue
		}
		if len(family.Metric) == 0 {
			continue
		}
		if err := encoder.Encode(family); err != nil {
			panic(err)
		}
	}
	return buf.String()
}

func mustReadString(metrics string) []*clientmodel.MetricFamily {
	return mustRead(bytes.NewBufferString(metrics), expfmt.FmtText)
}

func mustRead(r io.Reader, format expfmt.Format) []*clientmodel.MetricFamily {
	decoder := expfmt.NewDecoder(r, format)
	families := make([]*clientmodel.MetricFamily, 0)
	for {
		family := clientmodel.MetricFamily{}
		if err := decoder.Decode(&family); err != nil {
			if err == io.EOF {
				break
			}
			panic(err)
		}
		families = append(families, &family)
	}
	return families
}

func mustPostError(addr string, format expfmt.Format, families []*clientmodel.MetricFamily) (int, string) {
	buf := &bytes.Buffer{}
	encoder := expfmt.NewEncoder(buf, format)
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			panic(err)
		}
	}
	req, err := http.NewRequest("POST", addr, buf)
	if err != nil {
		panic(err)
	}
	req.Header.Add("Content-Type", string(format))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	if resp.StatusCode == http.StatusOK {
		panic(fmt.Errorf("unexpected code %d", resp.StatusCode))
	}
	body, _ := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(body)
}

func mustPost(addr string, format expfmt.Format, families []*clientmodel.MetricFamily) {
	buf := &bytes.Buffer{}
	encoder := expfmt.NewEncoder(buf, format)
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			panic(err)
		}
	}
	req, err := http.NewRequest("POST", addr, buf)
	if err != nil {
		panic(err)
	}
	req.Header.Add("Content-Type", string(format))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		panic(fmt.Errorf("unexpected code %d: %s", resp.StatusCode, string(body)))
	}
	resp.Body.Close()
}

func mustGet(addr string, format expfmt.Format) []*clientmodel.MetricFamily {
	req, err := http.NewRequest("GET", addr, nil)
	if err != nil {
		panic(err)
	}
	req.Header.Add("Accept", string(format))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	return mustRead(resp.Body, expfmt.ResponseFormat(resp.Header))
}

func fakeAuthorizeHandler(h http.Handler, client *authorize.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(authorize.WithClient(req.Context(), client))
		h.ServeHTTP(w, req)
	})
}
