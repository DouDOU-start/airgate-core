package account

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
)

type accountTestUsageSink struct {
	records []billing.UsageRecord
}

func (s *accountTestUsageSink) Record(record billing.UsageRecord) {
	s.records = append(s.records, record)
}

type accountTestPriceLookup map[string]pricing.Price

func (p accountTestPriceLookup) Get(model string) (pricing.Price, bool) {
	price, ok := p[model]
	return price, ok
}

func TestResolveXAITestTarget(t *testing.T) {
	tests := []struct {
		name     string
		item     Account
		wantURL  string
		wantAuth string
		wantCLI  bool
		wantErr  bool
	}{
		{
			name: "OAuth 默认使用 CLI chat-proxy",
			item: Account{Type: TypeOAuth, Credentials: map[string]string{
				"access_token": "oauth-token",
			}},
			wantURL:  testXAICLIBaseURL + "/responses",
			wantAuth: "oauth-token",
			wantCLI:  true,
		},
		{
			name: "OAuth 可显式使用官方 API",
			item: Account{Type: TypeOAuth, Credentials: map[string]string{
				"access_token": "oauth-token",
				"using_api":    "true",
			}},
			wantURL:  testXAIOfficialBaseURL + "/responses",
			wantAuth: "oauth-token",
		},
		{
			name: "API Key 默认使用官方 API",
			item: Account{Type: TypeAPIKey, Credentials: map[string]string{
				"api_key": "api-key",
			}},
			wantURL:  testXAIOfficialBaseURL + "/responses",
			wantAuth: "api-key",
		},
		{
			name: "缺少凭证时报错",
			item: Account{Type: TypeOAuth, Credentials: map[string]string{
				"email": "user@example.com",
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotAuth, gotCLI, err := resolveXAITestTarget(tt.item)
			if tt.wantErr {
				if err == nil {
					t.Fatal("期望返回错误")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveXAITestTarget() 错误: %v", err)
			}
			if gotURL != tt.wantURL || gotAuth != tt.wantAuth || gotCLI != tt.wantCLI {
				t.Fatalf("结果 = (%q, %q, %v)，期望 (%q, %q, %v)", gotURL, gotAuth, gotCLI, tt.wantURL, tt.wantAuth, tt.wantCLI)
			}
		})
	}
}

func TestApplyXAITestHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, testXAICLIBaseURL+"/responses", nil)
	applyXAITestHeaders(req, "oauth-token", true)

	if got := req.Header.Get("Authorization"); got != "Bearer oauth-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
		t.Fatalf("X-XAI-Token-Auth = %q", got)
	}
	if got := req.Header.Get("x-grok-client-version"); got != testXAIClientVersion {
		t.Fatalf("x-grok-client-version = %q", got)
	}
	if got := req.Header.Get("User-Agent"); got != "xai-grok-workspace/"+testXAIClientVersion {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestXAITestModelKindsAndDefaultPrompts(t *testing.T) {
	tests := []struct {
		model         string
		kind          string
		defaultPrompt string
	}{
		{model: "grok-4.3", kind: testModelKindText, defaultPrompt: "hi"},
		{model: "grok-imagine-image", kind: testModelKindImage, defaultPrompt: "雨后的未来城市街角，一辆复古跑车停在霓虹灯下，湿润路面映出蓝金色灯光，电影级构图，细节丰富，写实摄影风格"},
		{model: "xai/grok-imagine-image-quality", kind: testModelKindImage, defaultPrompt: "雨后的未来城市街角，一辆复古跑车停在霓虹灯下，湿润路面映出蓝金色灯光，电影级构图，细节丰富，写实摄影风格"},
		{model: "grok-imagine-video", kind: testModelKindVideo, defaultPrompt: "黄昏时分，一辆复古跑车沿海岸公路缓缓驶过，镜头以低角度平稳跟拍，海风吹动路旁棕榈树，金色阳光洒在车身上，电影感，真实自然"},
		{model: "grok-imagine-video-1.5", kind: testModelKindVideo, defaultPrompt: "黄昏时分，一辆复古跑车沿海岸公路缓缓驶过，镜头以低角度平稳跟拍，海风吹动路旁棕榈树，金色阳光洒在车身上，电影感，真实自然"},
		{model: "grok-imagine-video-1.5-preview", kind: testModelKindVideo, defaultPrompt: "黄昏时分，一辆复古跑车沿海岸公路缓缓驶过，镜头以低角度平稳跟拍，海风吹动路旁棕榈树，金色阳光洒在车身上，电影感，真实自然"},
	}
	for _, tt := range tests {
		got := buildAccountTestModel("xai", "", tt.model, "")
		if got.Kind != tt.kind {
			t.Errorf("模型 %s 类型 = %q，期望 %q", tt.model, got.Kind, tt.kind)
		}
		if got.DefaultPrompt != tt.defaultPrompt {
			t.Errorf("模型 %s 默认文案 = %q，期望 %q", tt.model, got.DefaultPrompt, tt.defaultPrompt)
		}
	}
}

func TestXAIConnectionUsesResponsesStream(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("请求路径 = %q，期望 /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-XAI-Token-Auth"); got != "" {
			t.Errorf("自定义网关不应携带 CLI 身份头，实际为 %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"正常\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n")
	}))
	defer server.Close()

	item := Account{
		ID:       1,
		Platform: "xai",
		Type:     TypeOAuth,
		Credentials: map[string]string{
			"access_token": "oauth-token",
			"base_url":     server.URL,
		},
	}
	var events []TestEvent
	model, usage, err := (&Service{}).testXAI(context.Background(), item, "grok-4.3", "", TestMediaOptions{}, "", func(event TestEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("testXAI() 错误: %v", err)
	}
	if model != "grok-4.3" {
		t.Fatalf("model = %q", model)
	}
	if usage.InputTokens != 2 || usage.OutputTokens != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	if !strings.Contains(gotBody, `"model":"grok-4.3"`) || !strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("请求体不符合预期: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"text":"hi"`) {
		t.Fatalf("请求体缺少 hi 探测消息: %s", gotBody)
	}
	if len(events) != 3 || events[0].Type != "test_start" || events[1].Text != "正常" || events[2].Type != "test_complete" {
		t.Fatalf("事件序列不符合预期: %+v", events)
	}
}

func TestXAIConnectionUsesImageEndpointAndCustomPrompt(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("请求路径 = %q，期望 /images/generations", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("X-XAI-Token-Auth"); got != "" {
			t.Errorf("媒体请求不应携带 CLI 身份头，实际为 %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":123,"data":[{"url":"https://example.com/generated.png"}]}`)
	}))
	defer server.Close()

	item := Account{Platform: "xai", Type: TypeOAuth, Credentials: map[string]string{
		"access_token": "oauth-token",
		"base_url":     server.URL,
	}}
	var events []TestEvent
	model, usage, err := (&Service{}).testXAI(context.Background(), item, "grok-imagine-image", "自定义生图文案", TestMediaOptions{}, "", func(event TestEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("testXAI() 生图错误: %v", err)
	}
	if model != "grok-imagine-image" || !strings.Contains(gotBody, `"prompt":"自定义生图文案"`) {
		t.Fatalf("生图请求不符合预期，model=%q body=%s", model, gotBody)
	}
	if usage.Calls != 1 || usage.ImageSize != "1k" {
		t.Fatalf("生图计费用量 = %+v，期望 1 张、1k", usage)
	}
	if len(events) != 4 || !strings.Contains(events[1].Text, "generated.png") || events[2].Type != "media" || events[2].MediaKind != testModelKindImage || events[2].URL != "https://example.com/generated.png" || events[3].Type != "test_complete" {
		t.Fatalf("生图事件序列不符合预期: %+v", events)
	}
}

func TestXAIConnectionUsesVideoEndpointAndCustomPrompt(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/videos/generations":
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"request_id":"vid_123"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/videos/vid_123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"done","video":{"url":"https://example.com/generated.mp4"}}`)
		default:
			t.Errorf("非预期请求：%s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	item := Account{Platform: "xai", Type: TypeOAuth, Credentials: map[string]string{
		"access_token": "oauth-token",
		"base_url":     server.URL,
	}}
	var events []TestEvent
	model, usage, err := (&Service{}).testXAI(context.Background(), item, "grok-imagine-video", "自定义视频文案", TestMediaOptions{
		Duration:    5,
		AspectRatio: "9:16",
		Resolution:  "480p",
	}, "", func(event TestEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("testXAI() 视频错误: %v", err)
	}
	if model != "grok-imagine-video" ||
		!strings.Contains(gotBody, `"prompt":"自定义视频文案"`) ||
		!strings.Contains(gotBody, `"duration":5`) ||
		!strings.Contains(gotBody, `"aspect_ratio":"9:16"`) ||
		!strings.Contains(gotBody, `"resolution":"480p"`) {
		t.Fatalf("视频请求不符合预期，model=%q body=%s", model, gotBody)
	}
	if usage.VideoSeconds != 5 || usage.VideoResolution != "480p" {
		t.Fatalf("视频计费用量 = %+v，期望 5 秒、480p", usage)
	}
	var mediaEvent *TestEvent
	for i := range events {
		if events[i].Type == "media" {
			mediaEvent = &events[i]
			break
		}
	}
	if len(events) < 5 || !strings.Contains(events[1].Text, "vid_123") || mediaEvent == nil || mediaEvent.MediaKind != testModelKindVideo || mediaEvent.URL != "https://example.com/generated.mp4" || events[len(events)-1].Type != "test_complete" {
		t.Fatalf("视频事件序列不符合预期: %+v", events)
	}
}

func TestRecordAccountTestMediaUsage(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		price         pricing.Price
		usage         testStreamUsage
		wantCalls     int
		wantUnitPrice float64
		wantTotal     float64
		wantImageSize string
		wantVideoRes  string
	}{
		{
			name:  "生图按实际张数和分辨率计费",
			model: "grok-imagine-image-quality",
			price: pricing.Price{
				PerRequest:      0.05,
				ImageSizePrices: map[string]float64{"1k": 0.05, "2k": 0.07},
			},
			usage:         testStreamUsage{Calls: 2, ImageSize: "2k"},
			wantCalls:     2,
			wantUnitPrice: 0.07,
			wantTotal:     0.14,
			wantImageSize: "2k",
		},
		{
			name:  "视频按时长和分辨率计费",
			model: "grok-imagine-video-1.5",
			price: pricing.Price{
				VideoPerSecond:        0.14,
				VideoResolutionPrices: map[string]float64{"480p": 0.08, "720p": 0.14, "1080p": 0.25},
			},
			usage:         testStreamUsage{VideoSeconds: 8, VideoResolution: "1080p"},
			wantCalls:     8,
			wantUnitPrice: 0.25,
			wantTotal:     2,
			wantVideoRes:  "1080p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &accountTestUsageSink{}
			service := &Service{
				usageSink:   sink,
				priceLookup: accountTestPriceLookup{tt.model: tt.price},
				calculator:  billing.NewCalculator(),
			}
			service.recordAccountTestUsage(Account{ID: 7, RateMultiplier: 0.8}, tt.model, "/v1/test", tt.usage, 123)

			if len(sink.records) != 1 {
				t.Fatalf("使用记录条数 = %d，期望 1", len(sink.records))
			}
			got := sink.records[0]
			if got.Calls != tt.wantCalls || got.InputPrice != tt.wantUnitPrice || got.InputCost != tt.wantTotal || got.TotalCost != tt.wantTotal {
				t.Fatalf("媒体计费记录 = %+v，期望 calls=%d unit=%v total=%v", got, tt.wantCalls, tt.wantUnitPrice, tt.wantTotal)
			}
			if got.ImageSize != tt.wantImageSize || got.VideoResolution != tt.wantVideoRes {
				t.Fatalf("媒体档位 = image:%q video:%q，期望 image:%q video:%q", got.ImageSize, got.VideoResolution, tt.wantImageSize, tt.wantVideoRes)
			}
			if got.ActualCost != 0 || got.BilledCost != 0 {
				t.Fatalf("连通性测试不应扣用户费用，actual=%v billed=%v", got.ActualCost, got.BilledCost)
			}
		})
	}
}

func TestResolveXAIVideoTestOptions(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		input   TestMediaOptions
		want    TestMediaOptions
		wantErr bool
	}{
		{
			name:  "空参数使用默认值",
			model: "grok-imagine-video",
			want:  TestMediaOptions{Duration: 1, AspectRatio: "16:9", Resolution: "720p"},
		},
		{
			name:  "自定义参数",
			model: "grok-imagine-video",
			input: TestMediaOptions{Duration: 12, AspectRatio: "3:4", Resolution: "480P"},
			want:  TestMediaOptions{Duration: 12, AspectRatio: "3:4", Resolution: "480p"},
		},
		{
			name:  "1.5 模型支持 1080p",
			model: "grok-imagine-video-1.5",
			input: TestMediaOptions{Duration: 15, AspectRatio: "16:9", Resolution: "1080p"},
			want:  TestMediaOptions{Duration: 15, AspectRatio: "16:9", Resolution: "1080p"},
		},
		{
			name:    "普通模型拒绝 1080p",
			model:   "grok-imagine-video",
			input:   TestMediaOptions{Duration: 1, AspectRatio: "16:9", Resolution: "1080p"},
			wantErr: true,
		},
		{
			name:    "拒绝超长视频",
			model:   "grok-imagine-video-1.5",
			input:   TestMediaOptions{Duration: 16, AspectRatio: "16:9", Resolution: "720p"},
			wantErr: true,
		},
		{
			name:    "拒绝无效画幅",
			model:   "grok-imagine-video-1.5",
			input:   TestMediaOptions{Duration: 5, AspectRatio: "21:9", Resolution: "720p"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveXAIVideoTestOptions(tt.model, tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望返回错误，实际结果为 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveXAIVideoTestOptions() 错误: %v", err)
			}
			if got != tt.want {
				t.Fatalf("结果 = %+v，期望 %+v", got, tt.want)
			}
		})
	}
}

func TestRefreshXAIAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != testXAIClientID || r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("刷新表单不符合预期: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer server.Close()

	item := Account{Credentials: map[string]string{
		"refresh_token":  "old-refresh",
		"token_endpoint": server.URL,
	}}
	if err := (&Service{}).refreshXAIAccessToken(context.Background(), &item, ""); err != nil {
		t.Fatalf("refreshXAIAccessToken() 错误: %v", err)
	}
	if item.Credentials["access_token"] != "new-access" || item.Credentials["refresh_token"] != "new-refresh" {
		t.Fatalf("刷新后凭证不符合预期: %+v", item.Credentials)
	}
	if item.Credentials["expired"] == "" || item.Credentials["auth_kind"] != TypeOAuth {
		t.Fatalf("刷新后元数据不完整: %+v", item.Credentials)
	}
}
