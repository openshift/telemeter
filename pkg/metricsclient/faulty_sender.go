package metricsclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"

	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type FaultySender struct {
	client      *http.Client
	timeout     time.Duration
	metricsName string
}

func NewFaultySender(client *http.Client, maxBytes int64, timeout time.Duration, metricsName string) *FaultySender {
	return &FaultySender{
		client:      client,
		timeout:     timeout,
		metricsName: metricsName,
	}
}

func (c *FaultySender) Send(ctx context.Context, req *http.Request, families []*clientmodel.MetricFamily) error {
	buf := &bytes.Buffer{}
	if _, err := buf.Write([]byte{0xd, 0xe, 0xa, 0xd, 0xb, 0xe, 0xe, 0xf}); err != nil {
		return err
	}

	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
	req.Header.Set("Content-Encoding", "snappy")
	req.Body = ioutil.NopCloser(buf)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	req = req.WithContext(ctx)
	defer cancel()

	return withCancel(ctx, c.client, req, func(resp *http.Response) error {
		defer func() {
			if _, err := io.Copy(ioutil.Discard, resp.Body); err != nil {
				log.Printf("error copying body: %v", err)
			}
			resp.Body.Close()
		}()

		switch resp.StatusCode {
		case http.StatusOK:
			gaugeRequestSend.WithLabelValues(c.metricsName, "200").Inc()
		case http.StatusUnauthorized:
			gaugeRequestSend.WithLabelValues(c.metricsName, "401").Inc()
			return fmt.Errorf("gateway server requires authentication: %s", resp.Request.URL)
		case http.StatusForbidden:
			gaugeRequestSend.WithLabelValues(c.metricsName, "403").Inc()
			return fmt.Errorf("gateway server forbidden: %s", resp.Request.URL)
		case http.StatusBadRequest:
			gaugeRequestSend.WithLabelValues(c.metricsName, "400").Inc()
			return fmt.Errorf("gateway server bad request: %s", resp.Request.URL)
		default:
			gaugeRequestSend.WithLabelValues(c.metricsName, strconv.Itoa(resp.StatusCode)).Inc()
			body, _ := ioutil.ReadAll(resp.Body)
			if len(body) > 1024 {
				body = body[:1024]
			}
			return fmt.Errorf("gateway server reported unexpected error code: %d: %s", resp.StatusCode, string(body))
		}

		return nil
	})
}
