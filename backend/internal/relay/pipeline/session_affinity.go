package pipeline

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// 粘性会话（对齐 CLIProxyAPI SessionAffinitySelector 语义）：同一会话的
// 请求尽量粘住同一路由目标（账号/渠道 key），保住上游 prompt cache 命中，
// 降低多轮对话成本与首 token 延迟。
//
//   - 会话身份提取：显式 header / body 标识优先；均缺失时用「system 指令 +
//     第一条 user 输入」的哈希派生稳定身份（多轮追加消息不改变哈希）。
//   - 绑定优先于优先级：已绑定目标只要仍可调度就继续用（RouteCandidate
//     实时复核），高优先级候选恢复不会把会话抢走；目标失效/被排除时
//     自动重选并重绑。
//   - 绑定 key 含 userID：不同用户的同名会话标识互不串扰。
//   - Relay Hook plan 自带定向语义，粘性让位。

const (
	sessionAffinityTTL        = time.Hour
	maxSessionAffinityEntries = 8192
	// sessionTextPrefixBytes 派生哈希各文本段截取长度（对齐 CPA 取前缀思路）。
	sessionTextPrefixBytes = 200
)

type affinityEntry struct {
	kind      routeKind
	id        int
	expiresAt time.Time
}

// sessionAffinityCache 会话 → 路由目标绑定（TTL 刷新式，纯内存）。
type sessionAffinityCache struct {
	mu      sync.RWMutex
	entries map[string]affinityEntry
}

// lookup 命中时刷新 TTL 并返回绑定目标。
func (s *sessionAffinityCache) lookup(key string) (routeKind, int, bool) {
	now := time.Now()
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok || entry.expiresAt.Before(now) {
		return 0, 0, false
	}
	s.mu.Lock()
	if current, ok := s.entries[key]; ok && current == entry {
		current.expiresAt = now.Add(sessionAffinityTTL)
		s.entries[key] = current
	}
	s.mu.Unlock()
	return entry.kind, entry.id, true
}

// bind 写入/覆盖绑定；超容时先清过期条目，仍超容整表重置（极端兜底）。
func (s *sessionAffinityCache) bind(key string, kind routeKind, id int) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]affinityEntry)
	}
	if len(s.entries) >= maxSessionAffinityEntries {
		for k, e := range s.entries {
			if e.expiresAt.Before(now) {
				delete(s.entries, k)
			}
		}
		if len(s.entries) >= maxSessionAffinityEntries {
			s.entries = make(map[string]affinityEntry)
		}
	}
	s.entries[key] = affinityEntry{kind: kind, id: id, expiresAt: now.Add(sessionAffinityTTL)}
}

// sessionIDForRequest 提取本次请求的会话身份；无法确定时返回空串（不粘）。
func sessionIDForRequest(c *gin.Context, req *dto.ChatRequest) string {
	for _, h := range [...]struct{ name, prefix string }{
		{"X-Claude-Code-Session-Id", "claude:"},
		{"Session-Id", "codex:"},
		{"X-Session-Id", "header:"},
		{"X-Session-Affinity", "affinity:"},
	} {
		if v := strings.TrimSpace(c.GetHeader(h.name)); v != "" {
			return h.prefix + v
		}
	}
	if req == nil {
		return ""
	}
	if s := jsonStringField(req, "session_id"); s != "" {
		return "session:" + s
	}
	if s := jsonStringField(req, "sessionId"); s != "" {
		return "session:" + s
	}
	if s := jsonStringField(req, "prompt_cache_key"); s != "" {
		return "pck:" + s
	}
	// anthropic metadata.user_id（Claude Code 会把会话身份编码在这里）
	if raw, ok := req.Get("metadata"); ok {
		if v := gjson.GetBytes(raw, "user_id").String(); v != "" {
			return "meta:" + v
		}
	}
	return derivedSessionID(req)
}

// derivedSessionID 无显式标识时的派生会话身份：对「system 指令 + 第一条
// user 输入」前缀做 FNV-64a。多轮对话只追加消息，哈希保持稳定。
func derivedSessionID(req *dto.ChatRequest) string {
	h := fnv.New64a()
	wrote := false
	write := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(text) > sessionTextPrefixBytes {
			text = text[:sessionTextPrefixBytes]
		}
		_, _ = h.Write([]byte(text))
		_, _ = h.Write([]byte{0})
		wrote = true
	}

	// openai responses：instructions + input（string 或消息数组）
	if raw, ok := req.Get("instructions"); ok {
		write(gjson.ParseBytes(raw).String())
	}
	// anthropic：system（string 或 block 数组）
	if raw, ok := req.Get("system"); ok {
		write(textOfContent(gjson.ParseBytes(raw)))
	}
	// gemini：systemInstruction.parts[].text
	if raw, ok := req.Get("systemInstruction"); ok {
		write(textOfContent(gjson.GetBytes(raw, "parts")))
	}
	if raw, ok := req.Get("messages"); ok {
		write(firstUserText(gjson.ParseBytes(raw), "role", "content"))
	}
	if raw, ok := req.Get("input"); ok {
		parsed := gjson.ParseBytes(raw)
		if parsed.Type == gjson.String {
			write(parsed.String())
		} else {
			write(firstUserText(parsed, "role", "content"))
		}
	}
	if raw, ok := req.Get("contents"); ok {
		write(firstUserText(gjson.ParseBytes(raw), "role", "parts"))
	}
	if !wrote {
		return ""
	}
	return fmt.Sprintf("ctx:%016x", h.Sum64())
}

// firstUserText 从消息数组取第一条 user 消息的文本（role 缺省视为 user，
// 兼容 gemini contents 不带 role 的情形）。
func firstUserText(messages gjson.Result, roleKey, contentKey string) string {
	text := ""
	messages.ForEach(func(_, message gjson.Result) bool {
		role := message.Get(roleKey).String()
		if role != "" && role != "user" {
			return true
		}
		text = textOfContent(message.Get(contentKey))
		return text == ""
	})
	return text
}

// textOfContent 提取字符串或多模态 part 数组中的文本拼接。
func textOfContent(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return ""
	}
	var sb strings.Builder
	content.ForEach(func(_, part gjson.Result) bool {
		if part.Type == gjson.String {
			sb.WriteString(part.String())
		} else if t := part.Get("text"); t.Exists() {
			sb.WriteString(t.String())
		}
		return sb.Len() < sessionTextPrefixBytes
	})
	return sb.String()
}

func jsonStringField(req *dto.ChatRequest, key string) string {
	raw, ok := req.Get(key)
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// affinityKey 绑定键：userID 隔离不同用户，分组/模型/协议隔离不同路由域。
func affinityKey(userID, groupID int, model, protocol, sessionID string) string {
	return fmt.Sprintf("%d|%d|%s|%s|%s", userID, groupID, protocol, model, sessionID)
}

// resolveAffinityTarget 复核绑定目标当前是否可调度，可用则构造 routeTarget。
func (p *Pipeline) resolveAffinityTarget(
	kind routeKind, id, groupID int, model, protocol string,
	excludeKeys, excludeAccounts []int,
) (routeTarget, bool) {
	now := time.Now()
	switch kind {
	case routeAccount:
		if p.accounts == nil || routeExcluded(excludeAccounts, id) {
			return routeTarget{}, false
		}
		account, ok := p.accounts.RouteCandidate(id, groupID, model, now)
		if !ok {
			return routeTarget{}, false
		}
		return routeTarget{
			kind: routeAccount, priority: account.EffectivePriority(now),
			weight: effectiveRouteWeight(account.Weight), account: account,
		}, true
	case routeChannel:
		if p.registry == nil || routeExcluded(excludeKeys, id) {
			return routeTarget{}, false
		}
		channel, ok := p.registry.RouteCandidate(id, groupID, model, protocol, now)
		if !ok {
			return routeTarget{}, false
		}
		weight := effectiveRouteWeight(channel.Weight)
		if channel.HealthStatus == "degraded" {
			weight = max(weight/2, 1)
		}
		return routeTarget{
			kind: routeChannel, priority: channel.Priority,
			weight: weight, channel: channel,
		}, true
	}
	return routeTarget{}, false
}

func routeTargetID(target routeTarget) int {
	if target.kind == routeAccount && target.account != nil {
		return target.account.ID
	}
	if target.kind == routeChannel && target.channel != nil {
		return target.channel.KeyID
	}
	return 0
}
