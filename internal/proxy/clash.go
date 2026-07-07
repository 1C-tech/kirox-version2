package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// ClashRotator 通过 Clash API 自动轮换代理节点
type ClashRotator struct {
	APIURL          string
	Secret          string
	GroupName       string
	Blacklist       []string
	FastestMode     bool
	CurrentProxyURL string
}

var defaultBlacklist = []string{
	"自动", "故障", "剩余", "到期", "官网", "Traffic",
	"DIRECT", "REJECT",
	"港", "HK", "Hongkong",
	"台", "TW", "Taiwan",
	"中", "CN", "China", "回国",
}

// NewClashRotator 创建 Clash 代理轮换器
func NewClashRotator(apiURL, secret, groupName string, blacklist []string, fastestMode bool) *ClashRotator {
	if groupName == "" {
		groupName = "节点选择"
	}
	if len(blacklist) == 0 {
		bl := make([]string, len(defaultBlacklist))
		copy(bl, defaultBlacklist)
		blacklist = bl
	}
	return &ClashRotator{
		APIURL:      strings.TrimRight(apiURL, "/"),
		Secret:      secret,
		GroupName:   groupName,
		Blacklist:   blacklist,
		FastestMode: fastestMode,
	}
}

// SwitchNode 自动切换一个可用节点，返回代理地址
func (r *ClashRotator) SwitchNode(fallbackProxy string) (string, error) {
	if r.APIURL == "" {
		return fallbackProxy, nil
	}

	delays, err := r.getNodeDelays()
	if err != nil {
		return fallbackProxy, fmt.Errorf("获取节点延迟失败: %w", err)
	}

	candidates := r.filterNodes(delays)
	if len(candidates) == 0 {
		return fallbackProxy, fmt.Errorf("所有节点均被黑名单过滤或超时")
	}

	var chosen string
	if r.FastestMode {
		chosen = pickFastest(candidates)
	} else {
		chosen = pickRandom(candidates)
	}

	if err := r.switchProxy(chosen); err != nil {
		return fallbackProxy, fmt.Errorf("切换代理失败: %w", err)
	}

	port, err := r.getMixedPort()
	if err != nil {
		return fallbackProxy, fmt.Errorf("获取 mixed-port 失败: %w", err)
	}

	proxyURL := fmt.Sprintf("http://127.0.0.1:%s", port)
	r.CurrentProxyURL = proxyURL
	return proxyURL, nil
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

func (r *ClashRotator) getNodeDelays() (nodeDelay, error) {
	path := fmt.Sprintf("/groups/%s/delay?timeout=3000&url=http://www.gstatic.com/generate_204", r.GroupName)
	resp, err := r.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var delays nodeDelay
	if err := json.NewDecoder(resp.Body).Decode(&delays); err != nil {
		return nil, err
	}
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
	body := fmt.Sprintf(`{"name":"%s"}`, strings.ReplaceAll(name, `"`, `\"`))
	resp, err := r.doRequest("PUT", "/proxies/"+r.GroupName, strings.NewReader(body))
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

func (r *ClashRotator) getMixedPort() (string, error) {
	resp, err := r.doRequest("GET", "/configs", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var cfg struct {
		MixedPort int `json:"mixed-port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", err
	}
	if cfg.MixedPort <= 0 {
		return "", fmt.Errorf("配置中未设置 mixed-port")
	}
	return fmt.Sprintf("%d", cfg.MixedPort), nil
}
