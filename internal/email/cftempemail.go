package email

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CFTempEmailConfig Cloudflare 临时邮箱配置
// 对应 dreamhunter2333/cloudflare_temp_email Worker 的 admin API
type CFTempEmailConfig struct {
	Name      string   `json:"name"`      // 配置名（用户自定义）
	URL       string   `json:"url"`       // Worker 基础 URL，例：https://xxx.workers.dev
	AdminAuth string   `json:"adminAuth"` // Admin 鉴权密码（x-admin-auth header）
	Domains   []string `json:"domains"`   // 可用域名列表（手动填写）
}

// CFTempEmailClient CF 临时邮箱 HTTP 客户端
type CFTempEmailClient struct {
	config CFTempEmailConfig
	client *http.Client
}

// cfTempEmailMessage CF Worker 响应中的单条邮件
// API 返回格式: {"id":int, "message_id":str, "source":str, "address":str, "raw":str, "created_at":str}
// 所有邮件内容（headers + MIME body）都在 Raw 字段中，无独立的 from/to/subject/text/html
type cfTempEmailMessage struct {
	ID        int64  `json:"id"`
	MessageID string `json:"message_id"`
	Source    string `json:"source"`
	Address   string `json:"address"`
	Raw       string `json:"raw"`
	CreatedAt string `json:"created_at"`
}

// cfTempEmailListResp /api/mails 或 /admin/mails 响应
type cfTempEmailListResp struct {
	Results []cfTempEmailMessage `json:"results"`
	Total   int                  `json:"total"`
}

// cfTempEmailCreateResp /admin/new_address 响应
type cfTempEmailCreateResp struct {
	Address string `json:"address"`
	JWT     string `json:"jwt"`
}

// extractTextFromRawMIME 从 raw MIME 邮件中提取 text/plain 正文（base64 解码后）
// CF Worker 将整封邮件以 raw MIME 格式返回，包含邮件头和 MIME 多部分体
// 直接在整个 raw 字符串上匹配 \b(\d{6})\b 会误匹配到邮件头中的数字（Message-ID、DKIM t= 等）
func extractTextFromRawMIME(raw string) string {
	// 解析 MIME 邮件头找到 boundary
	hp := textproto.NewReader(bufio.NewReader(strings.NewReader(raw)))
	header, err := hp.ReadMIMEHeader()
	if err != nil {
		// 解析失败则回退到整个 raw
		return raw
	}

	contentType := header.Get("Content-Type")
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		// 非 multipart，直接返回 raw
		return raw
	}

	boundary := params["boundary"]
	parts := strings.Split(raw, "--"+boundary)

	var textParts []string
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.TrimSpace(part) == "--" {
			continue
		}

		// 分离 MIME 子头和体
		idx := strings.Index(part, "\r\n\r\n")
		if idx < 0 {
			idx = strings.Index(part, "\n\n")
		}
		if idx < 0 {
			continue
		}

		subHeader := part[:idx]
		body := part[idx:]
		body = strings.TrimPrefix(body, "\r\n\r\n")
		body = strings.TrimPrefix(body, "\n\n")

		subCT := ""
		subCE := ""
		for _, line := range strings.Split(subHeader, "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.HasPrefix(strings.ToLower(line), "content-type:") {
				subCT = strings.TrimSpace(line[len("content-type:"):])
			}
			if strings.HasPrefix(strings.ToLower(line), "content-transfer-encoding:") {
				subCE = strings.TrimSpace(line[len("content-transfer-encoding:"):])
			}
		}

		// 解码 body
		var decoded []byte
		switch strings.ToLower(subCE) {
		case "base64":
			clean := strings.Map(func(r rune) rune {
				if ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') || r == '+' || r == '/' || r == '=' {
					return r
				}
				return -1
			}, body)
			d, err := base64.StdEncoding.DecodeString(clean)
			if err == nil {
				decoded = d
			}
		case "quoted-printable":
			r := quotedprintable.NewReader(strings.NewReader(body))
			d, err := io.ReadAll(r)
			if err == nil {
				decoded = d
			}
		default:
			decoded = []byte(body)
		}

		if strings.Contains(strings.ToLower(subCT), "text/plain") && len(decoded) > 0 {
			textParts = append(textParts, string(decoded))
		}
	}

	if len(textParts) > 0 {
		return strings.Join(textParts, "\n")
	}
	return raw
}

// NewCFTempEmailClient 创建客户端
func NewCFTempEmailClient(config CFTempEmailConfig) *CFTempEmailClient {
	return &CFTempEmailClient{
		config: config,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// doAdminPost 发送带 admin auth 的 POST 请求
func (c *CFTempEmailClient) doAdminPost(path string, payload interface{}) ([]byte, error) {
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(c.config.URL, "/") + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-admin-auth", c.config.AdminAuth)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// 检查容量/配额错误
	if resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 507 {
		return nil, fmt.Errorf("%s 容量/配额不足 (HTTP %d): %s", path, resp.StatusCode, string(respBody))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s HTTP %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// doAuthedGet 发送带 JWT 或 admin auth 的 GET 请求
func (c *CFTempEmailClient) doAuthedGet(path string, jwt string, queryParams map[string]string) ([]byte, error) {
	url := strings.TrimRight(c.config.URL, "/") + path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	} else {
		req.Header.Set("x-admin-auth", c.config.AdminAuth)
	}

	if len(queryParams) > 0 {
		q := req.URL.Query()
		for k, v := range queryParams {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 请求失败: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s HTTP %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// CreateEmail 通过 admin API 创建临时邮箱，返回地址和 JWT
func (c *CFTempEmailClient) CreateEmail(name, domain string) (string, string, error) {
	payload := map[string]interface{}{
		"enablePrefix": false,
		"name":         name,
		"domain":       domain,
	}
	respBody, err := c.doAdminPost("/admin/new_address", payload)
	if err != nil {
		return "", "", fmt.Errorf("创建邮箱失败: %w", err)
	}

	var data cfTempEmailCreateResp
	if err := json.Unmarshal(respBody, &data); err != nil {
		return "", "", fmt.Errorf("解析创建响应失败: %w, body=%s", err, string(respBody))
	}
	if data.Address == "" {
		return "", "", fmt.Errorf("创建邮箱失败: 未返回地址, body=%s", string(respBody))
	}
	return strings.TrimSpace(data.Address), strings.TrimSpace(data.JWT), nil
}

// GetMessagesWithJWT 通过用户 JWT 获取邮件列表
func (c *CFTempEmailClient) GetMessagesWithJWT(jwt string, limit int) ([]cfTempEmailMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	params := map[string]string{
		"limit":  fmt.Sprintf("%d", limit),
		"offset": "0",
	}
	respBody, err := c.doAuthedGet("/api/mails", jwt, params)
	if err != nil {
		return nil, err
	}

	var data cfTempEmailListResp
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("解析邮件列表失败: %w, body=%s", err, string(respBody))
	}
	return data.Results, nil
}

// GetMessagesAdmin 通过 admin auth 获取指定地址的邮件列表
func (c *CFTempEmailClient) GetMessagesAdmin(address string, limit int) ([]cfTempEmailMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	params := map[string]string{
		"limit":   fmt.Sprintf("%d", limit),
		"offset":  "0",
		"address": address,
	}
	respBody, err := c.doAuthedGet("/admin/mails", "", params)
	if err != nil {
		return nil, err
	}

	var data cfTempEmailListResp
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("解析邮件列表失败: %w, body=%s", err, string(respBody))
	}
	return data.Results, nil
}

// TestConnection 测试连接：尝试调用 admin API 探活
func (c *CFTempEmailClient) TestConnection() ([]string, error) {
	// 使用 admin/mails 端点探活（不创建真实邮箱，减少资源浪费）
	// 传一个不可能存在的邮箱地址，期望返回 200 + 空结果
	params := map[string]string{
		"limit":   "1",
		"offset":  "0",
		"address": "connection-test@test.invalid",
	}
	_, err := c.doAuthedGet("/admin/mails", "", params)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %w", err)
	}

	// 返回用户手动填写的域名列表
	return c.config.Domains, nil
}

// CFTempEmailProvider CF 临时邮箱提供商
type CFTempEmailProvider struct {
	client            *CFTempEmailClient
	address           string
	jwt               string
	processedMsgIDs   map[int64]bool
	processedMsgIDsMu sync.Mutex
}

// NewCFTempEmailProvider 创建一个 CF 临时邮箱（调用 Worker admin API）
func NewCFTempEmailProvider(config CFTempEmailConfig, name, domain string) (*CFTempEmailProvider, error) {
	client := NewCFTempEmailClient(config)

	if domain == "" {
		if len(config.Domains) > 0 {
			domain = config.Domains[0]
		} else {
			return nil, fmt.Errorf("CF 临时邮箱配置 %s 未提供域名", config.Name)
		}
	}

	addr, jwt, err := client.CreateEmail(name, domain)
	if err != nil {
		return nil, err
	}

	log.Printf("[CFTempEmail] 邮箱创建完成: %s (JWT: %s...)", addr, jwt[:min(20, len(jwt))])

	return &CFTempEmailProvider{
		client:          client,
		address:         addr,
		jwt:             jwt,
		processedMsgIDs: make(map[int64]bool),
	}, nil
}

// GetAddress 返回邮箱地址
func (p *CFTempEmailProvider) GetAddress() string { return p.address }

// WaitForCode 轮询等待 6 位数字验证码
func (p *CFTempEmailProvider) WaitForCode(timeout, interval int) (string, error) {
	return p.WaitForCodeContext(context.Background(), timeout, interval)
}

// WaitForCodeContext 轮询等待 6 位数字验证码，支持任务取消
func (p *CFTempEmailProvider) WaitForCodeContext(ctx context.Context, timeout, interval int) (string, error) {
	if interval <= 0 {
		interval = 3
	}
	maxRetries := timeout / interval
	if maxRetries < 1 {
		maxRetries = 1
	}
	codeRegex := regexp.MustCompile(`\b(\d{6})\b`)
	log.Printf("[CFTempEmail] 开始等待验证码 %s", p.address)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		// 优先使用 JWT，降级使用 admin auth
		var msgs []cfTempEmailMessage
		var err error
		if p.jwt != "" {
			msgs, err = p.client.GetMessagesWithJWT(p.jwt, 20)
			if err != nil {
				log.Printf("[CFTempEmail] JWT 获取邮件失败: %v，尝试 admin auth", err)
				msgs, err = p.client.GetMessagesAdmin(p.address, 20)
			}
		} else {
			msgs, err = p.client.GetMessagesAdmin(p.address, 20)
		}
		if err != nil {
			if attempt%5 == 0 {
				log.Printf("[CFTempEmail] 获取邮件失败: %v，重试中...", err)
			}
			if err := sleepWithContext(ctx, time.Duration(interval)*time.Second); err != nil {
				return "", err
			}
			continue
		}

		for _, m := range msgs {
			// 跳过已处理的邮件
			p.processedMsgIDsMu.Lock()
			if p.processedMsgIDs[m.ID] {
				p.processedMsgIDsMu.Unlock()
				continue
			}
			p.processedMsgIDs[m.ID] = true
			p.processedMsgIDsMu.Unlock()

			// 从 raw 字段提取 text/plain 正文后匹配验证码
			// 直接搜 raw 会误匹配邮件头中的 6 位数字（DKIM t=、Message-ID 等）
			body := extractTextFromRawMIME(m.Raw)
			if code := extractCodeFromText(body, codeRegex); code != "" {
				log.Printf("[CFTempEmail] 从邮件中获取到验证码: %s", code)
				return code, nil
			}
		}

		if attempt%5 == 0 {
			log.Printf("[CFTempEmail] [%d/%d] 暂无新邮件...", attempt, maxRetries)
		}
		if err := sleepWithContext(ctx, time.Duration(interval)*time.Second); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("等待验证码超时 (%ds)", timeout)
}
