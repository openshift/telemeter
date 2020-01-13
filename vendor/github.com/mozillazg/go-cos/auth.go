package cos

import (
	"crypto/hmac"
	"crypto/sha1"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const sha1SignAlgorithm = "sha1"
const privateHeaderPrefix = "x-cos-"
const defaultAuthExpire = time.Hour

// 需要校验的 Headers 列表
var needSignHeaders = map[string]bool{
	"host":                         true,
	"range":                        true,
	"x-cos-acl":                    true,
	"x-cos-grant-read":             true,
	"x-cos-grant-write":            true,
	"x-cos-grant-full-control":     true,
	"response-content-type":        true,
	"response-content-language":    true,
	"response-expires":             true,
	"response-cache-control":       true,
	"response-content-disposition": true,
	"response-content-encoding":    true,
	"cache-control":                true,
	"content-disposition":          true,
	"content-encoding":             true,
	// "content-type":                   true,
	"content-length":                 true,
	"content-md5":                    true,
	"expect":                         true,
	"expires":                        true,
	"x-cos-content-sha1":             true,
	"x-cos-storage-class":            true,
	"if-modified-since":              true,
	"origin":                         true,
	"access-control-request-method":  true,
	"access-control-request-headers": true,
	"x-cos-object-type":              true,
	// "x-cos-security-token":           true,
}

// AuthTime 用于生成签名所需的 q-sign-time 和 q-key-time 相关参数
type AuthTime struct {
	SignStartTime time.Time
	SignEndTime   time.Time
	KeyStartTime  time.Time
	KeyEndTime    time.Time
}

// NewAuthTime 生成 AuthTime 的便捷函数
//
//   expire: 从现在开始多久过期.
func NewAuthTime(expire time.Duration) *AuthTime {
	if expire == time.Duration(0) {
		expire = defaultAuthExpire
	}
	signStartTime := time.Now()
	keyStartTime := signStartTime
	signEndTime := signStartTime.Add(expire)
	keyEndTime := signEndTime
	return &AuthTime{
		SignStartTime: signStartTime,
		SignEndTime:   signEndTime,
		KeyStartTime:  keyStartTime,
		KeyEndTime:    keyEndTime,
	}
}

// signString return q-sign-time string
func (a *AuthTime) signString() string {
	return fmt.Sprintf("%d;%d", a.SignStartTime.Unix(), a.SignEndTime.Unix())
}

// keyString return q-key-time string
func (a *AuthTime) keyString() string {
	return fmt.Sprintf("%d;%d", a.KeyStartTime.Unix(), a.KeyEndTime.Unix())
}

// newAuthorization 通过一系列步骤生成最终需要的 Authorization 字符串
//
// https://cloud.tencent.com/document/product/436/7778
func newAuthorization(auth Auth, req *http.Request, authTime AuthTime) string {
	secretKey := auth.SecretKey
	secretID := auth.SecretID
	signTime := authTime.signString()
	keyTime := authTime.keyString()
	signKey := calSignKey(secretKey, keyTime)

	formatHeaders, signedHeaderList := genFormatHeaders(req.Header)
	formatParameters, signedParameterList := genFormatParameters(req.URL.Query())
	formatString := genFormatString(req.Method, *req.URL, formatParameters, formatHeaders)

	stringToSign := calStringToSign(sha1SignAlgorithm, keyTime, formatString)
	signature := calSignature(signKey, stringToSign)

	return genAuthorization(
		secretID, signTime, keyTime, signature, signedHeaderList,
		signedParameterList,
	)
}

// AddAuthorizationHeader 给 req 增加签名信息
func AddAuthorizationHeader(secretID, secretKey string, req *http.Request, authTime *AuthTime) {
	auth := newAuthorization(Auth{
		SecretID:  secretID,
		SecretKey: secretKey,
	}, req, *authTime)
	req.Header.Set("Authorization", auth)
}

// calSignKey 计算 SignKey
func calSignKey(secretKey, keyTime string) string {
	digest := calHMACDigest(secretKey, keyTime, sha1SignAlgorithm)
	return fmt.Sprintf("%x", digest)
}

// calStringToSign 计算 StringToSign
func calStringToSign(signAlgorithm, signTime, formatString string) string {
	h := sha1.New()
	h.Write([]byte(formatString))
	return fmt.Sprintf("%s\n%s\n%x\n", signAlgorithm, signTime, h.Sum(nil))
}

// calSignature 计算 Signature
func calSignature(signKey, stringToSign string) string {
	digest := calHMACDigest(signKey, stringToSign, sha1SignAlgorithm)
	return fmt.Sprintf("%x", digest)
}

// genAuthorization 生成 Authorization
func genAuthorization(secretID, signTime, keyTime, signature string, signedHeaderList, signedParameterList []string) string {
	return strings.Join([]string{
		"q-sign-algorithm=" + sha1SignAlgorithm,
		"q-ak=" + secretID,
		"q-sign-time=" + signTime,
		"q-key-time=" + keyTime,
		"q-header-list=" + strings.Join(signedHeaderList, ";"),
		"q-url-param-list=" + strings.Join(signedParameterList, ";"),
		"q-signature=" + signature,
	}, "&")
}

// genFormatString 生成 FormatString
func genFormatString(method string, uri url.URL, formatParameters, formatHeaders string) string {
	formatMethod := strings.ToLower(method)
	formatURI := uri.Path

	return fmt.Sprintf("%s\n%s\n%s\n%s\n", formatMethod, formatURI,
		formatParameters, formatHeaders,
	)
}

// https://github.com/tencentyun/cos-nodejs-sdk-v5/blob/a1dad3e9e3776cd24c97975f3aa47631e5001ff0/sdk/util.js#L11
func camSafeURLEncode(s string) string {
	s = encodeURIComponent(s)
	s = strings.Replace(s, "!", "%21", -1)
	s = strings.Replace(s, "'", "%27", -1)
	s = strings.Replace(s, "(", "%28", -1)
	s = strings.Replace(s, ")", "%29", -1)
	s = strings.Replace(s, "*", "%2A", -1)
	return s
}

type valuesForSign map[string][]string

func (vs valuesForSign) Add(key, value string) {
	key = strings.ToLower(key)
	vs[key] = append(vs[key], value)
}

// https://cloud.tencent.com/document/product/436/7778
// https://github.com/tencentyun/cos-nodejs-sdk-v5/blob/a1dad3e9e3776cd24c97975f3aa47631e5001ff0/sdk/util.js#L42-L69
func (vs valuesForSign) Encode() string {
	var keys []string
	for k := range vs {
		keys = append(keys, k)
	}
	// 字典序排序
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		items := vs[k]
		sort.Strings(items)
		for _, v := range items {
			pairs = append(
				pairs,
				fmt.Sprintf("%s=%s", camSafeURLEncode(k), camSafeURLEncode(v)))
		}
	}
	// <key1>=<value1>&<key2>=<value2>
	return strings.Join(pairs, "&")
}

// genFormatParameters 生成 FormatParameters 和 SignedParameterList
func genFormatParameters(parameters url.Values) (formatParameters string, signedParameterList []string) {
	ps := valuesForSign{}
	for key, values := range parameters {
		key = strings.ToLower(key)
		for _, value := range values {
			ps.Add(key, value)
			signedParameterList = append(signedParameterList, key)
		}
	}

	formatParameters = ps.Encode()
	sort.Strings(signedParameterList)
	return
}

// genFormatHeaders 生成 FormatHeaders 和 SignedHeaderList
func genFormatHeaders(headers http.Header) (formatHeaders string, signedHeaderList []string) {
	hs := valuesForSign{}
	for key, values := range headers {
		key = strings.ToLower(key)
		for _, value := range values {
			if isSignHeader(key) {
				hs.Add(key, value)
				signedHeaderList = append(signedHeaderList, key)
			}
		}
	}

	formatHeaders = hs.Encode()
	sort.Strings(signedHeaderList)
	return
}

// HMAC 签名
func calHMACDigest(key, msg, signMethod string) []byte {
	var hashFunc func() hash.Hash
	switch signMethod {
	case "sha1":
		hashFunc = sha1.New
	default:
		hashFunc = sha1.New
	}
	h := hmac.New(hashFunc, []byte(key))
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func isSignHeader(key string) bool {
	for k, v := range needSignHeaders {
		if key == k && v {
			return true
		}
	}
	return strings.HasPrefix(key, privateHeaderPrefix)
}

// Auth 签名相关的认证信息
type Auth struct {
	SecretID  string
	SecretKey string
	// 签名多久过期，默认是 time.Hour
	Expire time.Duration
}

// AuthorizationTransport 给请求增加 Authorization header
type AuthorizationTransport struct {
	SecretID  string
	SecretKey string
	// 临时密钥: https://cloud.tencent.com/document/product/436/14048
	SessionToken string
	// 签名多久过期，默认是 time.Hour
	Expire time.Duration

	// 实际发送 http 请求的 http.RoundTripper，默认使用 http.DefaultTransport
	Transport http.RoundTripper
}

// RoundTrip implements the RoundTripper interface.
func (t *AuthorizationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 使用预签名授权 URL 时跳过添加 Authorization header 的步骤
	if req.URL.Query().Get("sign") == "" {
		req = cloneRequest(req) // per RoundTrip contract

		// 增加 Authorization header
		authTime := NewAuthTime(t.Expire)
		AddAuthorizationHeader(t.SecretID, t.SecretKey, req, authTime)
		if t.SessionToken != "" {
			req.Header.Set("x-cos-security-token", t.SessionToken)
		}
	}

	resp, err := t.transport().RoundTrip(req)
	return resp, err
}

func (t *AuthorizationTransport) transport() http.RoundTripper {
	if t.Transport != nil {
		return t.Transport
	}
	return http.DefaultTransport
}
