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

	"github.com/go-kit/kit/log"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/http/server"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store/memstore"
	"github.com/openshift/telemeter/pkg/validate"
)

const (
	sampleMetrics = `
openshift_build_info{app="openshift-web-console",gitCommit="d911956",gitVersion="v3.10.0-alpha.0+d911956-1-dirty",instance="172.16.0.14:8443",job="kubernetes-service-endpoints",kubernetes_name="webconsole",kubernetes_namespace="openshift-web-console",major="3",minor="10+"} 1 1568970000000
openshift_build_info{gitCommit="32ac7fa",gitVersion="v3.10.0-alpha.0+32ac7fa-390",instance="10.142.0.3:1936",job="openshift-router",major="3",minor="10+"} 1 1568970000000
openshift_build_info{gitCommit="865022c",gitVersion="v3.10.0-alpha.0+865022c-1018",instance="10.142.0.3:8443",job="kubernetes-apiservers",major="3",minor="10+"} 1 1568970000000
openshift_build_info{gitCommit="865022c",gitVersion="v3.10.0-alpha.0+865022c-1018",instance="10.142.0.3:8444",job="kubernetes-controllers",major="3",minor="10+"} 1 1568970000000
`
	missingTimestamp = `
openshift_build_info{app="openshift-web-console",gitCommit="d911956",gitVersion="v3.10.0-alpha.0+d911956-1-dirty",instance="172.16.0.14:8443",job="kubernetes-service-endpoints",kubernetes_name="webconsole",kubernetes_namespace="openshift-web-console",major="3",minor="10+"} 1
`
)

var (
	//1568969001000
	now = func() time.Time { return time.Date(2019, 9, 20, 9, 0, 0, 0, time.UTC) }
)

func TestPost(t *testing.T) {
	validator := validate.New("cluster", 0, 0, now)
	labels := map[string]string{"cluster": "test"}

	send := withLabels(sort(mustReadString(sampleMetrics)), labels)
	expect := withLabels(sort(mustReadString(sampleMetrics)), labels)

	memStore := memstore.New(10 * time.Minute)

	s := httptest.NewServer(fakeAuthorizeHandler(
		server.Post(log.NewNopLogger(), memStore, validator, nil),
		&authorize.Client{ID: "test", Labels: map[string]string{"cluster": "test"}},
	))
	defer s.Close()

	format := expfmt.FmtProtoDelim

	buf := &bytes.Buffer{}
	encoder := expfmt.NewEncoder(buf, format)
	for _, family := range send {
		if err := encoder.Encode(family); err != nil {
			t.Fatal(err)
		}
	}

	req, err := http.NewRequest(http.MethodPost, s.URL, buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("Content-Type", string(format))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		t.Fatalf("unexpected code %d: %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	ps, err := memStore.ReadMetrics(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if e, a := metricsAsStringOrDie(expect), metricsAsStringOrDie(ps[0].Families); e != a {
		t.Errorf("expected:\n%s\nactual:\n%s", e, a)
	}

}

func TestPostError(t *testing.T) {
	validator := validate.New("cluster", 4096, 0, now)
	ttl := 10 * time.Minute
	store := memstore.New(ttl)
	labels := map[string]string{"cluster": "test"}

	s := httptest.NewServer(fakeAuthorizeHandler(server.Post(log.NewNopLogger(), store, validator, nil), &authorize.Client{ID: "test", Labels: labels}))
	defer s.Close()

	longName := strings.Repeat("abcd", 2048)

	testCases := []struct {
		name   string
		send   []*clientmodel.MetricFamily
		expect string
	}{
		{name: "without cluster ID", send: sort(mustReadString(sampleMetrics)), expect: "a required label is missing from the metric"},
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

func fakeAuthorizeHandler(h http.Handler, client *authorize.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(authorize.WithClient(req.Context(), client))
		h.ServeHTTP(w, req)
	})
}
