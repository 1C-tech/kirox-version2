package email

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"reg_go/internal/storage"
)

// getCFTempEmailConfigPath 配置文件路径
func getCFTempEmailConfigPath() string {
	return filepath.Join(storage.GetDataDir(), "cftempemail.dat")
}

// GetCFTempEmailConfigs 读取配置列表
func GetCFTempEmailConfigs() []CFTempEmailConfig {
	data, err := os.ReadFile(getCFTempEmailConfigPath())
	if err != nil {
		return []CFTempEmailConfig{}
	}

	var configs []CFTempEmailConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		log.Printf("[CFTempEmail] 配置文件格式无效，已重置: %v", err)
		os.Remove(getCFTempEmailConfigPath())
		return []CFTempEmailConfig{}
	}
	return configs
}

// SaveCFTempEmailConfigs 保存配置列表
func SaveCFTempEmailConfigs(configsJSON string) map[string]interface{} {
	var configs []CFTempEmailConfig
	if err := json.Unmarshal([]byte(configsJSON), &configs); err != nil {
		return map[string]interface{}{"error": "配置格式错误: " + err.Error()}
	}

	for i, cfg := range configs {
		if cfg.Name == "" {
			return map[string]interface{}{"error": fmt.Sprintf("第 %d 个配置缺少名称", i+1)}
		}
		if cfg.URL == "" {
			return map[string]interface{}{"error": fmt.Sprintf("配置 %s 缺少 URL", cfg.Name)}
		}
		if cfg.AdminAuth == "" {
			return map[string]interface{}{"error": fmt.Sprintf("配置 %s 缺少 Admin 鉴权密码", cfg.Name)}
		}
		// 域名字段可选：未填则注册时需手动指定
	}

	jsonData, _ := json.Marshal(configs)
	os.MkdirAll(filepath.Dir(getCFTempEmailConfigPath()), 0755)
	if err := os.WriteFile(getCFTempEmailConfigPath(), jsonData, 0600); err != nil {
		return map[string]interface{}{"error": "保存失败: " + err.Error()}
	}

	log.Printf("[CFTempEmail] 已保存 %d 个配置", len(configs))
	return map[string]interface{}{"success": true}
}

// TestCFTempEmailConnection 测试配置：调用 admin API 探活，返回用户填写的域名列表
func TestCFTempEmailConnection(configJSON string) map[string]interface{} {
	var config CFTempEmailConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return map[string]interface{}{"error": "配置格式错误: " + err.Error()}
	}

	client := NewCFTempEmailClient(config)
	domains, err := client.TestConnection()
	if err != nil {
		return map[string]interface{}{"error": "连接失败: " + err.Error()}
	}

	return map[string]interface{}{
		"success":     true,
		"domains":     domains,
		"domainCount": len(domains),
	}
}