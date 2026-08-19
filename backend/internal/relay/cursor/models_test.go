package cursor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyThinkingLevel(t *testing.T) {
	// 依赖内置目录：claude-fable-5 有 low/medium/high/xhigh 档，
	// claude-4.6-opus 只有 high（与 -max），gemini-3-flash 无档位变体。
	cases := []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{"未指定档位原样返回", "claude-fable-5-high", "", "claude-fable-5-high"},
		{"基础id加档位", "claude-fable-5", "high", "claude-fable-5-high"},
		{"改写已带档位", "claude-fable-5-high", "low", "claude-fable-5-low"},
		{"精确档位缺失就近取档", "claude-4.6-opus", "medium", "claude-4.6-opus-high"},
		{"距离相同偏向低档", "claude-opus-5", "xhigh", "claude-opus-5-high"},
		{"目录外模型原样返回", "no-such-model", "high", "no-such-model"},
		{"无档位变体回落基础id", "gemini-3-flash", "high", "gemini-3-flash"},
		{"未知档位原样返回", "claude-fable-5", "banana", "claude-fable-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyThinkingLevel(tc.model, tc.effort); got != tc.want {
				t.Fatalf("ApplyThinkingLevel(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}

func TestResolveWireModel(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{"目录内id无档位原样透传", "claude-fable-5-high", "", "claude-fable-5-high"},
		{"裸基础别名落medium", "claude-fable-5", "", "claude-fable-5-medium"},
		{"裸基础别名无medium就近", "claude-4.6-opus", "", "claude-4.6-opus-high"},
		{"显式档位优先", "claude-fable-5", "high", "claude-fable-5-high"},
		{"目录外模型原样透传", "no-such-model", "", "no-such-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveWireModel(tc.model, tc.effort); got != tc.want {
				t.Fatalf("ResolveWireModel(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}

func TestBaseAliases含常用基础名(t *testing.T) {
	aliases := BaseAliases()
	set := map[string]bool{}
	for _, a := range aliases {
		set[a] = true
	}
	if !set["claude-fable-5"] {
		t.Fatalf("BaseAliases 缺少 claude-fable-5，实际: %v", aliases[:min(10, len(aliases))])
	}
	// 别名不得与真实目录 id 重复
	for _, m := range Models() {
		if set[m.ID] {
			t.Fatalf("别名 %s 与真实目录 id 冲突", m.ID)
		}
	}
}

func TestParse思考档位归一(t *testing.T) {
	cases := []struct {
		name    string
		proto   Protocol
		payload string
		want    string
	}{
		{"OpenAI reasoning_effort", ProtoOpenAI,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"High"}`, "high"},
		{"OpenAI reasoning.effort 兼容", ProtoOpenAI,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"reasoning":{"effort":"low"}}`, "low"},
		{"OpenAI 未指定", ProtoOpenAI,
			`{"model":"m","messages":[{"role":"user","content":"hi"}]}`, ""},
		{"Anthropic budget 小", ProtoAnthropic,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":2048}}`, "low"},
		{"Anthropic budget 中", ProtoAnthropic,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":10000}}`, "medium"},
		{"Anthropic budget 大", ProtoAnthropic,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":60000}}`, "xhigh"},
		{"Anthropic disabled", ProtoAnthropic,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"}}`, ""},
		{"Anthropic output_config.effort（Claude Code beta）", ProtoAnthropic,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"}}`, "xhigh"},
		{"Anthropic adaptive 无 effort 走默认", ProtoAnthropic,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseRequest(tc.proto, []byte(tc.payload))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if parsed.ReasoningEffort != tc.want {
				t.Fatalf("ReasoningEffort = %q, want %q", parsed.ReasoningEffort, tc.want)
			}
		})
	}
}

func TestParseAnthropic会话中途system并入系统提示(t *testing.T) {
	payload := `{
		"model":"m",
		"system":"base prompt",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"system","content":[{"type":"text","text":"mid system"}]},
			{"role":"user","content":"again"}
		]
	}`
	parsed, err := ParseRequest(ProtoAnthropic, []byte(payload))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	joined := strings.Join(parsed.SystemPrompts, "\n")
	if !strings.Contains(joined, "base prompt") || !strings.Contains(joined, "mid system") {
		t.Fatalf("SystemPrompts = %v", parsed.SystemPrompts)
	}
	for _, m := range parsed.Messages {
		if m.Role == "system" {
			t.Fatalf("system 不应残留在对话消息中: %+v", parsed.Messages)
		}
	}
}

func TestWireToolName双向映射(t *testing.T) {
	defs, err := buildToolDefinitions([]NToolDef{{Name: "Read", Schema: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatalf("buildToolDefinitions: %v", err)
	}
	// 与 Cursor 内置工具重名会被服务端拒绝，上行必须带前缀。
	if defs[0].GetName() != "fn_Read" || defs[0].GetToolName() != "fn_Read" {
		t.Fatalf("wire 名 = %q / %q", defs[0].GetName(), defs[0].GetToolName())
	}
	if got := localToolName("fn_Read"); got != "Read" {
		t.Fatalf("localToolName = %q", got)
	}
	if got := localToolName("some_other"); got != "some_other" {
		t.Fatalf("无前缀名应原样返回, got %q", got)
	}
	if wireToolName("") != "" {
		t.Fatal("空名不应加前缀")
	}
}
