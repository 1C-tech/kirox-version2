package proxy

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	httputil "reg_go/internal/http"
)

var ErrRegionBlocked = fmt.Errorf("当前代理地区为 CN/HK，可能触发风控")

var cloudflareTraceURL = "https://cloudflare.com/cdn-cgi/trace"

func PreCheckProxy(proxyURL string) error {
	if proxyURL == "" {
		return nil
	}
	return preCheckProxyImpl(proxyURL, cloudflareTraceURL)
}

func preCheckProxyImpl(proxyURL, targetURL string) error {
	start := time.Now()
	client := httputil.NewTLSClient(proxyURL, true)
	req, _ := fhttp.NewRequest("GET", targetURL, nil)
	req.Header.Set("User-Agent", "kirox/proxy-check")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare trace 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("cloudflare trace 返回状态码 %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	var loc, ip, colo string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "loc":
			loc = parts[1]
		case "ip":
			ip = parts[1]
		case "colo":
			colo = parts[1]
		}
	}

	log.Printf("[代理预检] 地区: %s | 延迟: %.2fs | Colo: %s | IP: %s", loc, elapsed.Seconds(), colo, ip)

	if loc == "CN" || loc == "HK" {
		return ErrRegionBlocked
	}

	return nil
}
