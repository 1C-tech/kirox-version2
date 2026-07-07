package data

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// SaveKiroSuccess 以明文 JSON 数组形式把成功注册的账号写入 outDir/accounts.json。
// 同邮箱以最新一条覆盖；仅处理成功记录（失败/封号不落盘，只留在运行日志）。
func SaveKiroSuccess(result map[string]interface{}, outDir string) error {
	if result == nil || result["status"] != "success" {
		return nil
	}
	emailAddr, _ := result["email"].(string)
	if emailAddr == "" {
		return fmt.Errorf("缺少 email 字段")
	}

	at, _ := result["aws_token"].(map[string]interface{})
	if at == nil {
		at = map[string]interface{}{}
	}
	kiroTokens, _ := result["kiro_tokens"].(map[string]interface{})
	if kiroTokens == nil {
		kiroTokens = map[string]interface{}{}
	}
	verify, _ := result["verify"].(map[string]interface{})
	item := map[string]interface{}{
		"refreshToken":    at["refreshToken"],
		"awsRefreshToken": at["refreshToken"],
		"awsClientId":     result["client_id"],
		"awsClientSecret": result["client_secret"],
		"provider":        "BuilderId",
		"clientId":        result["client_id"],
		"clientSecret":    result["client_secret"],
		"region":          "us-east-1",
		"email":           emailAddr,
		"time":            time.Now().Format("2006-01-02 15:04:05"),
	}
	if rt, _ := kiroTokens["refreshToken"].(string); rt != "" {
		item["kiroRefreshToken"] = rt
	}
	if at, _ := kiroTokens["accessToken"].(string); at != "" {
		item["kiroAccessToken"] = at
	}
	if tt, _ := kiroTokens["tokenType"].(string); tt != "" {
		item["kiroTokenType"] = tt
	}
	if ei, ok := kiroTokens["expiresIn"]; ok {
		item["kiroExpiresIn"] = ei
	}
	if v := result["kiro_client_id"]; v != nil {
		item["kiroClientId"] = v
	}
	if v := result["kiro_client_secret"]; v != nil {
		item["kiroClientSecret"] = v
	}
	if len(kiroTokens) > 0 {
		item["kiroAuthTokenRaw"] = kiroTokens
	}
	if verify != nil {
		// subscription: 优先用 API 返回的真实值，为空时兜底 "Free"
		sub, _ := verify["subscription"].(string)
		if sub == "" {
			sub = "Free"
		}
		item["subscription"] = sub
		// creditUsed / creditLimit: nil 时兜底 0，避免 JSON null 导致服务器导入报错
		if cu := verify["credit_used"]; cu != nil {
			item["creditUsed"] = cu
		} else {
			item["creditUsed"] = 0
		}
		if cl := verify["credit_limit"]; cl != nil {
			item["creditLimit"] = cl
		} else {
			item["creditLimit"] = 0
		}
		if arn, _ := verify["profileArn"].(string); arn != "" {
			item["profileArn"] = arn
		}
		if raw, ok := verify["usageRaw"].(map[string]interface{}); ok && raw != nil {
			item["kiroUsageRaw"] = raw
		}
	} else {
		item["subscription"] = "Free"
		item["creditUsed"] = 0
		item["creditLimit"] = 0
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	path := filepath.Join(outDir, "accounts.json")

	existing, err := loadJSONArray(path)
	if err != nil {
		return fmt.Errorf("读取 accounts.json 失败: %w", err)
	}

	merged := make([]map[string]interface{}, 0, len(existing)+1)
	for _, e := range existing {
		if em, _ := e["email"].(string); em == emailAddr {
			continue
		}
		merged = append(merged, e)
	}
	merged = append(merged, item)

	if err := writeJSONArrayAtomic(path, merged); err != nil {
		return fmt.Errorf("写入 accounts.json 失败: %w", err)
	}
	log.Printf("[Kiro] 结果已保存: %s", path)
	return nil
}

// LoadAccounts 读取 outDir/accounts.json 中保存的账号列表（按写入顺序返回）。
func LoadAccounts(outDir string) ([]map[string]interface{}, error) {
	return loadJSONArray(filepath.Join(outDir, "accounts.json"))
}

// DeleteAccount 从 outDir/accounts.json 中移除指定邮箱的账号；返回是否实际删除。
func DeleteAccount(outDir, email string) (bool, error) {
	path := filepath.Join(outDir, "accounts.json")
	existing, err := loadJSONArray(path)
	if err != nil || len(existing) == 0 {
		return false, err
	}
	out := make([]map[string]interface{}, 0, len(existing))
	removed := false
	for _, e := range existing {
		if em, _ := e["email"].(string); em == email {
			removed = true
			continue
		}
		out = append(out, e)
	}
	if !removed {
		return false, nil
	}
	if err := writeJSONArrayAtomic(path, out); err != nil {
		return false, err
	}
	return true, nil
}

func loadJSONArray(path string) ([]map[string]interface{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

func writeJSONArrayAtomic(path string, arr []map[string]interface{}) error {
	b, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
