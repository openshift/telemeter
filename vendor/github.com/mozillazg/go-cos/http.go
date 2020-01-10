package cos

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
)

// Sender 定义了一个用来发送 http 请求的接口。
// 可以用于替换默认的基于 http.Client 的实现，
// 从而实现使用第三方 http client 或写单元测试时 mock 接口结果的需求。
//
// 实现自定义的 Sender 时可以参考 DefaultSender 的实现。
type Sender interface {
	// caller 中包含了从哪个方法触发的 http 请求的信息
	// 当 error != nil 时将不会调用 ResponseParser.ParseResponse 解析响应
	Send(ctx context.Context, caller Caller, req *http.Request) (*http.Response, error)
}

// ResponseParser 定义了一个用于解析响应的接口（反序列化 body 或错误检查）。
// 可以用于替换默认的解析响应的实现，
// 从而实现使用自定义的解析方法或写单元测试时 mock 接口结果的需求
//
// 实现自定义的 ResponseParser 时可以参考 DefaultResponseParser 的实现。
type ResponseParser interface {
	// caller 中包含了从哪个方法触发的 http 请求的信息
	// result: 反序列化后的结果将存储在指针类型的 result 中
	ParseResponse(ctx context.Context, caller Caller, resp *http.Response, result interface{}) (*Response, error)
}

// DefaultSender 是基于 http.Client 的默认 Sender 实现
type DefaultSender struct {
	*http.Client
}

// Send 发送 http 请求
func (s *DefaultSender) Send(ctx context.Context, caller Caller, req *http.Request) (*http.Response, error) {
	resp, err := s.Do(req)
	if err != nil {
		// If we got an error, and the context has been canceled,
		// the context's error is probably more useful.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		return nil, err
	}

	return resp, err
}

// DefaultResponseParser 是默认的 ResponseParser 实现
type DefaultResponseParser struct{}

// ParseResponse 解析响应内容，反序列化后的结果将存储在指针类型的 result 中
func (p *DefaultResponseParser) ParseResponse(ctx context.Context, caller Caller, resp *http.Response, result interface{}) (*Response, error) {
	response := newResponse(resp)

	err := checkResponse(resp)
	if err != nil {
		// even though there was an error, we still return the response
		// in case the caller wants to inspect it further
		resp.Body.Close()
		return response, err
	}

	if result != nil {
		if w, ok := result.(io.Writer); ok {
			_, err = io.Copy(w, resp.Body)
		} else {
			err = xml.NewDecoder(resp.Body).Decode(result)
			if err == io.EOF {
				err = nil // ignore EOF errors caused by empty response body
			}
		}
	}

	return response, err
}

// MethodName 用于 Caller 中表示调用的是哪个方法
type MethodName string

// Caller 方法调用信息，用于 Sender 和 ResponseParser 中判断是来自哪个方法的调用
type Caller struct {
	// 调用的方法名称
	Method MethodName
}
