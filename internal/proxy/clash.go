package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ClashRotator 通过 Clash API 自动轮换代理节点
type ClashRotator struct {
	APIURL          string
	Secret          string
	GroupName       string
	MixedPort       int
	Blacklist       []string
	FastestMode     bool
	CurrentProxyURL string

	// 模糊匹配后的实际策略组名（如 "🚀 节点选择"）
	// 在 getGroupProxies 成功后设置，switchProxy 使用此名而非 GroupName
	actualGroupName string
}

var defaultBlacklist = []string{
	"自动", "故障", "剩余", "到期", "官网", "Traffic",
	"DIRECT", "REJECT",
	"港", "HK", "Hongkong", "\U0001F1ED\U0001F1F0", // 🇭🇰 HK flag emoji
	"台", "TW", "Taiwan", "\U0001F1F9\U0001F1FC",   // 🇹🇼 TW flag emoji
	"中", "CN", "China", "回国",
}

// normalizeAPIURL 确保 API URL 有 scheme 且格式正确
// 处理:
//   127.0.0.1:49544      → http://127.0.0.1:49544
//   http:127.0.0.1:49544 → http://127.0.0.1:49544
//   http://127.0.0.1:49544 → 不变
func normalizeAPIURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	// 统一加 scheme: 无 scheme 或只有 http: 的都要补
	if strings.HasPrefix(raw, "http:") && !strings.HasPrefix(raw, "http://") {
		raw = "http://" + strings.TrimPrefix(raw, "http:")
	} else if strings.HasPrefix(raw, "https:") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + strings.TrimPrefix(raw, "https:")
	} else if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	// 修复多重斜杠: http:/// → http://
	for strings.Contains(raw, ":///") {
		raw = strings.Replace(raw, ":///", "://", 1)
	}
	return strings.TrimRight(raw, "/")
}

// NewClashRotator 创建 Clash 代理轮换器
func NewClashRotator(apiURL, secret, groupName string, mixedPort int, blacklist []string, fastestMode bool) *ClashRotator {
	if groupName == "" {
		groupName = "节点选择"
	}
	if len(blacklist) == 0 {
		bl := make([]string, len(defaultBlacklist))
		copy(bl, defaultBlacklist)
		blacklist = bl
	}
	return &ClashRotator{
		APIURL:      normalizeAPIURL(apiURL),
		Secret:      secret,
		GroupName:   groupName,
		MixedPort:   mixedPort,
		Blacklist:   blacklist,
		FastestMode: fastestMode,
	}
}

// SwitchNode 自动切换一个可用节点，返回代理地址
// 参考 CPA: utils/proxy_manager.py _do_smart_switch() (L153-300)
// 支持 FastestMode 优选测速 + 随机抽卡回退 + 切换后测活验证
func (r *ClashRotator) SwitchNode(fallbackProxy string) (string, error) {
	if r.APIURL == "" {
		return fallbackProxy, nil
	}

	// CPA L165-192: 获取节点 → 解析策略组 → 黑名单过滤
	delays, err := r.getNodeDelays()
	if err != nil {
		return fallbackProxy, fmt.Errorf("获取节点延迟失败: %w", err)
	}

	candidates := r.filterNodes(delays)
	if len(candidates) == 0 {
		return fallbackProxy, fmt.Errorf("所有节点均被黑名单过滤或超时")
	}

	proxyURL, err := r.trySwitch(candidates, fallbackProxy)
	if err != nil {
		return fallbackProxy, err
	}
	r.CurrentProxyURL = proxyURL
	return proxyURL, nil
}

// trySwitch 尝试切换节点，最多重试 10 次（CPA L276-295）
func (r *ClashRotator) trySwitch(candidates map[string]int, fallbackProxy string) (string, error) {
	maxRetries := 10

	// 获取 mixed-port 用于构建代理地址
	port, err := r.getMixedPort()
	if err != nil {
		return "", fmt.Errorf("获取 mixed-port 失败: %w", err)
	}
	proxyBase := fmt.Sprintf("http://127.0.0.1:%s", port)

	// CPA L217-269: FastestMode 优选模式
	if r.FastestMode {
		if best := r.fastestModePick(candidates); best != "" {
			if err := r.switchProxy(best); err == nil {
				// CPA L263: 切换后测活验证
				if !r.testProxyLiveness(proxyBase) {
					log.Printf("[Clash] 最优节点 %s 测活失败，回退随机模式", cleanNodeName(best))
				} else {
					return proxyBase, nil
				}
			}
		}
	}

	// CPA L271-295: 随机抽卡模式（含重试）
	// 优选模式失败也会回退到这里
	candidateNames := make([]string, 0, len(candidates))
	for name := range candidates {
		candidateNames = append(candidateNames, name)
	}
	// 按延迟排序，低延迟优先
	sort.SliceStable(candidateNames, func(i, j int) bool {
		return candidates[candidateNames[i]] < candidates[candidateNames[j]]
	})

	for i := 1; i <= maxRetries; i++ {
		selected := candidateNames[rand.Intn(len(candidateNames))]
		log.Printf("[Clash] 尝试切换节点 [%s] (%d/%d)", cleanNodeName(selected), i, maxRetries)

		if err := r.switchProxy(selected); err != nil {
			log.Printf("[Clash] 切换失败: %v", err)
			continue
		}

		// CPA L288-289: 等待 1.5s 后测活
		time.Sleep(1500 * time.Millisecond)
		if r.testProxyLiveness(proxyBase) {
			return proxyBase, nil
		}
		log.Printf("[Clash] 节点 %s 测活失败，继续抽卡", cleanNodeName(selected))
	}

	return "", fmt.Errorf("连续 %d 次抽卡均不可用", maxRetries)
}

// fastestModePick 优选模式：并发测速选择最低延迟节点
// 参考 CPA L217-269
func (r *ClashRotator) fastestModePick(candidates map[string]int) string {
	log.Printf("[Clash] 开启优选模式，测速 %d 个节点...", len(candidates))

	// 并发触发测速
	var wg sync.WaitGroup
	worker := make(chan struct{}, 5) // 最大并发 5
	for name := range candidates {
		wg.Add(1)
		worker <- struct{}{}
		go func(nodeName string) {
			defer wg.Done()
			defer func() { <-worker }()
			encName := url.QueryEscape(nodeName)
			// CPA L222-228: GET /proxies/{name}/delay
			r.doRequest("GET", "/proxies/"+encName+"/delay?timeout=2000&url=http://www.gstatic.com/generate_204", nil)
		}(name)
	}
	wg.Wait()

	// 等待测速完成（CPA L238）
	time.Sleep(1500 * time.Millisecond)

	// 重新读取延迟选最优
	delays, err := r.getNodeDelays()
	if err != nil {
		return ""
	}

	var bestNode string
	bestDelay := int(^uint(0) >> 1)
	for name, delay := range delays {
		if delay <= 0 || delay >= bestDelay {
			continue
		}
		// 仍需检查黑名单
		if r.isBlacklisted(name) {
			continue
		}
		bestDelay = delay
		bestNode = name
	}
	return bestNode
}

// testProxyLiveness 通过 cloudflare trace 验证代理可用性
// 参考 CPA proxy_manager.py L106-131 test_proxy_liveness()
func (r *ClashRotator) testProxyLiveness(proxyURL string) bool {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		log.Printf("[Clash] 测活链接解析失败: %v", err)
		return false
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(parsedURL),
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	resp, err := client.Get("https://cloudflare.com/cdn-cgi/trace")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "loc=") {
			loc := strings.TrimPrefix(line, "loc=")
			loc = strings.TrimSpace(loc)
			if loc == "CN" || loc == "HK" {
				log.Printf("[Clash] 节点地理受限 (%s)，弃用", loc)
				return false
			}
			return true
		}
	}
	return false
}

func cleanNodeName(name string) string {
	// 移除 Emoji 等特殊字符用于日志
	return strings.TrimSpace(name)
}

func (r *ClashRotator) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, r.APIURL+path, body)
	if err != nil {
		return nil, err
	}
	if r.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+r.Secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}

type nodeDelay map[string]int

// stripEmoji 移除字符串中的 Emoji 及装饰字符（用于模糊匹配）
func stripEmoji(s string) string {
	// Unicode 范围: 杂项符号、表情符号、国旗、修饰符
	re := regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{1F1E6}-\x{1F1FF}\x{FE0F}]`)
	return strings.TrimSpace(re.ReplaceAllString(s, ""))
}

// fuzzyMatchGroup 从 Clash /proxies 响应中模糊匹配策略组名
// 参考 CPA: utils/clash_group_utils.py resolve_group_name()
// 支持 节点选择 ↔ 🚀 节点选择（自动去除 Emoji 后比较）
func fuzzyMatchGroup(proxiesData map[string]interface{}, desired string) string {
	cleaned := stripEmoji(strings.ToLower(desired))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	for key, val := range proxiesData {
		v, ok := val.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasAll := v["all"]; !hasAll {
			continue // 不是 Selector/URLTest 组
		}
		// 完全匹配
		if key == desired {
			return key
		}
		// 模糊匹配：去 Emoji + 去空格后包含关系
		keyClean := stripEmoji(strings.ToLower(key))
		keyClean = strings.ReplaceAll(keyClean, " ", "")
		if strings.Contains(keyClean, cleaned) || strings.Contains(cleaned, keyClean) {
			return key
		}
	}
	return ""
}

// getGroupProxies 获取策略组成员列表
// 参考 CPA: proxy_manager.py L166-187
// 1. GET /proxies 一次性获取全部代理
// 2. 模糊匹配实际策略组名
// 3. 返回成员列表
// ListGroups 从 Clash API 获取所有可用的策略组名称
func ListGroups(apiURL, secret string) ([]string, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL 为空")
	}
	req, err := http.NewRequest("GET", strings.TrimRight(apiURL, "/")+"/proxies", nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 Clash API 失败: %w", err)
	}
	defer resp.Body.Close()
	var data struct {
		Proxies map[string]interface{} `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	var groups []string
	for key, val := range data.Proxies {
		v, ok := val.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasAll := v["all"]; hasAll {
			groups = append(groups, key)
		}
	}
	sort.Strings(groups)
	return groups, nil
}

func (r *ClashRotator) getGroupProxies() ([]string, string, error) {
	resp, err := r.doRequest("GET", "/proxies", nil)
	if err != nil {
		return nil, "", fmt.Errorf("获取代理列表失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("获取代理列表失败: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var data struct {
		Proxies map[string]interface{} `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, "", err
	}
	actualName := fuzzyMatchGroup(data.Proxies, r.GroupName)
	if actualName == "" {
		// 列出可用组名方便调试
		var groups []string
		for key, val := range data.Proxies {
			v, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasAll := v["all"]; hasAll {
				groups = append(groups, stripEmoji(key))
			}
		}
		return nil, "", fmt.Errorf("找不到策略组 '%s' (可用: %v)", r.GroupName, groups)
	}
	// 缓存实际组名供 switchProxy 使用
	r.actualGroupName = actualName
	group, ok := data.Proxies[actualName].(map[string]interface{})
	if !ok {
		return nil, "", fmt.Errorf("策略组 '%s' 类型异常", actualName)
	}
	allRaw, ok := group["all"].([]interface{})
	if !ok {
		return nil, "", fmt.Errorf("策略组 '%s' 缺少成员列表", actualName)
	}
	var members []string
	for _, m := range allRaw {
		if s, ok := m.(string); ok {
			members = append(members, s)
		}
	}
	return members, actualName, nil
}

// getNodeDelays 逐个测试策略组中所有代理节点的延迟
// 参考 CPA: proxy_manager.py L217-269
// 1. GET /proxies 获取全部代理 → 模糊匹配组名 → 获取成员列表
// 2. 并发测速每个成员 GET /proxies/{name}/delay
func (r *ClashRotator) getNodeDelays() (nodeDelay, error) {
	members, actualName, err := r.getGroupProxies()
	if err != nil {
		return nil, fmt.Errorf("获取策略组成员: %w", err)
	}
	if actualName != r.GroupName {
		log.Printf("[Clash] 策略组模糊匹配 '%s' → '%s'", r.GroupName, actualName)
	}

	delays := make(nodeDelay)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // 最大并发 5

	for _, name := range members {
		// 过滤掉 DIRECT/REJECT 等内置节点
		if name == "DIRECT" || name == "REJECT" || name == "GLOBAL" || name == "PASS" {
			continue
		}
		// 跳过黑名单
		if r.isBlacklisted(name) {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(nodeName string) {
			defer wg.Done()
			defer func() { <-sem }()
			encName := url.PathEscape(nodeName)
			path := fmt.Sprintf("/proxies/%s/delay?timeout=3000&url=http://www.gstatic.com/generate_204", encName)
			resp, err := r.doRequest("GET", path, nil)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				return
			}
			var result struct {
				Delay int `json:"delay"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return
			}
			if result.Delay > 0 {
				mu.Lock()
				delays[nodeName] = result.Delay
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()
	return delays, nil
}

func (r *ClashRotator) isBlacklisted(name string) bool {
	nameLower := strings.ToLower(name)
	for _, b := range r.Blacklist {
		if strings.Contains(nameLower, strings.ToLower(b)) {
			return true
		}
	}
	return false
}

func (r *ClashRotator) filterNodes(delays nodeDelay) map[string]int {
	result := make(map[string]int, len(delays))
	for name, delay := range delays {
		if delay <= 0 {
			continue
		}
		if r.isBlacklisted(name) {
			continue
		}
		result[name] = delay
	}
	return result
}

func pickFastest(candidates map[string]int) string {
	var best string
	bestDelay := int(^uint(0) >> 1)
	for name, delay := range candidates {
		if delay < bestDelay {
			bestDelay = delay
			best = name
		}
	}
	return best
}

func pickRandom(candidates map[string]int) string {
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	return names[rand.Intn(len(names))]
}

func (r *ClashRotator) switchProxy(name string) error {
	// 优先使用模糊匹配后的实际组名，否则回退到用户配置的组名
	groupName := r.actualGroupName
	if groupName == "" {
		groupName = r.GroupName
	}
	encGroup := url.PathEscape(groupName)
	body := fmt.Sprintf(`{"name":"%s"}`, strings.ReplaceAll(name, `"`, `\"`))
	resp, err := r.doRequest("PUT", "/proxies/"+encGroup, strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// AutoDetectClashConfig 从 Clash 配置文件自动读取 external-controller 和 secret。
// 典型路径: %USERPROFILE%\.config\clash\config.yaml（CFW）/ ~/.config/clash/config.yaml（Linux）
func AutoDetectClashConfig() (apiURL, secret string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	path := filepath.Join(home, ".config", "clash", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		// 也尝试 clash-verge / mihomo 路径
		path2 := filepath.Join(home, ".config", "mihomo", "config.yaml")
		if data2, e2 := os.ReadFile(path2); e2 == nil {
			data = data2
		} else {
			return "", ""
		}
	}
	var ec, sec string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "external-controller:") || strings.HasPrefix(line, "external-controller ") {
			ec = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "external-controller:"), "external-controller "))
			ec = strings.Trim(ec, "\"'")
		}
		if strings.HasPrefix(line, "secret:") || strings.HasPrefix(line, "secret ") {
			sec = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "secret:"), "secret "))
			sec = strings.Trim(sec, "\"'")
		}
	}
	if ec == "" {
		return "", ""
	}
	if !strings.Contains(ec, "://") {
		apiURL = "http://" + ec
	} else {
		apiURL = ec
	}
	return apiURL, sec
}

func (r *ClashRotator) getMixedPort() (string, error) {
	// 优先使用配置中指定的端口，跳过 API 查询
	if r.MixedPort > 0 {
		return fmt.Sprintf("%d", r.MixedPort), nil
	}
	resp, err := r.doRequest("GET", "/configs", nil)
	if err != nil {
		return "7890", nil // 默认 fallback
	}
	defer resp.Body.Close()

	var cfg struct {
		MixedPort int `json:"mixed-port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "7890", nil
	}
	if cfg.MixedPort <= 0 {
		return "7890", nil
	}
	return fmt.Sprintf("%d", cfg.MixedPort), nil
}
