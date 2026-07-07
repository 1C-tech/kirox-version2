package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"reg_go/internal/storage"
)

const (
	graphTokenURL   = "https://login.microsoftonline.com/consumers/oauth2/v2.0/token"
	graphAPIScope   = "https://graph.microsoft.com/.default offline_access"
	imapFallbackScope = "openid offline_access"
	graphBaseURL    = "https://graph.microsoft.com/v1.0/me"

	builtinClientID = "7feada80-d946-4d06-b134-73afa3524fb7"
)

// graphTokenResponse OAuth token 响应
type graphTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// graphMessage Microsoft Graph API 邮件消息
type graphMessage struct {
	ID       string `json:"id"`
	Subject  string `json:"subject"`
	From     struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	ReceivedDateTime string `json:"receivedDateTime"`
	Body             struct {
		Content string `json:"content"`
	} `json:"body"`
}

// graphMessagesResponse Graph API 邮件列表响应
type graphMessagesResponse struct {
	Value []graphMessage `json:"value"`
}

// refreshOutlookTokenWithScope 用指定 scope 刷新 token
func refreshOutlookTokenWithScope(acc OutlookAccount, scope string) (*graphTokenResponse, error) {
	clientID := acc.ClientID
	if clientID == "" {
		clientID = builtinClientID
	}

	form := url.Values{
		"client_id":     {clientID},
		"refresh_token": {acc.RefreshToken},
		"grant_type":    {"refresh_token"},
		"scope":         {scope},
	}

	proxyURL := storage.GetProxy()
	tryPost := func(p string) (*http.Response, error) {
		client := httpClientWithProxy(p, 30*time.Second)
		return client.Post(
			graphTokenURL,
			"application/x-www-form-urlencoded",
			strings.NewReader(form.Encode()),
		)
	}

	resp, err := tryPost(proxyURL)
	if err != nil && proxyURL != "" {
		log.Printf("[Outlook OAuth] 代理请求失败，降级直连：%v", err)
		resp, err = tryPost("")
	}
	if err != nil {
		return nil, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenResp graphTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	if resp.StatusCode != 200 {
		// 特殊处理 Graph scope 被拒绝的情况
		errStr := strings.ToLower(tokenResp.Error + " " + tokenResp.ErrorDesc)
		if strings.Contains(errStr, "aadsts70000") ||
			strings.Contains(errStr, "aadsts90023") ||
			strings.Contains(errStr, "invalid_scope") {
			return &tokenResp, fmt.Errorf("scope_rejected: %s", tokenResp.ErrorDesc)
		}
		return nil, fmt.Errorf("刷新失败 %d: %s", resp.StatusCode, string(body[:min(300, len(body))]))
	}

	return &tokenResp, nil
}

// detectTokenType 探测账号支持的 token 类型，优先 Graph，失败降级 IMAP
// 返回: accessToken, detectedType, error
func detectTokenType(acc *OutlookAccount) (string, TokenType, error) {
	// 如果已经探测过，直接用缓存的类型
	if acc.TokenType != TokenTypeUnknown {
		scope := imapFallbackScope
		if acc.TokenType == TokenTypeGraph {
			scope = graphAPIScope
		}
		tokenResp, err := refreshOutlookTokenWithScope(*acc, scope)
		if err != nil {
			// 缓存失效，重置并重新探测
			acc.TokenType = TokenTypeUnknown
		} else {
			return tokenResp.AccessToken, acc.TokenType, nil
		}
	}

	// 先尝试 Graph scope
	tokenResp, err := refreshOutlookTokenWithScope(*acc, graphAPIScope)
	if err == nil {
		acc.TokenType = TokenTypeGraph
		return tokenResp.AccessToken, TokenTypeGraph, nil
	}

	// Graph 被拒绝，降级到 IMAP 兼容 scope
	log.Printf("[Outlook OAuth] Graph 未授权 (%v)，降级到 IMAP 兼容模式", err)
	tokenResp, err = refreshOutlookTokenWithScope(*acc, imapFallbackScope)
	if err != nil {
		return "", TokenTypeUnknown, fmt.Errorf("双 scope 均失败: %v", err)
	}

	acc.TokenType = TokenTypeIMAP
	return tokenResp.AccessToken, TokenTypeIMAP, nil
}

// fetchGraphMessages 通过 Graph API 获取最近邮件
func fetchGraphMessages(accessToken string, proxyURL string) ([]graphMessage, error) {
	apiURL := graphBaseURL + "/messages?" + url.Values{
		"$select":  {"id,subject,from,receivedDateTime,body"},
		"$orderby": {"receivedDateTime desc"},
		"$top":     {"50"},
	}.Encode()

	client := httpClientWithProxy(proxyURL, 15*time.Second)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Graph 请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Graph 返回 %d: %s", resp.StatusCode, string(body[:min(300, len(body))]))
	}

	var msgs graphMessagesResponse
	if err := json.Unmarshal(body, &msgs); err != nil {
		return nil, fmt.Errorf("解析 Graph 响应失败: %v", err)
	}

	return msgs.Value, nil
}

// waitForOTPGraph 通过 Graph API 轮询等待验证码
func waitForOTPGraph(ctx context.Context, acc OutlookAccount, beforeCount, timeout, interval int) (string, error) {
	log.Printf("[Outlook Graph] 等待验证码, 邮箱=%s", acc.Email)

	codeRegex := regexp.MustCompile(`\b(\d{6})\b`)
	maxRetries := timeout / interval
	proxyURL := storage.GetProxy()

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}

		accessToken, _, err := detectTokenType(&acc)
		if err != nil {
			log.Printf("[Outlook Graph] 获取 Token 失败: %v", err)
			if err := sleepWithContext(ctx, time.Duration(interval)*time.Second); err != nil {
				return "", err
			}
			continue
		}

		msgs, err := fetchGraphMessages(accessToken, proxyURL)
		if err != nil {
			log.Printf("[Outlook Graph] 获取邮件失败: %v", err)
			if err := sleepWithContext(ctx, time.Duration(interval)*time.Second); err != nil {
				return "", err
			}
			continue
		}

		if len(msgs) <= beforeCount {
			if attempt%5 == 0 {
				log.Printf("[Outlook Graph] [%d/%d] 暂无新邮件 (当前%d封)...", attempt, maxRetries, len(msgs))
			}
			if err := sleepWithContext(ctx, time.Duration(interval)*time.Second); err != nil {
				return "", err
			}
			continue
		}

		// 只检查比 beforeCount 新的邮件
		for i := 0; i < len(msgs) && i < len(msgs)-beforeCount; i++ {
			msg := msgs[i]
			// 只关注 AWS 相关邮件
			sender := strings.ToLower(msg.From.EmailAddress.Address)
			subject := strings.ToLower(msg.Subject)
			if !strings.Contains(sender, "aws") && !strings.Contains(sender, "amazon") &&
				!strings.Contains(subject, "aws") && !strings.Contains(subject, "amazon") &&
				!strings.Contains(subject, "验证") && !strings.Contains(subject, "verif") &&
				!strings.Contains(subject, "code") {
				continue
			}

			// 解码 body
			bodyContent := msg.Body.Content
			decoded := decodeGraphBody(bodyContent)

			code := extractCodeFromText(decoded, codeRegex)
			if code != "" {
				log.Printf("[Outlook Graph] 获取到验证码: %s", code)
				return code, nil
			}
		}

		if attempt%5 == 0 {
			log.Printf("[Outlook Graph] [%d/%d] 新邮件中未找到验证码...", attempt, maxRetries)
		}
		if err := sleepWithContext(ctx, time.Duration(interval)*time.Second); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("等待验证码超时 (%ds)", timeout)
}

// decodeGraphBody 解码 Graph API 返回的邮件正文（可能含 base64）
func decodeGraphBody(body string) string {
	// Graph 可能返回纯文本或 HTML
	if strings.Contains(body, "base64") {
		parts := strings.Split(body, "------=_Part_")
		var decoded string
		for _, part := range parts {
			if strings.Contains(part, "base64") {
				idx := strings.Index(part, "base64")
				content := part[idx+6:]
				b64 := strings.Map(func(r rune) rune {
					if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
						return -1
					}
					return r
				}, content)
				if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
					decoded += string(data) + " "
				}
			}
		}
		if decoded != "" {
			return decoded
		}
	}

	// 整体 base64 解码
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, body)
	if data, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return string(data)
	}

	return body
}

// getInboxCountGraph 通过 Graph API 获取收件箱邮件数量
func getInboxCountGraph(acc OutlookAccount) (int, error) {
	accessToken, _, err := detectTokenType(&acc)
	if err != nil {
		return 0, fmt.Errorf("获取 Graph Token 失败: %v", err)
	}

	proxyURL := storage.GetProxy()
	msgs, err := fetchGraphMessages(accessToken, proxyURL)
	if err != nil {
		return 0, err
	}

	return len(msgs), nil
}
