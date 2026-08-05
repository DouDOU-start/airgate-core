package account

import "strings"

// 账号类型仅两种：OAuth 订阅凭证、API Key。
// Codex 粘贴 refresh_token 导入仍属 OAuth（与 airgate-openai 一致）；
// Claude setup-token / session_key 也归入 OAuth 凭证字段，不是独立类型。
const (
	TypeOAuth  = "oauth"
	TypeAPIKey = "api_key"
)

// CredentialField 凭证字段定义（管理面动态表单）。
type CredentialField struct {
	Key          string
	Label        string
	Type         string // text / password / textarea
	Required     bool
	Placeholder  string
	EditDisabled bool
}

// AccountType 账号类型定义。
type AccountType struct {
	Key         string
	Label       string
	Description string
	Fields      []CredentialField
}

// CredentialSchema 某平台的凭证 schema。
type CredentialSchema struct {
	Fields       []CredentialField
	AccountTypes []AccountType
}

// SupportedPlatforms 内置账号平台。
// 兼容上游（OpenAI-compatible 等）走「渠道」而非账号池，不单独占平台位。
var SupportedPlatforms = []string{
	"codex",
	"claude",
	"antigravity",
	"kimi",
	"xai",
	"gemini",
	"aistudio",
	"vertex",
}

// GetCredentialsSchema 返回平台凭证 schema。
func (s *Service) GetCredentialsSchema(platform string) CredentialSchema {
	return builtinCredentialSchema(platform)
}

// ListPlatforms 返回支持的平台列表。
func (s *Service) ListPlatforms() []string {
	return append([]string(nil), SupportedPlatforms...)
}

// NormalizeAccountType 将历史/别名类型收敛为 oauth | api_key。
// refresh_token / setup_token / session 等导入方式均视为 OAuth；
// apikey / service_account 视为 API Key。
func NormalizeAccountType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", TypeOAuth, "refresh_token", "setup_token", "session", "device":
		return TypeOAuth
	case TypeAPIKey, "apikey", "api-key", "service_account", "service-account":
		return TypeAPIKey
	default:
		// 未知值默认按 OAuth（订阅账号池主路径）。
		return TypeOAuth
	}
}

func builtinCredentialSchema(platform string) CredentialSchema {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "codex":
		return CredentialSchema{
			AccountTypes: []AccountType{
				{
					Key:         TypeOAuth,
					Label:       "OAuth",
					Description: "ChatGPT / Codex 订阅。可粘贴 refresh_token 导入（自动换 token），或填 access_token + refresh_token",
					Fields: []CredentialField{
						{Key: "refresh_token", Label: "Refresh Token", Type: "password", Required: false, Placeholder: "仅 RT 即可导入"},
						{Key: "access_token", Label: "Access Token", Type: "password", Required: false},
						{Key: "chatgpt_account_id", Label: "ChatGPT Account ID", Type: "text", Required: false},
						{Key: "email", Label: "Email", Type: "text", Required: false},
						{Key: "id_token", Label: "ID Token", Type: "password", Required: false},
						{Key: "expired", Label: "Token Expiry", Type: "text", Required: false},
					},
				},
				{
					Key:         TypeAPIKey,
					Label:       "API Key",
					Description: "OpenAI / Codex API Key（sk-...）",
					Fields: []CredentialField{
						{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "sk-..."},
					},
				},
			},
		}
	case "claude":
		return CredentialSchema{
			AccountTypes: []AccountType{
				{
					Key:         TypeOAuth,
					Label:       "OAuth",
					Description: "Claude Code / Claude.ai OAuth；也可粘贴 setup-token 到 access_token",
					Fields: []CredentialField{
						{Key: "access_token", Label: "Access Token / Setup Token", Type: "password", Required: false},
						{Key: "refresh_token", Label: "Refresh Token", Type: "password", Required: false},
						{Key: "session_key", Label: "Session Key", Type: "password", Required: false, Placeholder: "sk-ant-sid01-..."},
						{Key: "email", Label: "Email", Type: "text", Required: false},
						{Key: "expired", Label: "Token Expiry", Type: "text", Required: false},
					},
				},
				{
					Key:         TypeAPIKey,
					Label:       "API Key",
					Description: "Anthropic 官方 API Key（sk-ant-...）",
					Fields: []CredentialField{
						{Key: "api_key", Label: "API Key", Type: "password", Required: true, Placeholder: "sk-ant-..."},
					},
				},
			},
		}
	case "antigravity":
		return CredentialSchema{
			AccountTypes: []AccountType{{
				Key:         TypeOAuth,
				Label:       "OAuth",
				Description: "Google Antigravity / Gemini CLI OAuth",
				Fields: []CredentialField{
					{Key: "access_token", Label: "Access Token", Type: "password", Required: false},
					{Key: "refresh_token", Label: "Refresh Token", Type: "password", Required: false},
					{Key: "email", Label: "Email", Type: "text", Required: false},
					{Key: "project_id", Label: "Project ID", Type: "text", Required: false},
					{Key: "expired", Label: "Token Expiry", Type: "text", Required: false},
				},
			}},
		}
	case "kimi":
		return CredentialSchema{
			AccountTypes: []AccountType{
				{
					Key:         TypeOAuth,
					Label:       "OAuth",
					Description: "Kimi Code 设备码 OAuth",
					Fields: []CredentialField{
						{Key: "access_token", Label: "Access Token", Type: "password", Required: false},
						{Key: "refresh_token", Label: "Refresh Token", Type: "password", Required: false},
						{Key: "email", Label: "Email", Type: "text", Required: false},
						{Key: "expired", Label: "Token Expiry", Type: "text", Required: false},
					},
				},
				{
					Key:         TypeAPIKey,
					Label:       "API Key",
					Description: "Kimi Open Platform API Key",
					Fields: []CredentialField{
						{Key: "api_key", Label: "API Key", Type: "password", Required: true},
					},
				},
			},
		}
	case "xai":
		return CredentialSchema{
			AccountTypes: []AccountType{
				{
					Key:         TypeOAuth,
					Label:       "OAuth",
					Description: "xAI Grok Build / CLI 设备码 OAuth",
					Fields: []CredentialField{
						{Key: "access_token", Label: "Access Token", Type: "password", Required: false},
						{Key: "refresh_token", Label: "Refresh Token", Type: "password", Required: false},
						{Key: "email", Label: "Email", Type: "text", Required: false},
						{Key: "expired", Label: "Token Expiry", Type: "text", Required: false},
						{Key: "base_url", Label: "Base URL", Type: "text", Required: false, Placeholder: "https://cli-chat-proxy.grok.com/v1"},
					},
				},
				{
					Key:         TypeAPIKey,
					Label:       "API Key",
					Description: "xAI 官方 API Key",
					Fields: []CredentialField{
						{Key: "api_key", Label: "API Key", Type: "password", Required: true},
					},
				},
			},
		}
	case "gemini", "aistudio":
		return CredentialSchema{
			AccountTypes: []AccountType{{
				Key:         TypeAPIKey,
				Label:       "API Key",
				Description: "Google AI Studio / Gemini API Key",
				Fields: []CredentialField{
					{Key: "api_key", Label: "API Key", Type: "password", Required: true},
					{Key: "project_id", Label: "Project ID", Type: "text", Required: false},
				},
			}},
		}
	case "vertex":
		// 服务账号 JSON 归入 API Key 类型（非 OAuth 订阅）。
		return CredentialSchema{
			AccountTypes: []AccountType{{
				Key:         TypeAPIKey,
				Label:       "API Key",
				Description: "Vertex AI 服务账号 JSON",
				Fields: []CredentialField{
					{Key: "service_account_json", Label: "Service Account JSON", Type: "textarea", Required: true},
					{Key: "project_id", Label: "Project ID", Type: "text", Required: false},
					{Key: "location", Label: "Location", Type: "text", Required: false, Placeholder: "us-central1"},
				},
			}},
		}
	default:
		return CredentialSchema{
			AccountTypes: []AccountType{
				{
					Key:         TypeOAuth,
					Label:       "OAuth",
					Description: "通用 OAuth 凭证",
					Fields: []CredentialField{
						{Key: "access_token", Label: "Access Token", Type: "password", Required: false},
						{Key: "refresh_token", Label: "Refresh Token", Type: "password", Required: false},
						{Key: "email", Label: "Email", Type: "text", Required: false},
					},
				},
				{
					Key:         TypeAPIKey,
					Label:       "API Key",
					Description: "通用 API Key",
					Fields: []CredentialField{
						{Key: "api_key", Label: "API Key", Type: "password", Required: true},
					},
				},
			},
		}
	}
}
