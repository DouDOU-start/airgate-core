package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// neutralUpstream 中立上游：不校验路径与请求体，只计数。
func neutralUpstream(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
}

// fakeModeration 命中固定关键词即拦截；记录收到的 CheckRequest。
type fakeModeration struct {
	keyword string
	calls   atomic.Int32
	last    atomic.Value
}

func (f *fakeModeration) Check(_ context.Context, in moderation.CheckRequest) moderation.Decision {
	f.calls.Add(1)
	f.last.Store(in)
	if f.keyword != "" && strings.Contains(string(in.Body), f.keyword) {
		return moderation.Decision{
			Blocked: true, Flagged: true,
			StatusCode: http.StatusForbidden,
			Message:    "内容审计命中风险规则",
			Action:     moderation.ActionKeywordBlock,
		}
	}
	return moderation.Decision{Allowed: true, Action: moderation.ActionAllow}
}

func TestModerationProtocolFor(t *testing.T) {
	tests := []struct {
		endpoint string
		protocol string
		ok       bool
	}{
		{adaptor.EndpointChatCompletions, moderation.ProtocolOpenAIChat, true},
		{adaptor.EndpointResponses, moderation.ProtocolOpenAIResponses, true},
		{adaptor.EndpointImagesGenerations, moderation.ProtocolOpenAIImages, true},
		{adaptor.EndpointMessages, moderation.ProtocolAnthropicMessages, true},
		{adaptor.EndpointGenerateContent, moderation.ProtocolGemini, true},
		{adaptor.EndpointPredict, moderation.ProtocolGemini, true},
		{adaptor.EndpointMessagesCountTokens, "", false},
		{adaptor.EndpointCountTokens, "", false},
		{adaptor.EndpointAlphaSearch, "", false},
		{adaptor.EndpointImagesEdits, "", false},
	}
	for _, tt := range tests {
		protocol, ok := moderationProtocolFor(tt.endpoint)
		if protocol != tt.protocol || ok != tt.ok {
			t.Fatalf("%s → (%q,%v), want (%q,%v)", tt.endpoint, protocol, ok, tt.protocol, tt.ok)
		}
	}
}

// pre_block 拦截：三种入口协议各自返回原生错误形态、上游零调用、失败留痕带审核 phase。
func TestModerationBlockPerProtocol(t *testing.T) {
	var hits atomic.Int32
	upstream := neutralUpstream(&hits)
	defer upstream.Close()

	tests := []struct {
		name     string
		path     string
		body     string
		snapType string
		model    string
		verify   func(t *testing.T, body map[string]any)
	}{
		{
			name:  "openai 形态",
			path:  "/v1/chat/completions",
			body:  `{"model":"gpt-4o","messages":[{"role":"user","content":"含敏感词的输入"}]}`,
			model: testModel,
			verify: func(t *testing.T, body map[string]any) {
				errObj, _ := body["error"].(map[string]any)
				if errObj["code"] != "content_keyword_blocked" || errObj["type"] != "permission_error" {
					t.Fatalf("openai 错误体 = %v", body)
				}
			},
		},
		{
			name:     "anthropic 形态",
			path:     "/v1/messages",
			body:     `{"model":"claude-s","messages":[{"role":"user","content":"含敏感词的输入"}]}`,
			snapType: "anthropic",
			model:    anthModel,
			verify: func(t *testing.T, body map[string]any) {
				if body["type"] != "error" {
					t.Fatalf("anthropic 错误体 = %v", body)
				}
				errObj, _ := body["error"].(map[string]any)
				if errObj["type"] != "permission_error" {
					t.Fatalf("anthropic error.type = %v", errObj)
				}
			},
		},
		{
			name:     "gemini 形态",
			path:     "/v1beta/models/gem-flash:generateContent",
			body:     `{"contents":[{"role":"user","parts":[{"text":"含敏感词的输入"}]}]}`,
			snapType: "gemini",
			model:    gemModel,
			verify: func(t *testing.T, body map[string]any) {
				errObj, _ := body["error"].(map[string]any)
				if errObj == nil || errObj["code"] != float64(http.StatusForbidden) {
					t.Fatalf("gemini 错误体 = %v", body)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, testSnap(1, upstream.URL, func(s *registry.ChannelKeySnapshot) {
				if tt.snapType != "" {
					s.Type = tt.snapType
				}
				s.Models[tt.model] = struct{}{}
			}))
			checker := &fakeModeration{keyword: "敏感词"}
			env.pipe.moderation = checker

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			env.engine.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应体非 JSON: %v", err)
			}
			tt.verify(t, body)
			if hits.Load() != 0 {
				t.Fatal("被拦截请求不应触网")
			}
			entry := env.errSink.lastEntry(t)
			if entry.Phase != errlog.PhasePrecheckModeration || entry.ErrorCode != "content_keyword_blocked" {
				t.Fatalf("errlog entry = %+v", entry)
			}
		})
	}
}

// 放行路径：审核通过后正常转发，CheckRequest 携带完整鉴权归属。
func TestModerationAllowPassesThrough(t *testing.T) {
	var hits atomic.Int32
	var lastBody atomic.Value
	upstream := newGoodUpstream(t, &hits, &lastBody)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL))
	checker := &fakeModeration{keyword: "敏感词"}
	env.pipe.moderation = checker

	w := env.do(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"正常提问"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Fatalf("上游调用次数 = %d", hits.Load())
	}
	in := checker.last.Load().(moderation.CheckRequest)
	ki := testKeyInfo()
	if in.UserID != ki.UserID || in.APIKeyID != ki.KeyID || in.GroupID != ki.GroupID {
		t.Fatalf("CheckRequest 归属不完整: %+v", in)
	}
	if in.Protocol != moderation.ProtocolOpenAIChat || in.Model != testModel {
		t.Fatalf("CheckRequest = %+v", in)
	}
}

// countTokens 类零计费端点不审核。
func TestModerationSkipsCountTokens(t *testing.T) {
	var hits atomic.Int32
	upstream := neutralUpstream(&hits)
	defer upstream.Close()

	env := newTestEnv(t, testSnap(1, upstream.URL, func(s *registry.ChannelKeySnapshot) {
		s.Type = "anthropic"
		s.Models[anthModel] = struct{}{}
	}))
	checker := &fakeModeration{keyword: "敏感词"}
	env.pipe.moderation = checker

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"claude-s","messages":[{"role":"user","content":"含敏感词的输入"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)

	if checker.calls.Load() != 0 {
		t.Fatal("countTokens 不应触发审核")
	}
	_ = w
}
