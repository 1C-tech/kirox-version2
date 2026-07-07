package http

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strconv"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	DefaultUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36"
	DefaultSecUA = `"Chromium";v="137", "Not/A)Brand";v="24", "Google Chrome";v="137"`
)

// Hex4 生成 4 位随机十六进制
func Hex4() string {
	const chars = "0123456789abcdef"
	b := make([]byte, 4)
	for i := range b {
		b[i] = chars[rand.Intn(16)]
	}
	return string(b)
}

// Awsccc 生成 awsccc cookie 值
func Awsccc() string {
	d := map[string]interface{}{
		"e": 1, "p": 1, "f": 1, "a": 1,
		"i": fmt.Sprintf("%s-%s-4%s-%s-%s%s%s",
			Hex4()+Hex4(), Hex4(), Hex4()[1:], Hex4(), Hex4(), Hex4(), Hex4()),
		"v": "1",
	}
	b, _ := json.Marshal(d)
	return base64.StdEncoding.EncodeToString(b)
}

// UbidGen 生成 ubid cookie 值
func UbidGen() string {
	d7 := make([]byte, 7)
	d6 := make([]byte, 6)
	for i := range d7 {
		d7[i] = byte('0' + rand.Intn(10))
	}
	for i := range d6 {
		d6[i] = byte('0' + rand.Intn(10))
	}
	return fmt.Sprintf("186-%s-%s", string(d7), string(d6))
}

// VisitorID 生成随机 visitor ID
func VisitorID() string {
	return fmt.Sprintf("%s%s-%s-7%s-%s-%s%s%s",
		Hex4(), Hex4(), Hex4(), Hex4()[1:], Hex4(), Hex4(), Hex4(), Hex4())
}

// PKCE 生成 PKCE code_verifier 和 code_challenge
func PKCE() (verifier, challenge string) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(rand.Intn(256))
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// resolveProfile 根据 Chrome 主版本号映射 TLS profile
func resolveProfile(majorVer int) profiles.ClientProfile {
	// tls-client profiles 映射表
	// 可用 profiles:
	//   Chrome_103/104/105/106/107/108/109/110/111/112
	//   Chrome_116_PSK(特殊) Chrome_117
	//   Chrome_120 Chrome_124
	//   Chrome_130_PSK(特殊) Chrome_131 Chrome_131_PSK
	//   Chrome_133 Chrome_133_PSK
	//   Chrome_144 Chrome_144_PSK
	//   Chrome_146 Chrome_146_PSK
	switch {
	case majorVer >= 146:
		return profiles.Chrome_146
	case majorVer >= 144:
		return profiles.Chrome_144
	case majorVer >= 133:
		return profiles.Chrome_133
	case majorVer >= 131:
		return profiles.Chrome_131
	case majorVer >= 124:
		return profiles.Chrome_124
	case majorVer >= 120:
		return profiles.Chrome_120
	case majorVer >= 117:
		return profiles.Chrome_117
	default:
		return profiles.Chrome_112
	}
}

// parseChromeMajorVer 从 chromeVer 字符串中解析主版本号
// 支持格式: "144", "144.0.0.0", "137.0.0.0"
func parseChromeMajorVer(chromeVer string) int {
	if chromeVer == "" {
		return 144
	}
	parts := strings.SplitN(chromeVer, ".", 2)
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 100 {
		return 144
	}
	return major
}

// NewTLSClient 创建带 TLS 指纹伪装的 HTTP 客户端
// chromeVer 可选，指定 Chrome 主版本号（如 "144"、"137.0.0.0"），省略时默认 Chrome_144
func NewTLSClient(proxy string, followRedirect bool, chromeVer ...string) tls_client.HttpClient {
	ver := 144
	if len(chromeVer) > 0 && chromeVer[0] != "" {
		ver = parseChromeMajorVer(chromeVer[0])
	}
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(60),
		tls_client.WithClientProfile(resolveProfile(ver)),
		tls_client.WithInsecureSkipVerify(),
	}
	if !followRedirect {
		opts = append(opts, tls_client.WithNotFollowRedirects())
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		panic(fmt.Sprintf("创建 TLS 客户端失败: %v", err))
	}
	if proxy != "" {
		client.SetProxy(proxy)
	}
	return client
}

// NewNoRedirectTLSClient 创建不跟随重定向的 TLS 客户端
func NewNoRedirectTLSClient(proxy string, chromeVer ...string) tls_client.HttpClient {
	return NewTLSClient(proxy, false)
}

// ExtractParam 从 URL 中提取查询参数
func ExtractParam(rawURL, key string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}

// SplitAfter 从字符串中提取分隔符后的内容
func SplitAfter(s, sep string) string {
	idx := strings.Index(s, sep)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(sep):]
	if i := strings.IndexByte(rest, '&'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// GetNestedMap 获取嵌套 map
func GetNestedMap(data map[string]interface{}, keys ...string) map[string]interface{} {
	current := data
	for _, k := range keys {
		next, ok := current[k].(map[string]interface{})
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

// GetNestedStringMap 获取嵌套的 string map
func GetNestedStringMap(data map[string]interface{}, key string) map[string]string {
	if data == nil {
		return nil
	}
	nested, ok := data[key].(map[string]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]string)
	for k, v := range nested {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// SetHeaders 设置请求头 (保持顺序)
func SetHeaders(req *fhttp.Request, headers map[string]string) {
	var order []string
	for k, v := range headers {
		req.Header.Set(k, v)
		order = append(order, strings.ToLower(k))
	}
	req.Header[fhttp.HeaderOrderKey] = order
}

// TraceHeaders 生成 Datadog APM 追踪头
func TraceHeaders() map[string]string {
	traceID := fmt.Sprintf("%016x%016x", rand.Uint64(), rand.Uint64())
	parentID := fmt.Sprintf("%016x", rand.Uint64())
	return map[string]string{
		"traceparent":                 "00-" + traceID + "-" + parentID + "-01",
		"tracestate":                  "dd=s:1;o:rum",
		"x-datadog-origin":            "rum",
		"x-datadog-parent-id":         parentID,
		"x-datadog-sampling-priority": "1",
		"x-datadog-trace-id":          strconv.FormatUint(rand.Uint64(), 10),
	}
}

// SaveCookies 从 Set-Cookie 头中提取并保存 cookies
func SaveCookies(cookies map[string]string, headers map[string][]string) {
	skip := map[string]bool{
		"path": true, "domain": true, "expires": true,
		"max-age": true, "secure": true, "httponly": true, "samesite": true,
	}
	for _, vals := range headers {
		for _, raw := range vals {
			if !strings.Contains(raw, "=") {
				continue
			}
			kv := strings.SplitN(strings.Split(raw, ";")[0], "=", 2)
			if len(kv) == 2 {
				k := strings.TrimSpace(kv[0])
				v := strings.TrimSpace(kv[1])
				if !skip[strings.ToLower(k)] && k != "" {
					cookies[k] = v
				}
			}
		}
	}
}
