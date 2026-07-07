package core

import (
	"math/rand"
	"strings"

	"reg_go/internal/email"
)

// Config 注册配置
type Config struct {
	OIDCBase    string
	SigninBase  string
	ProfileBase string
	ViewBase    string
	PortalBase  string
	DirectoryID string
	StartURL    string

	KiroBase        string
	KiroRedirectURI string

	Password string
	FullName string

	Proxy string
	Debug bool

	EmailProvider  string
	UseOutlook     bool
	OutlookAccount *email.OutlookAccount

	UseMoeMail      bool
	MoeMailConfig   *email.MoeMailConfig
	MoeMailProvider *email.MoeMailProvider

	UseCloudMail      bool
	CloudMailConfig   *email.CloudMailConfig
	CloudMailProvider *email.CloudMailProvider

	UseCFTempEmail      bool
	CFTempEmailConfig   *email.CFTempEmailConfig
	CFTempEmailProvider *email.CFTempEmailProvider
	CFTempEmailJWT      string

	MoEmailBaseURL string
	MoEmailAPIKey  string

	// 反检测配置（所有配置默认关闭，零配置向后兼容）
	ClashConfig     *ClashConfig     `json:"clash_config,omitempty"`
	AntiDetect      *AntiDetectConfig `json:"anti_detect,omitempty"`
}

// ClashConfig Clash 代理轮换配置
type ClashConfig struct {
	Enable      bool     `json:"enable"`
	FastestMode bool     `json:"fastest_mode"`
	APIURL      string   `json:"api_url"`       // 例如 http://127.0.0.1:9097
	Secret      string   `json:"secret"`
	GroupName   string   `json:"group_name"`    // 默认 "节点选择"
	MixedPort   int      `json:"mixed_port"`    // Clash mixed-port，>=1 时跳过 /configs API 查询
	Blacklist   []string `json:"blacklist"`
	TestProxyURL string  `json:"test_proxy_url"` // 测活用代理 URL
}

// AntiDetectConfig 反检测功能配置
type AntiDetectConfig struct {
	EnableTraceHeaders            bool `json:"enable_trace_headers"`
	RefreshFingerprintPerAccount  bool `json:"refresh_fingerprint_per_account"`
	EnableIPPrecheck              bool `json:"enable_ip_precheck"`
	EnableClashRotation           bool `json:"enable_clash_rotation"`
}

// NewConfig 创建默认配置
func NewConfig() *Config {
	return &Config{
		OIDCBase:        "https://oidc.us-east-1.amazonaws.com",
		SigninBase:      "https://us-east-1.signin.aws",
		ProfileBase:     "https://profile.aws.amazon.com",
		ViewBase:        "https://view.awsapps.com",
		PortalBase:      "https://portal.sso.us-east-1.amazonaws.com",
		DirectoryID:     "d-9067642ac7",
		StartURL:        "https://view.awsapps.com/start",
		KiroBase:        "https://app.kiro.dev",
		KiroRedirectURI: "https://app.kiro.dev/signin/oauth",
		Password:        GenPassword(),
		FullName:        "Test User",
		// 反检测模块默认 nil，零配置向后兼容
	}
}

// GenPassword 生成随机密码
func GenPassword() string {
	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower := "abcdefghijklmnopqrstuvwxyz"
	digits := "0123456789"
	special := "!@#$%^&*"

	var b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteByte(upper[rand.Intn(len(upper))])
	}
	for i := 0; i < 6; i++ {
		b.WriteByte(lower[rand.Intn(len(lower))])
	}
	for i := 0; i < 3; i++ {
		b.WriteByte(digits[rand.Intn(len(digits))])
	}
	for i := 0; i < 2; i++ {
		b.WriteByte(special[rand.Intn(len(special))])
	}
	pw := []byte(b.String())
	rand.Shuffle(len(pw), func(i, j int) { pw[i], pw[j] = pw[j], pw[i] })
	return string(pw)
}

