package cos

import (
	"context"
	"encoding/xml"
	"net/http"
)

// ServiceService ...
//
// Service 相关 API
type ServiceService service

// ServiceGetResult ...
type ServiceGetResult struct {
	XMLName xml.Name `xml:"ListAllMyBucketsResult"`
	Owner   *Owner   `xml:"Owner"`
	Buckets []Bucket `xml:"Buckets>Bucket,omitempty"`
}

// MethodServiceGet method name of Service.Get
const MethodServiceGet MethodName = "Service.Get"

// Get Service 接口是用来获取请求者名下的所有存储空间列表（Bucket list）。
//
// https://cloud.tencent.com/document/product/436/8291
func (s *ServiceService) Get(ctx context.Context) (*ServiceGetResult, *Response, error) {
	var res ServiceGetResult
	sendOpt := sendOptions{
		baseURL: s.client.BaseURL.ServiceURL,
		uri:     "/",
		method:  http.MethodGet,
		result:  &res,
		caller: Caller{
			Method: MethodServiceGet,
		},
	}
	resp, err := s.client.send(ctx, &sendOpt)
	return &res, resp, err
}
