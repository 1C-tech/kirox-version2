package email

// TokenType 表示 Outlook OAuth 令牌的授权范围类型
type TokenType string

const (
	TokenTypeUnknown  TokenType = ""                // 尚未探测
	TokenTypeGraph    TokenType = "graph_full"       // Microsoft Graph API (mail.read)
	TokenTypeIMAP     TokenType = "outlook_legacy"   // IMAP (IMAP.AccessAsUser.All)
)

// OutlookAccount Outlook 邮箱账号
type OutlookAccount struct {
	Email        string
	Password     string
	ClientID     string
	RefreshToken string
	TokenType    TokenType // 缓存协议类型，空=未探测
}

// ResetTokenType 重置协议探测状态，下次请求时会重新探测
func (a *OutlookAccount) ResetTokenType() {
	a.TokenType = TokenTypeUnknown
}
