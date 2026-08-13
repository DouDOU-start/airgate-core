package pipeline

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

func newSessionTestContext(t *testing.T, headers map[string]string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

func parseChatRequest(t *testing.T, body string) *dto.ChatRequest {
	t.Helper()
	req, err := dto.ParseChatRequest([]byte(body))
	if err != nil {
		t.Fatalf("解析请求体失败: %v", err)
	}
	return req
}

func TestSessionIDExtractionPriority(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		body    string
		want    string
	}{
		{
			name:    "claude code header 最优先",
			headers: map[string]string{"X-Claude-Code-Session-Id": "abc", "Session-Id": "def"},
			body:    `{"model":"m"}`,
			want:    "claude:abc",
		},
		{
			name: "body session_id",
			body: `{"model":"m","session_id":"s1"}`,
			want: "session:s1",
		},
		{
			name: "prompt_cache_key",
			body: `{"model":"m","prompt_cache_key":"pck-1"}`,
			want: "pck:pck-1",
		},
		{
			name: "anthropic metadata.user_id",
			body: `{"model":"m","metadata":{"user_id":"u_123"}}`,
			want: "meta:u_123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newSessionTestContext(t, tc.headers)
			got := sessionIDForRequest(c, parseChatRequest(t, tc.body))
			if got != tc.want {
				t.Fatalf("sessionID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDerivedSessionIDStableAcrossTurns(t *testing.T) {
	c := newSessionTestContext(t, nil)
	turn1 := parseChatRequest(t, `{"model":"m","messages":[
		{"role":"system","content":"你是助手"},
		{"role":"user","content":"第一问"}]}`)
	turn2 := parseChatRequest(t, `{"model":"m","messages":[
		{"role":"system","content":"你是助手"},
		{"role":"user","content":"第一问"},
		{"role":"assistant","content":"第一答"},
		{"role":"user","content":"第二问"}]}`)
	other := parseChatRequest(t, `{"model":"m","messages":[
		{"role":"system","content":"你是助手"},
		{"role":"user","content":"完全不同的会话"}]}`)

	id1 := sessionIDForRequest(c, turn1)
	id2 := sessionIDForRequest(c, turn2)
	id3 := sessionIDForRequest(c, other)
	if id1 == "" {
		t.Fatal("应派生出会话身份")
	}
	if id1 != id2 {
		t.Fatalf("多轮追加消息不应改变派生身份: %q vs %q", id1, id2)
	}
	if id1 == id3 {
		t.Fatal("不同首问应派生不同身份")
	}
}

func TestDerivedSessionIDAnthropicSystemBlocks(t *testing.T) {
	c := newSessionTestContext(t, nil)
	req := parseChatRequest(t, `{"model":"m","system":[{"type":"text","text":"sys"}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	if got := sessionIDForRequest(c, req); got == "" {
		t.Fatal("anthropic block 结构应能派生会话身份")
	}
}

func TestSessionAffinityCacheBindLookup(t *testing.T) {
	cache := &sessionAffinityCache{}
	if _, _, ok := cache.lookup("k"); ok {
		t.Fatal("空缓存不应命中")
	}
	cache.bind("k", routeAccount, 42)
	kind, id, ok := cache.lookup("k")
	if !ok || kind != routeAccount || id != 42 {
		t.Fatalf("lookup = (%v,%v,%v), want (routeAccount,42,true)", kind, id, ok)
	}
	// 覆盖重绑
	cache.bind("k", routeChannel, 7)
	kind, id, _ = cache.lookup("k")
	if kind != routeChannel || id != 7 {
		t.Fatalf("重绑后 lookup = (%v,%v)", kind, id)
	}
}

func TestSessionAffinityCacheExpiry(t *testing.T) {
	cache := &sessionAffinityCache{entries: map[string]affinityEntry{
		"stale": {kind: routeAccount, id: 1, expiresAt: time.Now().Add(-time.Minute)},
	}}
	if _, _, ok := cache.lookup("stale"); ok {
		t.Fatal("过期条目不应命中")
	}
}
