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
	ID             string                 `json:"id"`
	Email          string                 `json:"email"`
	UserID         string                 `json:"user_id"`
	LoginProvider  string                 `json:"login_provider"`
	AccessToken    string                 `json:"access_token"`
	RefreshToken   string                 `json:"refresh_token"`
	TokenType      string                 `json:"token_type"`
	ExpiresAt      int64                  `json:"expires_at"`
	LoginHint      string                 `json:"login_hint"`
	PlanName       string                 `json:"plan_name"`
	PlanTier       string                 `json:"plan_tier"`
	CreditsTotal   float64                `json:"credits_total"`
	CreditsUsed    float64                `json:"credits_used"`
	UsageResetAt   int64                  `json:"usage_reset_at"`
	ClientID       string                 `json:"client_id,omitempty"`
	ClientSecret   string                 `json:"client_secret,omitempty"`
	Region         string                 `json:"region,omitempty"`
	IdcRegion      string                 `json:"idc_region,omitempty"`
	KiroAuthToken  map[string]interface{} `json:"kiro_auth_token_raw"`
	KiroProfile    map[string]interface{} `json:"kiro_profile_raw"`
	KiroUsageRaw   map[string]interface{} `json:"kiro_usage_raw"`
	Status         string                 `json:"status"`
	UsageUpdatedAt int64                  `json:"usage_updated_at"`
	CreatedAt      int64                  `json:"created_at"`
	LastUsed       int64                  `json:"last_used"`
}

type ExportAccountInput struct {
	Email        string `json:"email"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region"`
	Provider     string `json:"provider"`

	AWSRefreshToken string `json:"awsRefreshToken"`
	AWSClientID     string `json:"awsClientId"`
	AWSClientSecret string `json:"awsClientSecret"`

	KiroAccessToken  string `json:"kiroAccessToken"`
	KiroRefreshToken string `json:"kiroRefreshToken"`
	KiroClientID     string `json:"kiroClientId"`
	KiroClientSecret string `json:"kiroClientSecret"`
	KiroTokenType    string `json:"kiroTokenType"`
	KiroExpiresAt    int64  `json:"kiroExpiresAt"`

	UserID        string                 `json:"userId"`
	LoginProvider string                 `json:"loginProvider"`
	ProfileARN    string                 `json:"profileArn"`
	PlanName      string                 `json:"planName"`
	PlanTier      string                 `json:"planTier"`
	CreditsTotal  float64                `json:"creditsTotal"`
	CreditsUsed   float64                `json:"creditsUsed"`
	UsageResetAt  int64                  `json:"usageResetAt"`
	CreatedAt     int64                  `json:"createdAt"`
	LastUsed      int64                  `json:"lastUsed"`
	KiroAuthToken map[string]interface{} `json:"kiroAuthTokenRaw"`
	KiroProfile   map[string]interface{} `json:"kiroProfileRaw"`
	KiroUsageRaw  map[string]interface{} `json:"kiroUsageRaw"`
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
// 优先使用 Step15 获得的 Kiro 登录态；AWS/OIDC refresh token 只作为旧数据兼容路径。
func ExportAccount(acc ExportAccountInput) (*ExportedAccount, error) {
	machineID := stableMachineIDExport(acc.Email)
	now := time.Now().Unix()
	region := firstNonEmpty(acc.Region, "us-east-1")
	kiroRegion := kiroAPIRegion(region)

	awsRefreshToken := firstNonEmpty(acc.AWSRefreshToken, acc.RefreshToken)
	awsClientID := firstNonEmpty(acc.AWSClientID, acc.ClientID)
	awsClientSecret := firstNonEmpty(acc.AWSClientSecret, acc.ClientSecret)

	kiroAuthToken := cloneMap(acc.KiroAuthToken)
	accessToken := firstNonEmpty(acc.KiroAccessToken, stringField(kiroAuthToken, "accessToken"))
	exportRefreshToken := firstNonEmpty(acc.KiroRefreshToken, stringField(kiroAuthToken, "refreshToken"))
	tokenType := firstNonEmpty(acc.KiroTokenType, stringField(kiroAuthToken, "tokenType"), "Bearer")
	expiresIn := numberField(kiroAuthToken, "expiresIn")
	expiresAt := firstNonZero(acc.KiroExpiresAt, expiresAtFromAuth(kiroAuthToken, now, expiresIn))

	var kiroRefreshErr error
	if acc.KiroRefreshToken != "" && acc.KiroClientID != "" {
		refreshed, err := refreshKiroAuthToken(acc, idcRegion(region))
		if err != nil {
			kiroRefreshErr = err
			log.Printf("[Export] %s Kiro refreshToken 刷新失败，尝试复用本地 Kiro 登录态: %v", acc.Email, err)
		} else {
			kiroAuthToken = refreshed
			accessToken = firstNonEmpty(stringField(refreshed, "accessToken"), accessToken)
			exportRefreshToken = firstNonEmpty(stringField(refreshed, "refreshToken"), exportRefreshToken)
			tokenType = firstNonEmpty(stringField(refreshed, "tokenType"), tokenType, "Bearer")
			expiresIn = numberField(refreshed, "expiresIn")
			expiresAt = expiresAtFromAuth(refreshed, now, expiresIn)
		}
	}

	usedAWSFallback := false
	if accessToken == "" && awsRefreshToken != "" && awsClientID != "" && awsClientSecret != "" {
		tok, at, ei, err := refreshAWSAccessToken(awsClientID, awsClientSecret, awsRefreshToken, region)
		if err != nil {
			if kiroRefreshErr != nil {
				return nil, fmt.Errorf("刷新Kiro登录失败: %v; AWS IAM Identity Center OIDC刷新失败: %w", kiroRefreshErr, err)
			}
			return nil, fmt.Errorf("AWS IAM Identity Center OIDC刷新失败: %w", err)
		}
		usedAWSFallback = true
		accessToken = at
		expiresIn = ei
		expiresAt = now + int64(ei)
		if exportRefreshToken == "" {
			exportRefreshToken = awsRefreshToken
		}
		if kiroAuthToken == nil {
			kiroAuthToken = tok
		}
	}

	if accessToken == "" {
		if kiroRefreshErr != nil {
			return nil, fmt.Errorf("刷新Kiro登录失败: %v", kiroRefreshErr)
		}
		return nil, fmt.Errorf("缺少 Kiro accessToken/refreshToken，无法导出可登录的 Kiro JSON")
	}
	if exportRefreshToken == "" {
		exportRefreshToken = awsRefreshToken
	}
	if expiresAt == 0 && expiresIn > 0 {
		expiresAt = now + int64(expiresIn)
	}

	loginProvider := deriveLoginProvider(acc, kiroAuthToken)
	profileARN := firstNonEmpty(acc.ProfileARN, stringField(kiroAuthToken, "profileArn"), stringField(acc.KiroProfile, "arn"))
	if profileARN == "" {
		profileARN = extractProfileARN(kiroAuthToken, firstNonEmpty(loginProvider, acc.Provider))
		if profileARN != "" {
			log.Printf("[Export] %s 缺少 profileArn，按 provider=%s 使用兜底 profileArn 查询 usage", acc.Email, firstNonEmpty(loginProvider, acc.Provider))
		}
	}

	usageRaw := cloneMap(acc.KiroUsageRaw)
	if profileARN != "" {
		fetchedUsage, err := fetchUsageLimits(accessToken, profileARN, kiroRegion, machineID)
		if err != nil {
			if usageRaw == nil {
				return nil, err
			}
			log.Printf("[Export] %s usage 实时查询失败，复用本地 usage: %v", acc.Email, err)
		} else {
			usageRaw = fetchedUsage
		}
	} else if usageRaw == nil {
		return nil, fmt.Errorf("缺少 profileArn，无法查询 usage；请用包含 kiro_auth_token_raw.profileArn 的账号源重新导出")
	}

	planName, planTier, userID, email, totalLimit, totalUsed, usageResetAt := parseUsageSummary(usageRaw)
	email = firstNonEmpty(email, stringField(kiroAuthToken, "email"), acc.Email)
	userID = firstNonEmpty(userID, stringField(kiroAuthToken, "userId"), stringField(kiroAuthToken, "user_id"), acc.UserID)
	planName = firstNonEmpty(planName, acc.PlanName)
	planTier = firstNonEmpty(planTier, acc.PlanTier)
	totalLimit = firstNonZeroFloat(totalLimit, acc.CreditsTotal)
	totalUsed = firstNonZeroFloat(totalUsed, acc.CreditsUsed)
	usageResetAt = firstNonZeroInt(usageResetAt, acc.UsageResetAt)

	kiroAuthToken = ensureKiroAuthToken(kiroAuthToken, email, userID, loginProvider, accessToken, exportRefreshToken, tokenType, expiresAt, expiresIn, profileARN)
	kiroProfile := cloneMap(acc.KiroProfile)
	if kiroProfile == nil {
		kiroProfile = map[string]interface{}{}
	}
	if profileARN != "" {
		kiroProfile["arn"] = profileARN
	}
	if _, ok := kiroProfile["name"]; !ok {
		kiroProfile["name"] = loginProvider
	}

	id := fmt.Sprintf("kiro_%s", sha256Hex(email)[:32])
	createdAt := firstNonZero(acc.CreatedAt, now)
	lastUsed := firstNonZero(acc.LastUsed, now)

	eo := &ExportedAccount{
		ID:             id,
		Email:          email,
		UserID:         userID,
		LoginProvider:  loginProvider,
		AccessToken:    accessToken,
		RefreshToken:   exportRefreshToken,
		TokenType:      tokenType,
		ExpiresAt:      expiresAt,
		LoginHint:      email,
		PlanName:       planName,
		PlanTier:       planTier,
		CreditsTotal:   totalLimit,
		CreditsUsed:    totalUsed,
		UsageResetAt:   usageResetAt,
		KiroAuthToken:  kiroAuthToken,
		KiroProfile:    kiroProfile,
		KiroUsageRaw:   usageRaw,
		Status:         "normal",
		UsageUpdatedAt: now,
		CreatedAt:      createdAt,
		LastUsed:       lastUsed,
	}
	if usedAWSFallback {
		eo.ClientID = awsClientID
		eo.ClientSecret = awsClientSecret
		eo.Region = region
		eo.IdcRegion = idcRegion(region)
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
		"idc_region":   oidcRegionFromFullURL(oidcURL),
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func firstNonZeroInt(values ...int64) int64 {
	return firstNonZero(values...)
}

func firstNonZeroFloat(values ...float64) float64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func stringField(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func numberField(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		n, _ := v.Float64()
		return n
	default:
		return 0
	}
}

func int64Field(m map[string]interface{}, key string) int64 {
	return int64(numberField(m, key))
}

func expiresAtFromAuth(auth map[string]interface{}, now int64, expiresIn float64) int64 {
	if auth != nil {
		if n := int64Field(auth, "expires_at"); n != 0 {
			return n
		}
		if s := stringField(auth, "expiresAt"); s != "" {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.Unix()
			}
		}
		if n := numberField(auth, "expiresIn"); n != 0 {
			return now + int64(n)
		}
	}
	if expiresIn != 0 {
		return now + int64(expiresIn)
	}
	return 0
}

func kiroAPIRegion(region string) string {
	if region != "" && strings.HasPrefix(region, "eu-") {
		return "eu-central-1"
	}
	return "us-east-1"
}

func refreshAWSAccessToken(clientID, clientSecret, refreshToken, region string) (map[string]interface{}, string, float64, error) {
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return nil, "", 0, fmt.Errorf("缺少 clientId/clientSecret/refreshToken")
	}
	oidcURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", kiroAPIRegion(region))
	client := httputil.NewTLSClient("", true)
	tokenBody, _ := json.Marshal(map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
		"idc_region":   oidcRegionFromFullURL(oidcURL),
	})
	req, _ := fhttp.NewRequest("POST", oidcURL, bytes.NewReader(tokenBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("刷新 token 失败: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", 0, fmt.Errorf("刷新 token 失败 HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 500))
	}
	var tok map[string]interface{}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, "", 0, fmt.Errorf("解析 token 响应失败: %w", err)
	}
	at, _ := tok["accessToken"].(string)
	if at == "" {
		return nil, "", 0, fmt.Errorf("accessToken 为空")
	}
	return tok, at, numberField(tok, "expiresIn"), nil
}

func refreshKiroAuthToken(acc ExportAccountInput, region string) (map[string]interface{}, error) {
	if acc.KiroClientID == "" || acc.KiroRefreshToken == "" {
		return nil, fmt.Errorf("缺少 Kiro clientId/refreshToken")
	}
	client := httputil.NewTLSClient("", true)
	kiroRefreshBody, _ := json.Marshal(map[string]string{
		"clientId":     acc.KiroClientID,
		"clientSecret": acc.KiroClientSecret,
		"refreshToken": acc.KiroRefreshToken,
		"grantType":    "refresh_token",
		"idc_region":   region,
	})
	req, _ := fhttp.NewRequest("POST", "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken", bytes.NewReader(kiroRefreshBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Kiro refreshToken接口失败: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Kiro refreshToken接口返回异常: status=%d %s, body_len=%d", resp.StatusCode, resp.Status, len(body))
	}
	var authToken map[string]interface{}
	if err := json.Unmarshal(body, &authToken); err != nil {
		return nil, fmt.Errorf("解析 Kiro refreshToken 响应失败: %w", err)
	}
	if stringField(authToken, "accessToken") == "" {
		return nil, fmt.Errorf("Kiro refreshToken 响应缺少 accessToken")
	}
	return authToken, nil
}

func fetchUsageLimits(accessToken, profileARN, region, machineID string) (map[string]interface{}, error) {
	client := httputil.NewTLSClient("", true)
	qURL := fmt.Sprintf("https://q.%s.amazonaws.com/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&isEmailRequired=true&profileArn=%s",
		region, url.QueryEscape(profileARN))
	req, _ := fhttp.NewRequest("GET", qURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", exportUA(machineID))
	req.Header.Set("x-amz-user-agent", exportAmzUA(machineID))
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询 usage 失败: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("usage API HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 500))
	}
	var usageRaw map[string]interface{}
	if err := json.Unmarshal(body, &usageRaw); err != nil {
		return nil, fmt.Errorf("解析 usage 响应失败: %w", err)
	}
	return usageRaw, nil
}

func parseUsageSummary(usageRaw map[string]interface{}) (planName, planTier, userID, email string, totalLimit, totalUsed float64, usageResetAt int64) {
	if usageRaw == nil {
		return
	}
	userInfo, _ := usageRaw["userInfo"].(map[string]interface{})
	subInfo, _ := usageRaw["subscriptionInfo"].(map[string]interface{})
	planName, _ = subInfo["subscriptionTitle"].(string)
	planTier, _ = subInfo["type"].(string)
	userID, _ = userInfo["userId"].(string)
	email, _ = userInfo["email"].(string)

	if breakdown, ok := usageRaw["usageBreakdownList"].([]interface{}); ok {
		for _, item := range breakdown {
			b, _ := item.(map[string]interface{})
			dn, _ := b["displayName"].(string)
			if b["resourceType"] == "CREDIT" || dn == "Credits" || dn == "Credit" {
				totalLimit = firstNonZeroFloat(numberField(b, "usageLimitWithPrecision"), numberField(b, "usageLimit"))
				totalUsed = firstNonZeroFloat(numberField(b, "currentUsageWithPrecision"), numberField(b, "currentUsage"))
				usageResetAt = int64(numberField(b, "nextDateReset"))
				break
			}
		}
	}
	if usageResetAt == 0 {
		usageResetAt = int64(numberField(usageRaw, "nextDateReset"))
	}
	return
}

func deriveLoginProvider(acc ExportAccountInput, auth map[string]interface{}) string {
	return firstNonEmpty(
		stringField(auth, "loginProvider"),
		stringField(auth, "provider"),
		acc.LoginProvider,
		acc.Provider,
		"BuilderId",
	)
}

func ensureKiroAuthToken(auth map[string]interface{}, email, userID, provider, accessToken, refreshToken, tokenType string, expiresAt int64, expiresIn float64, profileARN string) map[string]interface{} {
	if auth == nil {
		auth = map[string]interface{}{}
	}
	auth["accessToken"] = firstNonEmpty(stringField(auth, "accessToken"), accessToken)
	auth["refreshToken"] = firstNonEmpty(stringField(auth, "refreshToken"), refreshToken)
	auth["tokenType"] = firstNonEmpty(stringField(auth, "tokenType"), tokenType, "Bearer")
	if _, ok := auth["expiresIn"]; !ok && expiresIn > 0 {
		auth["expiresIn"] = expiresIn
	}
	if _, ok := auth["expiresAt"]; !ok && expiresAt > 0 {
		auth["expiresAt"] = time.Unix(expiresAt, 0).UTC().Format(time.RFC3339Nano)
	}
	auth["email"] = firstNonEmpty(stringField(auth, "email"), email)
	auth["loginHint"] = firstNonEmpty(stringField(auth, "loginHint"), stringField(auth, "login_hint"), email)
	auth["login_hint"] = firstNonEmpty(stringField(auth, "login_hint"), stringField(auth, "loginHint"), email)
	auth["provider"] = firstNonEmpty(stringField(auth, "provider"), provider)
	auth["loginProvider"] = firstNonEmpty(stringField(auth, "loginProvider"), provider)
	if _, ok := auth["login_option"]; !ok && provider != "" {
		auth["login_option"] = strings.ToLower(provider)
	}
	if userID != "" {
		auth["userId"] = firstNonEmpty(stringField(auth, "userId"), userID)
		auth["user_id"] = firstNonEmpty(stringField(auth, "user_id"), userID)
	}
	if profileARN != "" {
		auth["profileArn"] = firstNonEmpty(stringField(auth, "profileArn"), profileARN)
	}
	if _, ok := auth["authMethod"]; !ok {
		if provider == "Github" || provider == "Google" {
			auth["authMethod"] = "social"
		} else {
			auth["authMethod"] = "builderId"
		}
	}
	return auth
}

// idcRegion 根据 OIDC region 推导 Identity Center 区域代码。
// 所有 BuilderId 账号使用 us-east-1；eu-* 账号使用 eu-central-1。
func idcRegion(region string) string {
	if region != "" && strings.HasPrefix(region, "eu-") {
		return "eu-central-1"
	}
	return "us-east-1"
}

// oidcRegionFromFullURL 从 OIDC 完整 URL 中提取 idc_region 值。
// 例如 "https://oidc.eu-central-1.amazonaws.com/token" → "eu-central-1"
func oidcRegionFromFullURL(oidcURL string) string {
	if strings.Contains(oidcURL, "eu-central-1") {
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
