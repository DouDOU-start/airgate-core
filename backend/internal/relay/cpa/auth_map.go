package cpa

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// AccountAuthInput 将 airgate 账号快照映射为 CPA Auth 所需的最小字段。
type AccountAuthInput struct {
	// AccountID airgate 账号主键（写入 Auth.ID 前缀保证唯一）。
	AccountID int
	// Name 展示名。
	Name string
	// Platform CPA provider 键（codex / xai / claude / ...）。
	Platform string
	// Type 账号接入方式（仅 oauth / api_key）。
	Type string
	// Credentials 解密后的凭证 map（access_token / refresh_token / api_key 等）。
	Credentials map[string]string
	// ProxyURL 出站代理 URL（可空）；格式如 http://user:pass@host:port 或 socks5://...
	ProxyURL string
}

// MapAuth 把 airgate 账号凭证映射为 CPA sdk/cliproxy/auth.Auth。
//
// 约定：
//   - api_key / base_url → Attributes（executor 优先读 Attributes）
//   - access_token / refresh_token / id_token / expired / email / account_id 等 → Metadata
//   - credentials 中其余非空键一律写入 Metadata（兼容各平台扩展字段）
//
// 不修改入参 Credentials。
func MapAuth(in AccountAuthInput) (*coreauth.Auth, error) {
	if in.AccountID <= 0 {
		return nil, fmt.Errorf("account_id 无效")
	}
	provider := ResolveProvider(in.Platform)
	if provider == "" {
		return nil, fmt.Errorf("platform 为空")
	}
	if len(in.Credentials) == 0 {
		return nil, fmt.Errorf("凭证为空")
	}

	attrs := make(map[string]string, 8)
	meta := make(map[string]any, len(in.Credentials)+4)

	for k, v := range in.Credentials {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "api_key":
			attrs["api_key"] = val
			// 部分 executor 也会从 Metadata 读 access_token；api_key 同步一份不伤。
			if _, exists := meta["access_token"]; !exists {
				meta["access_token"] = val
			}
		case "base_url":
			attrs["base_url"] = val
			meta["base_url"] = val
		case "access_token", "refresh_token", "id_token", "session_token",
			"email", "expired", "expire", "expires_at", "account_id",
			"token_type", "token_endpoint", "auth_kind", "type",
			"last_refresh", "sub", "project_id", "project", "location",
			"using_api", "compat_name":
			meta[strings.ToLower(key)] = val
		default:
			// 其余字段原样进 Metadata，键保持原始大小写（部分字段大小写敏感）。
			meta[key] = val
		}
	}

	// type 优先用账号 Type，其次凭证 type，最后 platform。
	authType := strings.TrimSpace(in.Type)
	if authType == "" {
		if v, ok := meta["type"].(string); ok && v != "" {
			authType = v
		} else {
			authType = provider
		}
	}
	meta["type"] = authType

	// xAI executor 通过 auth_kind 判断订阅 OAuth 应走 Grok Build
	// cli-chat-proxy，不能仅依赖凭证里是否刚好带有该字段。账号类型是
	// OAuth 时补齐标记，同时兼容修复前已落库的存量账号。
	if provider == "xai" && strings.EqualFold(authType, "oauth") {
		if strings.TrimSpace(metadataToString(meta["auth_kind"])) == "" {
			meta["auth_kind"] = "oauth"
		}
	}

	id := fmt.Sprintf("airgate-account-%d", in.AccountID)
	label := strings.TrimSpace(in.Name)
	if label == "" {
		label = id
	}

	now := time.Now().UTC()
	auth := &coreauth.Auth{
		ID:         id,
		Provider:   provider,
		Label:      label,
		Status:     coreauth.StatusActive,
		Disabled:   false,
		ProxyURL:   strings.TrimSpace(in.ProxyURL),
		Attributes: attrs,
		Metadata:   meta,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// openai-compatibility 需要 compat_name 属性。
	if provider == "openai-compatibility" {
		if auth.Attributes == nil {
			auth.Attributes = map[string]string{}
		}
		if auth.Attributes["compat_name"] == "" {
			auth.Attributes["compat_name"] = "openai-compatibility"
		}
	}

	return auth, nil
}

// CredentialsFromAuth 从 CPA Auth 回写 airgate 凭证 map（refresh 后落库用）。
// 仅提取常见 token 字段；未知 Metadata 字符串键一并带回。
func CredentialsFromAuth(auth *coreauth.Auth) map[string]string {
	if auth == nil {
		return nil
	}
	out := make(map[string]string, 16)
	if auth.Attributes != nil {
		for _, k := range []string{"api_key", "base_url"} {
			if v := strings.TrimSpace(auth.Attributes[k]); v != "" {
				out[k] = v
			}
		}
	}
	if auth.Metadata != nil {
		for k, raw := range auth.Metadata {
			if s := metadataToString(raw); s != "" {
				out[k] = s
			}
		}
	}
	return out
}

func metadataToString(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		// JSON 数字默认 float64；整数值去小数。
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}
