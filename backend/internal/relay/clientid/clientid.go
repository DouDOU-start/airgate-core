// Package clientid 从请求头识别客户端类型（Claude Code / Codex）。
// 识别结果经 gin context 传递，供转发管线分组限制检查使用。
package clientid

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// 客户端类型常量，与 Group.allowed_clients 值域一致。
const (
	ClaudeCode = "claude_code"
	Codex      = "codex"
)

const ctxKey = "x-client-type"

var claudeCodeUA = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+`)

// knownCodexRequestHeaders is the finite set of x-codex-* headers emitted by
// the official CLI/app-server request paths.  A random non-empty
// X-Codex-* header is not an identity signal: accepting the whole namespace
// would let any ordinary OpenAI caller opt into Codex-only instruction and
// routing behavior by spelling a made-up header.  Keep this list limited to
// headers that are used on outbound request metadata (including the Remote
// Control handshake headers).
var knownCodexRequestHeaders = map[string]struct{}{
	"x-codex-beta-features":                 {},
	"x-codex-host-device-kind":              {},
	"x-codex-imagegen-request-id":           {},
	"x-codex-image-turn-id":                 {},
	"x-codex-installation-id":               {},
	"x-codex-name":                          {},
	"x-codex-parent-thread-id":              {},
	"x-codex-protocol-version":              {},
	"x-codex-routing-hint":                  {},
	"x-codex-safety-buffering-enabled":      {},
	"x-codex-safety-buffering-faster-model": {},
	"x-codex-server-id":                     {},
	"x-codex-subscribe-cursor":              {},
	"x-codex-turn-metadata":                 {},
	"x-codex-turn-state":                    {},
	"x-codex-window-id":                     {},
}

// Detect 从请求头识别客户端类型并存入 gin context。
// 识别顺序：Claude Code → Codex → 空（普通客户端）。
func Detect(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	if clientType := ClassifyRequest(c.Request); clientType != "" {
		c.Set(ctxKey, clientType)
	}
}

// ClassifyRequest returns the header-derived client identity using the same
// precedence as Detect. Pipeline route classification can use this helper
// before Detect has populated the Gin context.
func ClassifyRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	if hasHeaderValue(req.Header.Values("User-Agent"), claudeCodeUA.MatchString) {
		return ClaudeCode
	}
	if IsCodexRequest(req) {
		return Codex
	}
	return ""
}

// Get 从 gin context 取客户端类型，空串表示普通客户端。
func Get(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, _ := c.Get(ctxKey)
	s, _ := v.(string)
	return s
}

// IsCodexRequest reports whether an HTTP request carries one of the official
// Codex client identity signals. Route and query classification belongs to
// the pipeline because shared aliases need additional context there.
func IsCodexRequest(req *http.Request) bool {
	if req == nil {
		return false
	}

	if hasHeaderValue(req.Header.Values("Originator"), isCodexOriginator) {
		return true
	}
	if hasHeaderValue(req.Header.Values("User-Agent"), isCodexUserAgent) {
		return true
	}
	if hasHeaderValue(req.Header.Values("X-OpenAI-Client-User-Agent"), isCodexClientUserAgent) {
		return true
	}
	for key, values := range req.Header {
		key = strings.ToLower(strings.TrimSpace(key))
		if _, known := knownCodexRequestHeaders[key]; !known {
			continue
		}
		if hasHeaderValue(values, func(value string) bool {
			return strings.TrimSpace(value) != ""
		}) {
			return true
		}
	}
	return false
}

// isCodexOriginator mirrors the finite first-party originators emitted by the
// official Codex clients. codex_exec is set by the official non-interactive
// CLI and therefore also identifies a Codex CLI request even though it is not
// part of the auth crate's first-party product classification helpers.
func isCodexOriginator(value string) bool {
	value = strings.TrimSpace(value)
	for _, originator := range []string{
		"codex_cli_rs",
		"codex-tui",
		"codex_vscode",
		"codex_atlas",
		"codex_chatgpt_desktop",
		// Work/ChatGPT surfaces use these thread originators when they
		// dispatch requests through the Codex app-server. They are emitted
		// by the official thread-manager originator mapping and therefore
		// must remain Codex identities even though they are not CLI names.
		"codex_work_desktop",
		"codex_work_web",
		"codex_work_mobile",
		"codex_work_cca",
		"chatgpt_cca",
		"codex_exec",
		// The official TypeScript SDK sets
		// CODEX_INTERNAL_ORIGINATOR_OVERRIDE=codex_sdk_ts for requests made
		// through its Codex client transport.
		"codex_sdk_ts",
	} {
		if strings.EqualFold(value, originator) {
			return true
		}
	}
	// The official client deliberately reserves this case-sensitive prefix for
	// additional first-party Codex products. Do not broaden it to arbitrary
	// strings that merely contain "codex".
	return strings.HasPrefix(value, "Codex ")
}

func hasHeaderValue(values []string, predicate func(string) bool) bool {
	for _, value := range values {
		if predicate(value) {
			return true
		}
	}
	return false
}

func isCodexUserAgent(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	// A few first-party transports return the originator without a version
	// suffix. Keep exact bare values explicit; in particular, do not treat a
	// product-looking prefix such as `codex-cli-wrapper` as first-party.
	for _, bare := range []string{
		"codex_cli_rs",
		"codex-tui",
		"codex_vscode",
		"codex_atlas",
		"codex_chatgpt_desktop",
		"codex_work_desktop",
		"codex_work_web",
		"codex_work_mobile",
		"codex_work_cca",
		"chatgpt_cca",
		"codex_exec",
		"codex-cli",
	} {
		if strings.EqualFold(value, bare) {
			return true
		}
	}
	if strings.HasPrefix(value, "Codex ") {
		return true
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"codex_cli_rs/",
		"codex-tui/",
		"codex_vscode/",
		"codex_atlas/",
		"codex_chatgpt_desktop/",
		"codex_work_desktop/",
		"codex_work_web/",
		"codex_work_mobile/",
		"codex_work_cca/",
		"chatgpt_cca/",
		"codex_exec/",
		"codex-cli/",
		"openai-codex-cli/",
		"openai-codex/",
	} {
		if hasUserAgentToken(lower, marker) {
			return true
		}
	}
	return hasUserAgentToken(lower, "codex/")
}

func isCodexClientUserAgent(value string) bool {
	// The client-UA header uses the same product/version token family as the
	// normal User-Agent. Reusing the strict parser prevents broad substring
	// matches (for example `openai-codex-cli-wrapper`) from opting a caller into
	// Codex behavior.
	return isCodexUserAgent(value)
}

// hasUserAgentToken matches a product marker only at a user-agent token
// boundary. A plain substring check would classify values such as
// "notcodex_cli_rs/1.0" or "openai-codex-cli-wrapper" as official Codex
// clients and could opt an ordinary caller into Codex-only behavior.
func hasUserAgentToken(value, marker string) bool {
	if value == "" || marker == "" {
		return false
	}
	for offset := 0; offset <= len(value)-len(marker); {
		index := strings.Index(value[offset:], marker)
		if index < 0 {
			return false
		}
		index += offset
		if index == 0 || !isUserAgentTokenChar(value[index-1]) {
			return true
		}
		offset = index + 1
	}
	return false
}

func isUserAgentTokenChar(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9') ||
		value == '-' || value == '_' || value == '.'
}

// Matches 检查当前客户端是否匹配 allowed 列表中的任一类型。
// allowed 为空时视为不限制，返回 true。
func Matches(clientType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == clientType {
			return true
		}
	}
	return false
}
