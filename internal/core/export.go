package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	httputil "reg_go/internal/http"
)

const exportAppVersion = "0.12.155"

// ExportedAccount 导出用的完整账号结构
type ExportedAccount struct {
	ID              string                 `json:"id"`
	Email           string                 `json:"email"`
	UserID          string                 `json:"user_id"`
	LoginProvider   string                 `json:"login_provider"`
	AccessToken     string                 `json:"access_token"`
	RefreshToken    string                 `json:"refresh_token"`
	TokenType       string                 `json:"token_type"`
	ExpiresAt       int64                  `json:"expires_at"`
	LoginHint       string                 `json:"login_hint"`
	PlanName        string                 `json:"plan_name"`
	PlanTier        string                 `json:"plan_tier"`
	CreditsTotal    float64                `json:"credits_total"`
	CreditsUsed     float64                `json:"credits_used"`
	UsageResetAt    int64                  `json:"usage_reset_at"`
	KiroAuthToken   map[string]interface{} `json:"kiro_auth_token_raw"`
	KiroProfile     map[string]interface{} `json:"kiro_profile_raw"`
	KiroUsageRaw    map[string]interface{} `json:"kiro_usage_raw"`
	Status          string                 `json:"status"`
	UsageUpdatedAt  int64                  `json:"usage_updated_at"`
	CreatedAt       int64                  `json:"created_at"`
	LastUsed        int64                  `json:"last_used"`
}

type ExportAccountInput struct {
	Email        string `json:"email"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region"`
	Provider     string `json:"provider"`
}

// stableMachineIDExport 与 subscription 包保持一致
func stableMachineIDExport(email string) string {
	h := sha256.Sum256([]byte("kiro-device-" + email))
	return hex.EncodeToString(h[:])
}

func exportUA(machineID string) string {
	return fmt.Sprintf("aws-sdk-js/1.0.0 ua/2.1 os/win32#10.0.19043 lang/js md/nodejs#22.22.0 api/codewhispererruntime#1.0.0 m/N,E KiroIDE-%s-%s", exportAppVersion, machineID)
}

func exportAmzUA(machineID string) string {
	return fmt.Sprintf("aws-sdk-js/1.0.0 KiroIDE-%s-%s", exportAppVersion, machineID)
}

// oidcRefreshResult OIDC token 刷新结果
type oidcRefreshResult struct {
	tok         map[string]interface{}
	accessToken string
	expiresIn   float64
	err         error
}

// kiroRefreshResult Kiro refreshToken 结果
type kiroRefreshResult struct {
	authToken map[string]interface{}
	err       error
}

// ExportAccount 对单个账号拉取完整 Kiro 数据，返回导出格式的 JSON。
// 优化：步骤1（OIDC刷新）和步骤2（Kiro refreshToken）并行执行，
// 两者互不依赖，并行后可节省约 1/3 的网络等待时间。
func ExportAccount(acc ExportAccountInput) (*ExportedAccount, error) {
	machineID := stableMachineIDExport(acc.Email)

	oidcURL := "https://oidc.us-east-1.amazonaws.com/token"
	kiroRegion := "us-east-1"
	if acc.Region != "" && strings.HasPrefix(acc.Region, "eu-") {
		oidcURL = "https://oidc.eu-central-1.amazonaws.com/token"
		kiroRegion = "eu-central-1"
	}

	// ── 并行步骤 1+2：OIDC 刷新 和 Kiro refreshToken 同时发起 ──
	oidcCh := make(chan oidcRefreshResult, 1)
	kiroCh := make(chan kiroRefreshResult, 1)

	// 协程 A: OIDC token 刷新
	go func() {
		client := httputil.NewTLSClient("", true)
		tokenBody, _ := json.Marshal(map[string]string{
			"clientId":     acc.ClientID,
			"clientSecret": acc.ClientSecret,
			"refreshToken": acc.RefreshToken,
			"grantType":    "refresh_token",
		})
		req, _ := fhttp.NewRequest("POST", oidcURL, bytes.NewReader(tokenBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			oidcCh <- oidcRefreshResult{err: fmt.Errorf("刷新 token 失败: %w", err)}
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			oidcCh <- oidcRefreshResult{err: fmt.Errorf("刷新 token 失败 HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 500))}
			return
		}
		var tok map[string]interface{}
		json.Unmarshal(body, &tok)
		at, _ := tok["accessToken"].(string)
		if at == "" {
			oidcCh <- oidcRefreshResult{err: fmt.Errorf("accessToken 为空")}
			return
		}
		ei, _ := tok["expiresIn"].(float64)
		oidcCh <- oidcRefreshResult{tok: tok, accessToken: at, expiresIn: ei}
	}()

	// 协程 B: Kiro refreshToken（与 OIDC 无依赖，可并行）
	go func() {
		client := httputil.NewTLSClient("", true)
		idcR := idcRegion(acc.Region)
		kiroRefreshBody, _ := json.Marshal(map[string]string{
			"clientId":     acc.ClientID,
			"clientSecret": acc.ClientSecret,
			"refreshToken": acc.RefreshToken,
			"grantType":    "refresh_token",
			"idc_region":   idcR,
		})
		req, _ := fhttp.NewRequest("POST", "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken", bytes.NewReader(kiroRefreshBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			kiroCh <- kiroRefreshResult{err: fmt.Errorf("Kiro refreshToken 失败: %w", err)}
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("[Export] Kiro refreshToken HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 500))
			kiroCh <- kiroRefreshResult{} // 非致命错误，继续
			return
		}
		var authToken map[string]interface{}
		json.Unmarshal(body, &authToken)
		kiroCh <- kiroRefreshResult{authToken: authToken}
	}()

	// 等待两个协程完成
	oidcRes := <-oidcCh
	kiroRes := <-kiroCh

	// OIDC 刷新是必需的（accessToken 用于后续查询）
	if oidcRes.err != nil {
		return nil, oidcRes.err
	}
	accessToken := oidcRes.accessToken
	expiresIn := oidcRes.expiresIn
	kiroAuthToken := kiroRes.authToken

	// 从 Kiro refreshToken 响应中提取 profileArn
	profileARN := extractProfileARN(kiroAuthToken, acc.Provider)

	// ── 步骤 3: 查询 usage limits ──
	client := httputil.NewTLSClient("", true)
	qURL := fmt.Sprintf("https://q.%s.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true&profileArn=%s",
		kiroRegion, url.QueryEscape(profileARN))
	req3, _ := fhttp.NewRequest("GET", qURL, nil)
	req3.Header.Set("Accept", "application/json")
	req3.Header.Set("Authorization", "Bearer "+accessToken)
	req3.Header.Set("User-Agent", exportUA(machineID))
	req3.Header.Set("x-amz-user-agent", exportAmzUA(machineID))
	resp3, err := client.Do(req3)
	if err != nil {
		return nil, fmt.Errorf("查询 usage 失败: %w", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()

	var usageRaw map[string]interface{}
	if resp3.StatusCode == 200 {
		json.Unmarshal(body3, &usageRaw)
	} else {
		return nil, fmt.Errorf("usage API HTTP %d: %s", resp3.StatusCode, truncateStr(string(body3), 500))
	}

	// 4. 解析 usage 数据
	userInfo, _ := usageRaw["userInfo"].(map[string]interface{})
	subInfo, _ := usageRaw["subscriptionInfo"].(map[string]interface{})
	planName, _ := subInfo["subscriptionTitle"].(string)
	planTier, _ := subInfo["type"].(string)
	userID, _ := userInfo["userId"].(string)
	email, _ := userInfo["email"].(string)

	var totalLimit, totalUsed float64
	var usageResetAt float64
	if breakdown, ok := usageRaw["usageBreakdownList"].([]interface{}); ok {
		for _, item := range breakdown {
			b, _ := item.(map[string]interface{})
			dn, _ := b["displayName"].(string)
			if b["resourceType"] == "CREDIT" || dn == "Credits" {
				totalLimit, _ = b["usageLimitWithPrecision"].(float64)
				totalUsed, _ = b["currentUsageWithPrecision"].(float64)
				if totalLimit == 0 {
					totalLimit, _ = b["usageLimit"].(float64)
				}
				if totalUsed == 0 {
					totalUsed, _ = b["currentUsage"].(float64)
				}
				usageResetAt, _ = b["nextDateReset"].(float64)
				break
			}
		}
	}

	// 5. Kiro profile
	loginProvider := "BuilderId"
	if kiroAuthToken != nil {
		if lp, ok := kiroAuthToken["loginProvider"].(string); ok {
			loginProvider = lp
		}
		if lp, ok := kiroAuthToken["provider"].(string); ok {
			loginProvider = lp
		}
	}
	if acc.Provider != "" {
		loginProvider = acc.Provider
	}

	kiroProfile := map[string]interface{}{
		"name": loginProvider,
	}
	if kiroAuthToken != nil {
		if arn, ok := kiroAuthToken["profileArn"].(string); ok {
			kiroProfile["arn"] = arn
		}
	}

	// 生成稳定的 ID
	id := fmt.Sprintf("kiro_%s", sha256Hex(acc.Email)[:32])

	now := time.Now().Unix()

	eo := &ExportedAccount{
		ID:            id,
		Email:         email,
		UserID:        userID,
		LoginProvider: loginProvider,
		AccessToken:   accessToken,
		RefreshToken:  acc.RefreshToken,
		TokenType:     "Bearer",
		ExpiresAt:     now + int64(expiresIn),
		LoginHint:     email,
		PlanName:      planName,
		PlanTier:      planTier,
		CreditsTotal:  totalLimit,
		CreditsUsed:   totalUsed,
		UsageResetAt:  int64(usageResetAt),
		KiroAuthToken: kiroAuthToken,
		KiroProfile:   kiroProfile,
		KiroUsageRaw:  usageRaw,
		Status:        "normal",
		UsageUpdatedAt: now,
		CreatedAt:     now,
		LastUsed:      now,
	}

	return eo, nil
}

// ExportAccounts 并发批量导出多个账号。
// concurrency 控制并发数，≤0 或 >len(inputs) 时自动设为 len(inputs)。
func ExportAccounts(inputs []ExportAccountInput, concurrency int) ([]*ExportedAccount, []map[string]interface{}) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if concurrency <= 0 || concurrency > len(inputs) {
		concurrency = len(inputs)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []*ExportedAccount
		errors  []map[string]interface{}
	)

	if concurrency == 1 {
		// 单并发退化为串行（无 goroutine 开销）
		for _, acc := range inputs {
			eo, err := ExportAccount(acc)
			if err != nil {
				log.Printf("[Export] %s 导出失败: %v", acc.Email, err)
				errors = append(errors, map[string]interface{}{
					"email": acc.Email,
					"error": err.Error(),
				})
				continue
			}
			results = append(results, eo)
		}
		return results, errors
	}

	sem := make(chan struct{}, concurrency)
	log.Printf("[Export] 并发导出 %d 个账号，并发数 %d", len(inputs), concurrency)

	for i := range inputs {
		wg.Add(1)
		go func(acc ExportAccountInput) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			eo, err := ExportAccount(acc)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Printf("[Export] %s 导出失败: %v", acc.Email, err)
				errors = append(errors, map[string]interface{}{
					"email": acc.Email,
					"error": err.Error(),
				})
				return
			}
			results = append(results, eo)
		}(inputs[i])
	}
	wg.Wait()

	return results, errors
}

// QuickExportAccount 离线快速导出：不调用任何 API，仅使用本地存储的账号数据构建导出 JSON。
// 适用场景：大批量导出只需 refreshToken 等基础字段，无需实时用量数据。
func QuickExportAccount(acc ExportAccountInput) *ExportedAccount {
	id := fmt.Sprintf("kiro_%s", sha256Hex(acc.Email)[:32])
	now := time.Now().Unix()

	return &ExportedAccount{
		ID:            id,
		Email:         acc.Email,
		UserID:        "",
		LoginProvider: acc.Provider,
		AccessToken:   "",
		RefreshToken:  acc.RefreshToken,
		TokenType:     "Bearer",
		ExpiresAt:     0,
		LoginHint:     acc.Email,
		PlanName:      "",
		PlanTier:      "",
		CreditsTotal:  0,
		CreditsUsed:   0,
		UsageResetAt:  0,
		KiroAuthToken: nil,
		KiroProfile: map[string]interface{}{
			"name": acc.Provider,
		},
		KiroUsageRaw:   nil,
		Status:         "offline",
		UsageUpdatedAt: 0,
		CreatedAt:      now,
		LastUsed:       0,
	}
}

// QuickExportAccounts 离线批量导出：零 API 调用，即时完成。
func QuickExportAccounts(inputs []ExportAccountInput) []*ExportedAccount {
	results := make([]*ExportedAccount, 0, len(inputs))
	for _, acc := range inputs {
		results = append(results, QuickExportAccount(acc))
	}
	return results
}

// ChangeSubscription 对单个账号修改订阅类型
func ChangeSubscription(acc ExportAccountInput, subscriptionType string) (map[string]interface{}, error) {
	oidcURL := "https://oidc.us-east-1.amazonaws.com/token"
	if acc.Region != "" && strings.HasPrefix(acc.Region, "eu-") {
		oidcURL = "https://oidc.eu-central-1.amazonaws.com/token"
	}

	client := httputil.NewTLSClient("", true)

	// 刷新 token
	tokenBody, _ := json.Marshal(map[string]string{
		"clientId":     acc.ClientID,
		"clientSecret": acc.ClientSecret,
		"refreshToken": acc.RefreshToken,
		"grantType":    "refresh_token",
	})
	req, _ := fhttp.NewRequest("POST", oidcURL, bytes.NewReader(tokenBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("刷新 token 失败: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("刷新 token 失败 HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 500))
	}

	var tok map[string]interface{}
	json.Unmarshal(body, &tok)
	accessToken, _ := tok["accessToken"].(string)
	if accessToken == "" {
		return nil, fmt.Errorf("accessToken 为空")
	}

	// 调用 Kiro refreshToken 获取 profileArn
	idcR := idcRegion(acc.Region)
	kiroRefreshBody, _ := json.Marshal(map[string]string{
		"clientId":     acc.ClientID,
		"clientSecret": acc.ClientSecret,
		"refreshToken": acc.RefreshToken,
		"grantType":    "refresh_token",
		"idc_region":   idcR,
	})
	reqKiro, _ := fhttp.NewRequest("POST", "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken", bytes.NewReader(kiroRefreshBody))
	reqKiro.Header.Set("Content-Type", "application/json")
	respKiro, err := client.Do(reqKiro)
	var kiroAuth map[string]interface{}
	if err == nil {
		bodyKiro, _ := io.ReadAll(respKiro.Body)
		respKiro.Body.Close()
		if respKiro.StatusCode == 200 {
			json.Unmarshal(bodyKiro, &kiroAuth)
		}
	}

	// 调用 CreateSubscriptionToken
	profileARN := extractProfileARN(kiroAuth, acc.Provider)
	subType := subscriptionType
	if subType == "" {
		subType = "Q_DEVELOPER_STANDALONE_FREE"
	}

	machineID := stableMachineIDExport(acc.Email)
	qURL := "https://q.us-east-1.amazonaws.com/CreateSubscriptionToken"
	payload, _ := json.Marshal(map[string]interface{}{
		"clientToken":      fmt.Sprintf("kirox-%d", time.Now().UnixNano()),
		"profileArn":       profileARN,
		"provider":         "STRIPE",
		"subscriptionType": subType,
	})
	req2, _ := fhttp.NewRequest("POST", qURL, bytes.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+accessToken)
	req2.Header.Set("User-Agent", exportUA(machineID))
	req2.Header.Set("x-amz-user-agent", exportAmzUA(machineID))
	resp2, err := client.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("CreateSubscriptionToken 失败: %w", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	var result map[string]interface{}
	json.Unmarshal(body2, &result)

	if resp2.StatusCode != 200 {
		msg, _ := result["message"].(string)
		if msg == "" {
			msg = truncateStr(string(body2), 500)
		}
		return map[string]interface{}{
			"success": false,
			"status":  resp2.StatusCode,
			"error":   msg,
		}, nil
	}

	return map[string]interface{}{
		"success": true,
		"data":    result,
		"email":   acc.Email,
	}, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// idcRegion 根据 OIDC region 推导 Identity Center 区域代码。
// 所有 BuilderId 账号使用 us-east-1；eu-* 账号使用 eu-central-1。
func idcRegion(region string) string {
	if region != "" && strings.HasPrefix(region, "eu-") {
		return "eu-central-1"
	}
	return "us-east-1"
}

// extractProfileARN 从 Kiro refreshToken 响应中提取 profileArn；
// 若不存在则根据 provider 推导 fallback。
func extractProfileARN(kiroAuthToken map[string]interface{}, provider string) string {
	if kiroAuthToken != nil {
		if arn, ok := kiroAuthToken["profileArn"].(string); ok && arn != "" {
			return arn
		}
	}

	// fallback：已知的默认 profile ARN
	const defaultProfileARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	const socialProfileARN = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"

	if provider == "Github" || provider == "Google" {
		return socialProfileARN
	}
	return defaultProfileARN
}