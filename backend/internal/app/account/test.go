package account

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/relay/accounttesthook"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// 账号连通性测试（对齐 sub2api AccountTestService）。
// 通过 emit 回推 SSE 事件；service 不依赖 gin。

const (
	testClaudeAPIURL         = "https://api.anthropic.com/v1/messages?beta=true"
	chatgptCodexAPIURL       = "https://chatgpt.com/backend-api/codex/responses"
	testXAIOfficialBaseURL   = "https://api.x.ai/v1"
	testXAICLIBaseURL        = "https://cli-chat-proxy.grok.com/v1"
	testXAITokenEndpoint     = "https://auth.x.ai/oauth2/token"
	testXAIClientID          = "b1a00492-073a-47ea-816f-4c329264a828"
	testXAIClientVersion     = "0.2.93"
	testMaxBody              = 1 << 20
	testMediaMaxBody         = 16 << 20
	testXAIVideoPollTimeout  = 10 * time.Minute
	testXAIVideoPollInterval = 2 * time.Second
	antigravityModelsPath    = "/v1internal:fetchAvailableModels"
	antigravityModelsDaily   = "https://daily-cloudcode-pa.googleapis.com"
	antigravityModelsProd    = "https://cloudcode-pa.googleapis.com"
	antigravityModelsTimeout = 15 * time.Second
	antigravityModelsUA      = "antigravity/hub/2.2.1 darwin/arm64"
)

var sseDataPrefix = regexp.MustCompile(`^data:\s*`)

// TestForwarder 是账号连通性测试所需的非流式 CPA 转发子集。
type TestForwarder interface {
	ForwardNonStream(context.Context, cpa.ForwardRequest) cpa.ForwardResult
}

// TestEvent 测试过程 SSE 事件。
type TestEvent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Model     string `json:"model,omitempty"`
	Success   bool   `json:"success,omitempty"`
	Error     string `json:"error,omitempty"`
	MediaKind string `json:"media_kind,omitempty"`
	URL       string `json:"url,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

// TestModel 可选测试模型。
type TestModel struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Kind          string `json:"kind"`
	DefaultPrompt string `json:"default_prompt"`
}

// TestMediaOptions 媒体模型连通性测试参数。
type TestMediaOptions struct {
	Duration    int
	AspectRatio string
	Resolution  string
}

// TestMode 指定账号连接测试的请求处理模式。
type TestMode string

const (
	// TestModeNormal 直接发送 Core 构造的普通探测请求。
	TestModeNormal TestMode = "normal"
	// TestModeOverage 先交给插件执行 Codex 超额请求变换，再发送给当前账号。
	TestModeOverage TestMode = "overage"
)

// TestOptions 汇总账号连接测试选项。
type TestOptions struct {
	Mode  TestMode
	Media TestMediaOptions
}

const (
	testModelKindText  = "text"
	testModelKindImage = "image"
	testModelKindVideo = "video"
)

var xaiMediaTestModels = []TestModel{
	{ID: "grok-imagine-image", DisplayName: "Grok Imagine Image", Kind: testModelKindImage},
	{ID: "grok-imagine-image-quality", DisplayName: "Grok Imagine Image Quality", Kind: testModelKindImage},
	{ID: "grok-imagine-video", DisplayName: "Grok Imagine Video", Kind: testModelKindVideo},
	{ID: "grok-imagine-video-1.5", DisplayName: "Grok Imagine Video 1.5", Kind: testModelKindVideo},
}

// AvailableTestModels 返回账号可测模型列表。
//
// 来源优先级：
//  1. 账号 extra.models（若配置了白名单，优先）
//  2. Antigravity OAuth 账号实时读取 fetchAvailableModels，过滤未知/内部模型
//     · CPA 标准 Gemini 3.7 Flash 保留规范兜底入口，避免上游目录漏报导致无法选择
//  3. 其他平台或实时查询失败时使用 cpa 嵌入的 models.json
//     · Codex 按 credentials.plan_type / extra.plan_type 分 free/plus/team/pro 档
func (s *Service) AvailableTestModels(ctx context.Context, id int) ([]TestModel, error) {
	item, err := s.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		return nil, err
	}
	plan := resolvePlanType(item)
	allowedIDs := modelsFromAccountExtra(item.Extra)

	if cpa.ResolveProvider(item.Platform) == "antigravity" {
		proxyURL := proxyURLFromRef(item.Proxy)
		if err := s.ensureOAuthCredentialsFresh(ctx, &item, proxyURL); err == nil {
			if liveModels, fetchErr := fetchAntigravityAvailableModels(ctx, item.Credentials, proxyURL); fetchErr == nil {
				return buildAntigravityTestModels(item.Platform, plan, liveModels, allowedIDs), nil
			}
		}
	}

	// 白名单：仅 ID 列表，展示名尽量从 CPA 目录补
	if len(allowedIDs) > 0 {
		if cpa.ResolveProvider(item.Platform) == "antigravity" {
			allowedIDs = canonicalAntigravityTestModelIDs(item.Platform, plan, allowedIDs)
		}
		out := make([]TestModel, 0, len(allowedIDs))
		for _, mid := range allowedIDs {
			mid = strings.TrimSpace(mid)
			if mid == "" {
				continue
			}
			out = append(out, buildAccountTestModel(item.Platform, plan, mid, cpa.LookupModelDisplayName(item.Platform, plan, mid)))
		}
		return out, nil
	}

	infos := cpa.DefaultModelInfos(item.Platform, plan)
	isXAI := cpa.ResolveProvider(item.Platform) == "xai"
	if len(infos) == 0 && !isXAI {
		return nil, nil
	}
	out := make([]TestModel, 0, len(infos)+len(xaiMediaTestModels))
	seen := make(map[string]struct{}, len(infos)+len(xaiMediaTestModels))
	for _, m := range infos {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		out = append(out, buildAccountTestModel(item.Platform, plan, m.ID, name))
		seen[m.ID] = struct{}{}
	}
	if isXAI {
		for _, media := range xaiMediaTestModels {
			if _, ok := seen[media.ID]; ok {
				continue
			}
			out = append(out, buildAccountTestModel(item.Platform, plan, media.ID, media.DisplayName))
		}
	}
	return out, nil
}

type antigravityAvailableModel struct {
	ID          string
	DisplayName string
}

type antigravityAvailableModelsResponse struct {
	Models map[string]struct {
		DisplayName string `json:"displayName"`
	} `json:"models"`
}

func isAntigravityInternalModel(modelID string) bool {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "chat_20706", "chat_23310", "tab_flash_lite_preview", "tab_jump_flash_lite_preview", "gemini-2.5-flash-thinking", "gemini-2.5-pro":
		return true
	default:
		return false
	}
}

func fetchAntigravityAvailableModels(ctx context.Context, credentials map[string]string, proxyURL string) ([]antigravityAvailableModel, error) {
	accessToken := strings.TrimSpace(credentials["access_token"])
	if accessToken == "" {
		return nil, fmt.Errorf("antigravity 凭证缺少 access_token")
	}

	baseURLs := []string{antigravityModelsDaily, antigravityModelsProd}
	if baseURL := strings.TrimRight(strings.TrimSpace(credentials["base_url"]), "/"); baseURL != "" {
		baseURLs = []string{baseURL}
	}
	payload := map[string]string{}
	if projectID := strings.TrimSpace(credentials["project_id"]); projectID != "" {
		payload["project"] = projectID
	}
	rawPayload, _ := json.Marshal(payload)

	fetchCtx, cancel := context.WithTimeout(ctx, antigravityModelsTimeout)
	defer cancel()
	client := httpClient(proxyURL)
	var lastErr error
	for _, baseURL := range baseURLs {
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+antigravityModelsPath, bytes.NewReader(rawPayload))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		userAgent := strings.TrimSpace(credentials["user_agent"])
		if userAgent == "" {
			userAgent = antigravityModelsUA
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("antigravity 模型目录 HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
			continue
		}

		var parsed antigravityAvailableModelsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = fmt.Errorf("解析 Antigravity 模型目录失败: %w", err)
			continue
		}
		models := make([]antigravityAvailableModel, 0, len(parsed.Models))
		for id, info := range parsed.Models {
			id = strings.TrimSpace(id)
			if id == "" || isAntigravityInternalModel(id) {
				continue
			}
			models = append(models, antigravityAvailableModel{ID: id, DisplayName: strings.TrimSpace(info.DisplayName)})
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		return models, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("antigravity 模型目录不可用")
	}
	return nil, lastErr
}

func canonicalAntigravityAvailableModels(platform, plan string, liveModels []antigravityAvailableModel, allowedIDs []string) []antigravityAvailableModel {
	live := make(map[string]antigravityAvailableModel, len(liveModels))
	for _, model := range liveModels {
		if !isAntigravityInternalModel(model.ID) {
			live[strings.ToLower(strings.TrimSpace(model.ID))] = model
		}
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		if id = strings.TrimSpace(id); id != "" {
			allowed[strings.ToLower(id)] = struct{}{}
		}
	}
	out := make([]antigravityAvailableModel, 0, len(live))
	for _, canonical := range cpa.DefaultModelInfos(platform, plan) {
		model, ok := live[strings.ToLower(strings.TrimSpace(canonical.ID))]
		if !ok {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(canonical.ID)]; !ok {
				continue
			}
		}
		model.ID = canonical.ID
		if strings.TrimSpace(canonical.DisplayName) != "" {
			model.DisplayName = canonical.DisplayName
		}
		out = append(out, model)
	}
	return out
}

func canonicalAntigravityModelID(platform, plan, modelID string) (string, bool) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || isAntigravityInternalModel(modelID) {
		return "", false
	}
	for _, canonical := range cpa.DefaultModelInfos(platform, plan) {
		if strings.EqualFold(canonical.ID, modelID) {
			return canonical.ID, true
		}
	}
	return "", false
}

func canonicalAntigravityTestModelIDs(platform, plan string, modelIDs []string) []string {
	seen := make(map[string]struct{}, len(modelIDs))
	out := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		canonical, ok := canonicalAntigravityModelID(platform, plan, modelID)
		if !ok {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out
}

func buildAntigravityTestModels(platform, plan string, liveModels []antigravityAvailableModel, allowedIDs []string) []TestModel {
	canonical := canonicalAntigravityAvailableModels(platform, plan, liveModels, allowedIDs)
	out := make([]TestModel, 0, len(canonical))
	seen := make(map[string]struct{}, len(canonical))
	for _, model := range canonical {
		out = append(out, buildAccountTestModel(platform, plan, model.ID, model.DisplayName))
		seen[strings.ToLower(model.ID)] = struct{}{}
	}

	// Antigravity 的实时目录偶尔漏报标准 3.7，但 CPA 仍以 high 作为唯一规范模型 ID。
	// 只补这一条规范入口，不把配额组名称或其他未知 ID 映射成模型。
	const gemini37FlashHigh = "gemini-3.7-flash-high"
	if _, ok := seen[gemini37FlashHigh]; !ok {
		allowed := len(allowedIDs) == 0
		for _, id := range allowedIDs {
			if strings.EqualFold(strings.TrimSpace(id), gemini37FlashHigh) {
				allowed = true
				break
			}
		}
		if allowed {
			if canonicalID, ok := canonicalAntigravityModelID(platform, plan, gemini37FlashHigh); ok {
				out = append(out, buildAccountTestModel(platform, plan, canonicalID, cpa.LookupModelDisplayName(platform, plan, canonicalID)))
			}
		}
	}
	return out
}

func antigravityModelAvailable(models []antigravityAvailableModel, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	for _, model := range models {
		if strings.EqualFold(model.ID, modelID) {
			return true
		}
	}
	return false
}

func pickAntigravityAvailableModel(models []antigravityAvailableModel) string {
	for _, model := range models {
		low := strings.ToLower(model.ID)
		if strings.Contains(low, "flash") && !strings.Contains(low, "thinking") {
			return model.ID
		}
	}
	if len(models) > 0 {
		return models[0].ID
	}
	return ""
}

func buildAccountTestModel(platform, plan, modelID, displayName string) TestModel {
	modelID = strings.TrimSpace(modelID)
	if displayName = strings.TrimSpace(displayName); displayName == "" {
		displayName = xaiMediaTestDisplayName(modelID)
	}
	if displayName == "" {
		displayName = cpa.LookupModelDisplayName(platform, plan, modelID)
	}
	if displayName == "" {
		displayName = modelID
	}
	kind := accountTestModelKind(modelID)
	return TestModel{
		ID:            modelID,
		DisplayName:   displayName,
		Kind:          kind,
		DefaultPrompt: defaultAccountTestPrompt(kind),
	}
}

func accountTestModelKind(modelID string) string {
	base := strings.ToLower(strings.TrimSpace(modelID))
	if slash := strings.LastIndex(base, "/"); slash >= 0 && slash < len(base)-1 {
		base = base[slash+1:]
	}
	switch base {
	case "grok-imagine-image", "grok-imagine-image-quality":
		return testModelKindImage
	case "grok-imagine-video", "grok-imagine-video-1.5", "grok-imagine-video-1.5-preview":
		return testModelKindVideo
	default:
		return testModelKindText
	}
}

func xaiMediaTestDisplayName(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if slash := strings.LastIndex(modelID, "/"); slash >= 0 && slash < len(modelID)-1 {
		modelID = modelID[slash+1:]
	}
	for _, model := range xaiMediaTestModels {
		if strings.EqualFold(model.ID, modelID) {
			return model.DisplayName
		}
	}
	if strings.EqualFold(modelID, "grok-imagine-video-1.5-preview") {
		return "Grok Imagine Video 1.5 Preview"
	}
	return ""
}

func defaultAccountTestPrompt(kind string) string {
	switch kind {
	case testModelKindImage:
		return "雨后的未来城市街角，一辆复古跑车停在霓虹灯下，湿润路面映出蓝金色灯光，电影级构图，细节丰富，写实摄影风格"
	case testModelKindVideo:
		return "黄昏时分，一辆复古跑车沿海岸公路缓缓驶过，镜头以低角度平稳跟拍，海风吹动路旁棕榈树，金色阳光洒在车身上，电影感，真实自然"
	default:
		return "hi"
	}
}

func resolveAccountTestPrompt(modelID, prompt string) string {
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		return prompt
	}
	return defaultAccountTestPrompt(accountTestModelKind(modelID))
}

// modelsFromAccountExtra 读取 extra.models（与 accountreg 选路白名单同字段）。
func modelsFromAccountExtra(extra map[string]any) []string {
	if extra == nil {
		return nil
	}
	raw, ok := extra["models"]
	if !ok || raw == nil {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		for _, m := range v {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// pickDefaultTestModel 未指定 model 时的默认探测模型（偏向轻量/常用）。
func pickDefaultTestModel(platform, planType, preferred string) string {
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		return preferred
	}
	ids := cpa.DefaultModelsWithPlan(platform, planType)
	if len(ids) == 0 {
		return ""
	}
	// 优先带 codex / sonnet / flash 的条目，否则取第一个
	p := strings.ToLower(cpa.ResolveProvider(platform))
	for _, id := range ids {
		low := strings.ToLower(id)
		switch p {
		case "codex":
			if strings.Contains(low, "codex") && !strings.Contains(low, "spark") {
				return id
			}
		case "claude":
			if strings.Contains(low, "sonnet") {
				return id
			}
		case "gemini", "aistudio", "antigravity", "vertex":
			if strings.Contains(low, "flash") && !strings.Contains(low, "thinking") {
				return id
			}
		}
	}
	return ids[0]
}

// testStreamUsage 连通性测试解析出的计费用量。
// 文本模型使用 token 字段；生图使用 Calls/ImageSize；视频使用 VideoSeconds/VideoResolution。
type testStreamUsage struct {
	InputTokens     int
	OutputTokens    int
	CachedTokens    int
	Calls           int
	ImageSize       string
	ImageQuality    string
	VideoSeconds    int
	VideoResolution string
}

// TestConnection 对账号发一条最小探测请求，经 emit 推送 SSE 事件。
// 成功时落 usage_log（source=account_test），对齐渠道测试不扣用户余额。
func (s *Service) TestConnection(ctx context.Context, id int, modelID, prompt string, options TestOptions, emit func(TestEvent)) error {
	if emit == nil {
		emit = func(TestEvent) {}
	}
	testMode, err := normalizeTestMode(options.Mode)
	if err != nil {
		emit(TestEvent{Type: "error", Error: err.Error()})
		return err
	}
	item, err := s.FindByID(ctx, id, LoadOptions{WithProxy: true})
	if err != nil {
		emit(TestEvent{Type: "error", Error: err.Error()})
		return err
	}
	proxyURL := proxyURLFromRef(item.Proxy)
	platform := strings.ToLower(strings.TrimSpace(item.Platform))
	if testMode == TestModeOverage && platform != "codex" {
		return emitErr(emit, "Codex超额测试仅支持 Codex 账号")
	}
	start := time.Now()

	var (
		model    string
		usage    testStreamUsage
		testErr  error
		endpoint string
	)
	switch platform {
	case "codex", "openai":
		model, usage, testErr = s.testCodex(ctx, item, modelID, prompt, testMode, proxyURL, emit)
		endpoint = "/backend-api/codex/responses"
	case "claude", "anthropic":
		model, usage, testErr = s.testClaude(ctx, item, modelID, prompt, proxyURL, emit)
		endpoint = "/v1/messages"
	case "xai", "grok":
		model, usage, testErr = s.testXAI(ctx, item, modelID, prompt, options.Media, proxyURL, emit)
		endpoint = xaiTestEndpoint(model)
	case "antigravity":
		model, usage, testErr = s.testAntigravity(ctx, item, modelID, prompt, proxyURL, emit)
		endpoint = "/v1beta/models/" + model + ":generateContent"
	default:
		msg := fmt.Sprintf("平台 %s 暂不支持连通性测试（当前支持 Codex / Claude / xAI / Antigravity）", item.Platform)
		emit(TestEvent{Type: "error", Error: msg})
		return fmt.Errorf("%s", msg)
	}
	if testErr != nil {
		return testErr
	}
	s.recordAccountTestUsage(item, model, endpoint, usage, time.Since(start).Milliseconds())
	return nil
}

type accountTestForwarder interface {
	ForwardAccountTest(context.Context, cpa.ForwardRequest) cpa.ForwardResult
}

// testAntigravity 通过 CPA Antigravity executor 发起一次非流式探测请求。
// 使用 OpenAI Chat Completions 作为统一输入格式，由 CPA 负责翻译为 Gemini 请求。
func (s *Service) testAntigravity(ctx context.Context, item Account, modelID, prompt, proxyURL string, emit func(TestEvent)) (string, testStreamUsage, error) {
	plan := resolvePlanType(item)
	model := strings.TrimSpace(modelID)
	if model != "" {
		canonical, ok := canonicalAntigravityModelID(item.Platform, plan, model)
		if !ok {
			return model, testStreamUsage{}, emitErr(emit, fmt.Sprintf("模型 %s 不在 Antigravity CPA 标准模型目录中", model))
		}
		model = canonical
	} else {
		model = pickDefaultTestModel(item.Platform, plan, "")
	}
	if err := s.ensureOAuthCredentialsFresh(ctx, &item, proxyURL); err != nil {
		return model, testStreamUsage{}, emitErr(emit, "access_token 刷新失败: "+err.Error())
	}
	if liveModels, err := fetchAntigravityAvailableModels(ctx, item.Credentials, proxyURL); err == nil {
		liveModels = canonicalAntigravityAvailableModels(item.Platform, plan, liveModels, modelsFromAccountExtra(item.Extra))
		if strings.TrimSpace(modelID) == "" {
			model = pickAntigravityAvailableModel(liveModels)
		}
		if model == "" {
			return "", testStreamUsage{}, emitErr(emit, "当前 Antigravity 账号未返回可用模型")
		}
		if !antigravityModelAvailable(liveModels, model) {
			return model, testStreamUsage{}, emitErr(emit, fmt.Sprintf("当前 Antigravity 账号未开放模型 %s，请从实时可用模型列表中重新选择", model))
		}
	}
	if model == "" {
		return "", testStreamUsage{}, emitErr(emit, "无可测模型")
	}
	forwarder, ok := s.oauthRefresher.(accountTestForwarder)
	if !ok || forwarder == nil {
		return model, testStreamUsage{}, emitErr(emit, "CPA Antigravity 连通性测试执行器不可用")
	}
	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role":    "user",
			"content": resolveAccountTestPrompt(model, prompt),
		}},
		"stream":     false,
		"max_tokens": 64,
	}
	raw, _ := json.Marshal(payload)
	emit(TestEvent{Type: "test_start", Model: model})
	result := forwarder.ForwardAccountTest(ctx, cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{
			AccountID:   item.ID,
			Name:        item.Name,
			Platform:    item.Platform,
			Type:        item.Type,
			Credentials: item.Credentials,
			ProxyURL:    proxyURL,
		},
		Model:         model,
		Endpoint:      adaptor.EndpointChatCompletions,
		EntryProtocol: registry.ProtocolOpenAI,
		Payload:       raw,
	})
	if message := accountTestForwardError(result); message != "" {
		return model, testStreamUsage{}, emitErr(emit, message)
	}
	if len(result.RefreshedCredentials) > 0 {
		if err := s.applyRefreshedCredentials(ctx, &item, result.RefreshedCredentials); err != nil {
			return model, testStreamUsage{}, emitErr(emit, "刷新凭证落库失败: "+err.Error())
		}
	}
	usage := testStreamUsage{}
	if result.Usage != nil {
		usage.InputTokens = result.Usage.PromptTokens
		usage.OutputTokens = result.Usage.CompletionTokens
		usage.CachedTokens = result.Usage.CachedTokens
	}
	var body map[string]any
	if err := json.Unmarshal(result.Body, &body); err != nil {
		return model, usage, emitErr(emit, "解析 Antigravity 测试响应失败: "+err.Error())
	}
	mergeUsageMap(&usage, body["usage"])
	if text := extractAccountTestResponseText(body); text != "" {
		emit(TestEvent{Type: "content", Text: text})
	}
	emit(TestEvent{Type: "test_complete", Success: true})
	return model, usage, nil
}

func accountTestForwardError(result cpa.ForwardResult) string {
	if result.BuildErr != nil {
		return "构造 Antigravity 测试请求失败: " + result.BuildErr.Error()
	}
	if result.NetErr != nil {
		return "请求 Antigravity 失败: " + result.NetErr.Error()
	}
	if result.StreamErr != nil {
		return "Antigravity 测试响应失败: " + result.StreamErr.Error()
	}
	if result.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(string(result.Body)), "requested entity was not found") {
		return "Antigravity 上游未找到模型或项目（HTTP 404）：当前账号未开放所选模型，或 project_id 已失效；请重新授权后从实时模型列表重新选择"
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return formatUpstreamHTTPError(result.StatusCode, result.Body)
	}
	return ""
}

func extractAccountTestResponseText(body map[string]any) string {
	if body == nil {
		return ""
	}
	if choices, ok := body["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if text := extractAccountTestTextValue(choice["message"]); text != "" {
				return text
			}
			if text := extractAccountTestTextValue(choice["text"]); text != "" {
				return text
			}
		}
	}
	if candidates, ok := body["candidates"].([]any); ok && len(candidates) > 0 {
		if candidate, ok := candidates[0].(map[string]any); ok {
			if text := extractAccountTestTextValue(candidate["content"]); text != "" {
				return text
			}
		}
	}
	if output, ok := body["output"].([]any); ok {
		for _, part := range output {
			if text := extractAccountTestTextValue(part); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractAccountTestTextValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		if text, ok := value["content"].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if text, ok := value["text"].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if parts, ok := value["parts"].([]any); ok {
			for _, part := range parts {
				if text := extractAccountTestTextValue(part); text != "" {
					return text
				}
			}
		}
	case []any:
		for _, part := range value {
			if text := extractAccountTestTextValue(part); text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeTestMode(mode TestMode) (TestMode, error) {
	switch TestMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case "", TestModeNormal:
		return TestModeNormal, nil
	case TestModeOverage:
		return TestModeOverage, nil
	default:
		return "", fmt.Errorf("不支持的账号测试模式 %q", mode)
	}
}

// recordAccountTestUsage 账号测试成功落消费记录：无用户/Key 归属，不扣任何人余额；
// total 按文本 token、生图张数或视频秒数实算，账号成本口径 total×rate_multiplier 成立。
func (s *Service) recordAccountTestUsage(item Account, model, endpoint string, usage testStreamUsage, durationMs int64) {
	if s == nil || s.usageSink == nil || model == "" {
		return
	}
	var price pricing.Price
	if s.priceLookup != nil {
		if p, ok := s.priceLookup.Get(model); ok {
			price = p
		}
	}
	costs := pricing.ComputeCosts(price, pricing.Usage{
		PromptTokens:     usage.InputTokens + usage.CachedTokens,
		CompletionTokens: usage.OutputTokens,
		CachedTokens:     usage.CachedTokens,
		Calls:            usage.Calls,
		ImageSize:        usage.ImageSize,
		ImageQuality:     usage.ImageQuality,
	}, "")
	inputPrice := price.Input
	billedCalls := usage.Calls
	if usage.VideoSeconds > 0 {
		if perSecond, ok := pricing.VideoPriceFor(price, usage.VideoResolution); ok {
			costs = pricing.Costs{Input: perSecond * float64(usage.VideoSeconds)}
			inputPrice = perSecond
			billedCalls = usage.VideoSeconds
		}
	} else if perImage, ok := pricing.ImagePriceFor(price, usage.ImageQuality, usage.ImageSize); ok {
		inputPrice = perImage
		if billedCalls < 1 {
			billedCalls = 1
		}
	} else if price.PerRequest > 0 {
		inputPrice = price.PerRequest
		if billedCalls < 1 {
			billedCalls = 1
		}
	}
	calc := s.calculator
	if calc == nil {
		calc = billing.NewCalculator()
	}
	result := calc.Calculate(billing.CalculateInput{
		InputCost:         costs.Input,
		OutputCost:        costs.Output,
		CachedInputCost:   costs.Cached,
		CacheCreationCost: costs.CacheCreation5m + costs.CacheCreation1h,
		BillingRate:       0, // 不向任何用户计费
		SellRate:          0,
		AccountRate:       item.RateMultiplier,
	})
	inputTokens := usage.InputTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	s.usageSink.Record(billing.UsageRecord{
		AccountID:             item.ID,
		Model:                 model,
		InputTokens:           inputTokens,
		OutputTokens:          usage.OutputTokens,
		CachedInputTokens:     usage.CachedTokens,
		Calls:                 billedCalls,
		InputPrice:            inputPrice,
		OutputPrice:           price.Output,
		CachedInputPrice:      price.CachedInput,
		CacheCreationPrice:    price.CacheCreation5m,
		CacheCreation1hPrice:  price.CacheCreation1h,
		ImageSize:             usage.ImageSize,
		ImageQuality:          usage.ImageQuality,
		VideoResolution:       usage.VideoResolution,
		InputCost:             result.InputCost,
		OutputCost:            result.OutputCost,
		CachedInputCost:       result.CachedInputCost,
		CacheCreationCost:     result.CacheCreationCost,
		TotalCost:             result.TotalCost,
		AccountRateMultiplier: result.AccountRateMultiplier,
		Stream:                true,
		Endpoint:              endpoint,
		Source:                billing.SourceAccountTest,
		DurationMs:            durationMs,
		RequestID:             uuid.NewString(),
	})
}

func (s *Service) testCodex(ctx context.Context, item Account, modelID, prompt string, mode TestMode, proxyURL string, emit func(TestEvent)) (string, testStreamUsage, error) {
	model := pickDefaultTestModel(item.Platform, resolvePlanType(item), modelID)
	if model == "" {
		return "", testStreamUsage{}, emitErr(emit, "无可测模型")
	}

	// 统一凭证键名（兼容 accessToken / refreshToken 等）
	normalizeCredentialKeys(item.Credentials)

	// OAuth 账号常只存 refresh_token / session_token：测试前自动换票并落库。
	if err := s.ensureCodexAccessToken(ctx, &item, proxyURL, emit); err != nil {
		return model, testStreamUsage{}, err
	}

	token := strings.TrimSpace(item.Credentials["access_token"])
	apiKey := strings.TrimSpace(item.Credentials["api_key"])
	if token == "" && apiKey == "" {
		return model, testStreamUsage{}, emitErr(emit, credentialMissingMsg(item.Credentials))
	}

	var apiURL string
	isOAuth := false
	if token != "" {
		apiURL = chatgptCodexAPIURL
		isOAuth = true
	} else {
		base := strings.TrimRight(firstNonEmpty(item.Credentials["base_url"], "https://api.openai.com"), "/")
		apiURL = base + "/responses"
	}

	payload := map[string]any{
		"model": model,
		"input": []map[string]any{
			{"role": "user", "content": []map[string]any{{"type": "input_text", "text": resolveAccountTestPrompt(model, prompt)}}},
		},
		"stream":       true,
		"instructions": "You are a helpful assistant. Reply briefly.",
	}
	if isOAuth {
		payload["store"] = false
	}
	raw, _ := json.Marshal(payload)
	if mode == TestModeOverage {
		transformed, transformErr := s.transformAccountTestRequest(ctx, mode, model, raw)
		if transformErr != nil {
			if errors.Is(transformErr, accounttesthook.ErrUnavailable) {
				return model, testStreamUsage{}, emitErr(emit, "Codex超额测试插件不可用，请先安装并启用支持该模式的插件")
			}
			return model, testStreamUsage{}, emitErr(emit, "Codex超额测试请求处理失败: "+transformErr.Error())
		}
		raw = transformed
	}

	emit(TestEvent{Type: "test_start", Model: model})
	execute := func() (*http.Response, error) {
		auth := strings.TrimSpace(item.Credentials["api_key"])
		if isOAuth {
			auth = strings.TrimSpace(item.Credentials["access_token"])
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Accept", "text/event-stream")
		if isOAuth {
			req.Host = "chatgpt.com"
			if aid := strings.TrimSpace(item.Credentials["chatgpt_account_id"]); aid != "" {
				req.Header.Set("ChatGPT-Account-Id", aid)
			}
			req.Header.Set("originator", codexOriginator)
			req.Header.Set("User-Agent", codexOriginator)
		}
		return httpClient(proxyURL).Do(req)
	}

	resp, err := execute()
	if err != nil {
		return model, testStreamUsage{}, emitErr(emit, "请求失败: "+err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		_ = resp.Body.Close()
		if isOAuth && cpa.IsRefreshableAuthFailure(item.Platform, resp.StatusCode, string(body)) &&
			(strings.TrimSpace(item.Credentials["refresh_token"]) != "" || strings.TrimSpace(item.Credentials["session_token"]) != "") {
			if refreshErr := s.refreshOAuthCredentials(ctx, &item, proxyURL); refreshErr != nil {
				return model, testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body)+"\naccess_token 刷新失败: "+refreshErr.Error())
			}
			resp, err = execute()
			if err != nil {
				return model, testStreamUsage{}, emitErr(emit, "刷新凭证后重试失败: "+err.Error())
			}
		} else {
			return model, testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		return model, testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
	}
	usage, err := processOpenAIResponsesStream(resp.Body, emit)
	return model, usage, err
}

func (s *Service) transformAccountTestRequest(ctx context.Context, mode TestMode, model string, body json.RawMessage) (json.RawMessage, error) {
	if s == nil || s.testTransformer == nil {
		return nil, accounttesthook.ErrUnavailable
	}
	decision, err := s.testTransformer.TransformAccountTest(ctx, accounttesthook.Request{
		Version:  accounttesthook.VersionV1,
		Mode:     string(mode),
		Platform: "codex",
		Endpoint: "responses",
		Model:    model,
		Body:     append(json.RawMessage(nil), body...),
	})
	if err != nil {
		return nil, err
	}
	if decision.Version != accounttesthook.VersionV1 || len(decision.RequestBody) == 0 {
		return nil, fmt.Errorf("插件未返回有效的账号测试请求体")
	}
	return append(json.RawMessage(nil), decision.RequestBody...), nil
}

func (s *Service) testClaude(ctx context.Context, item Account, modelID, prompt, proxyURL string, emit func(TestEvent)) (string, testStreamUsage, error) {
	model := pickDefaultTestModel(item.Platform, resolvePlanType(item), modelID)
	if model == "" {
		return "", testStreamUsage{}, emitErr(emit, "无可测模型")
	}
	normalizeCredentialKeys(item.Credentials)
	if err := s.ensureOAuthCredentialsFresh(ctx, &item, proxyURL); err != nil {
		return model, testStreamUsage{}, emitErr(emit, "access_token 刷新失败: "+err.Error())
	}
	token := strings.TrimSpace(item.Credentials["access_token"])
	apiKey := strings.TrimSpace(item.Credentials["api_key"])
	useBearer := token != ""
	if !useBearer && apiKey == "" {
		return model, testStreamUsage{}, emitErr(emit, credentialMissingMsg(item.Credentials))
	}

	apiURL := testClaudeAPIURL
	if !useBearer {
		base := strings.TrimRight(firstNonEmpty(item.Credentials["base_url"], "https://api.anthropic.com"), "/")
		apiURL = base + "/v1/messages?beta=true"
	}

	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{{"type": "text", "text": resolveAccountTestPrompt(model, prompt)}}},
		},
		"max_tokens":  64,
		"temperature": 1,
		"stream":      true,
	}
	raw, _ := json.Marshal(payload)

	emit(TestEvent{Type: "test_start", Model: model})
	execute := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("创建请求失败: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Accept", "text/event-stream")
		if useBearer {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(item.Credentials["access_token"]))
			req.Header.Set("anthropic-beta", claudeBetaOAuth)
		} else {
			req.Header.Set("x-api-key", apiKey)
		}
		return httpClient(proxyURL).Do(req)
	}

	resp, err := execute()
	if err != nil {
		return model, testStreamUsage{}, emitErr(emit, "请求失败: "+err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		_ = resp.Body.Close()
		if useBearer && cpa.IsRefreshableAuthFailure(item.Platform, resp.StatusCode, string(body)) &&
			strings.TrimSpace(item.Credentials["refresh_token"]) != "" {
			if refreshErr := s.refreshOAuthCredentials(ctx, &item, proxyURL); refreshErr != nil {
				return model, testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body)+"\naccess_token 刷新失败: "+refreshErr.Error())
			}
			resp, err = execute()
			if err != nil {
				return model, testStreamUsage{}, emitErr(emit, "刷新凭证后重试失败: "+err.Error())
			}
		} else {
			return model, testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		return model, testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
	}
	usage, err := processClaudeStream(resp.Body, emit)
	return model, usage, err
}

// testXAI 按模型类型选择文本、生图或视频测试端点。
func (s *Service) testXAI(ctx context.Context, item Account, modelID, prompt string, mediaOptions TestMediaOptions, proxyURL string, emit func(TestEvent)) (string, testStreamUsage, error) {
	model := pickDefaultTestModel(item.Platform, resolvePlanType(item), modelID)
	if model == "" {
		return "", testStreamUsage{}, emitErr(emit, "无可测模型")
	}
	normalizeCredentialKeys(item.Credentials)
	if NormalizeAccountType(item.Type) == TypeOAuth {
		if err := s.ensureXAIAccessToken(ctx, &item, proxyURL, emit); err != nil {
			return model, testStreamUsage{}, err
		}
	}
	prompt = resolveAccountTestPrompt(model, prompt)
	emit(TestEvent{Type: "test_start", Model: model})

	switch accountTestModelKind(model) {
	case testModelKindImage:
		usage, err := s.testXAIImage(ctx, &item, model, prompt, proxyURL, emit)
		return model, usage, err
	case testModelKindVideo:
		usage, err := s.testXAIVideo(ctx, &item, model, prompt, mediaOptions, proxyURL, emit)
		return model, usage, err
	default:
		usage, err := s.testXAIText(ctx, &item, model, prompt, proxyURL, emit)
		return model, usage, err
	}
}

// testXAIText 发送最小 Responses API 流式请求。
// OAuth 默认走 Grok CLI chat-proxy；API Key 默认走 xAI 官方 API。
func (s *Service) testXAIText(ctx context.Context, item *Account, model, prompt, proxyURL string, emit func(TestEvent)) (testStreamUsage, error) {
	payload := map[string]any{
		"model": model,
		"input": []map[string]any{
			{"role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}},
		},
		"stream":       true,
		"instructions": "You are a helpful assistant. Reply briefly.",
	}
	raw, _ := json.Marshal(payload)

	resp, err := executeXAITestRequest(ctx, *item, proxyURL, raw)
	if err != nil {
		return testStreamUsage{}, emitErr(emit, "请求失败: "+err.Error())
	}

	// xAI 会用 403 bad-credentials 表达 OAuth access_token 已失效。
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) &&
		NormalizeAccountType(item.Type) == TypeOAuth &&
		strings.TrimSpace(item.Credentials["refresh_token"]) != "" {
		firstBody, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		_ = resp.Body.Close()
		if !cpa.IsRefreshableAuthFailure(item.Platform, resp.StatusCode, string(firstBody)) {
			return testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, firstBody))
		}
		firstStatus := resp.StatusCode
		if refreshErr := s.refreshOAuthCredentials(ctx, item, proxyURL); refreshErr != nil {
			msg := formatUpstreamHTTPError(firstStatus, firstBody) + "\naccess_token 刷新失败: " + refreshErr.Error()
			return testStreamUsage{}, emitErr(emit, msg)
		}
		resp, err = executeXAITestRequest(ctx, *item, proxyURL, raw)
		if err != nil {
			return testStreamUsage{}, emitErr(emit, "刷新凭证后重试失败: "+err.Error())
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
		return testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
	}
	usage, err := processOpenAIResponsesStream(resp.Body, emit)
	return usage, err
}

func (s *Service) testXAIImage(ctx context.Context, item *Account, model, prompt, proxyURL string, emit func(TestEvent)) (testStreamUsage, error) {
	raw, _ := json.Marshal(map[string]any{
		"model":           model,
		"prompt":          prompt,
		"response_format": "url",
		"aspect_ratio":    "1:1",
		"resolution":      "1k",
		"n":               1,
	})
	resp, err := s.executeXAIMediaTestRequest(ctx, item, proxyURL, http.MethodPost, "/images/generations", raw)
	if err != nil {
		return testStreamUsage{}, emitErr(emit, "生图请求失败: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, testMediaMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
	}
	var result struct {
		Data []struct {
			URL           string `json:"url"`
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return testStreamUsage{}, emitErr(emit, "解析 xAI 生图响应失败: "+err.Error())
	}
	if len(result.Data) == 0 {
		return testStreamUsage{}, emitErr(emit, "xAI 生图响应未包含图片数据\n"+truncate(prettyUpstreamBody(body, string(body)), 4000))
	}
	for i, image := range result.Data {
		switch {
		case strings.TrimSpace(image.URL) != "":
			imageURL := strings.TrimSpace(image.URL)
			emit(TestEvent{Type: "content", Text: fmt.Sprintf("图片 %d：%s\n", i+1, imageURL)})
			emit(TestEvent{Type: "media", MediaKind: testModelKindImage, URL: imageURL})
		case strings.TrimSpace(image.B64JSON) != "":
			emit(TestEvent{Type: "content", Text: fmt.Sprintf("图片 %d 已生成（base64 数据，%d 个字符）\n", i+1, len(image.B64JSON))})
			emit(TestEvent{Type: "media", MediaKind: testModelKindImage, URL: "data:image/jpeg;base64," + strings.TrimSpace(image.B64JSON)})
		default:
			emit(TestEvent{Type: "content", Text: fmt.Sprintf("图片 %d 已生成\n", i+1)})
		}
		if strings.TrimSpace(image.RevisedPrompt) != "" {
			emit(TestEvent{Type: "content", Text: "优化后的文案：" + strings.TrimSpace(image.RevisedPrompt) + "\n"})
		}
	}
	emit(TestEvent{Type: "test_complete", Success: true})
	return testStreamUsage{Calls: len(result.Data), ImageSize: "1k"}, nil
}

func (s *Service) testXAIVideo(ctx context.Context, item *Account, model, prompt string, mediaOptions TestMediaOptions, proxyURL string, emit func(TestEvent)) (testStreamUsage, error) {
	videoOptions, err := resolveXAIVideoTestOptions(model, mediaOptions)
	if err != nil {
		return testStreamUsage{}, emitErr(emit, err.Error())
	}
	raw, _ := json.Marshal(map[string]any{
		"model":        model,
		"prompt":       prompt,
		"duration":     videoOptions.Duration,
		"aspect_ratio": videoOptions.AspectRatio,
		"resolution":   videoOptions.Resolution,
	})
	resp, err := s.executeXAIMediaTestRequest(ctx, item, proxyURL, http.MethodPost, "/videos/generations", raw)
	if err != nil {
		return testStreamUsage{}, emitErr(emit, "视频生成请求失败: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return testStreamUsage{}, emitErr(emit, formatUpstreamHTTPError(resp.StatusCode, body))
	}
	var result xaiVideoTestResult
	if err := json.Unmarshal(body, &result); err != nil {
		return testStreamUsage{}, emitErr(emit, "解析 xAI 视频响应失败: "+err.Error())
	}
	if strings.TrimSpace(result.RequestID) != "" {
		emit(TestEvent{Type: "content", Text: "视频任务已创建：" + strings.TrimSpace(result.RequestID) + "\n"})
	}
	if strings.TrimSpace(result.Status) != "" {
		emitXAIVideoStatus(emit, result)
	}
	if strings.TrimSpace(result.Video.URL) != "" {
		emitXAIVideoMedia(emit, result)
		emit(TestEvent{Type: "test_complete", Success: true})
		return xaiVideoBillingUsage(videoOptions), nil
	}
	if strings.TrimSpace(result.RequestID) == "" {
		return testStreamUsage{}, emitErr(emit, "xAI 视频响应未包含任务 ID 或视频地址\n"+truncate(prettyUpstreamBody(body, string(body)), 4000))
	}
	emit(TestEvent{
		Type:      "media_status",
		Text:      "视频正在生成，请稍候…",
		MediaKind: testModelKindVideo,
		RequestID: strings.TrimSpace(result.RequestID),
		Status:    firstNonEmpty(strings.TrimSpace(result.Status), "pending"),
	})
	completed, err := s.pollXAIVideo(ctx, item, proxyURL, strings.TrimSpace(result.RequestID), emit)
	if err != nil {
		return testStreamUsage{}, err
	}
	emitXAIVideoMedia(emit, completed)
	emit(TestEvent{Type: "test_complete", Success: true})
	return xaiVideoBillingUsage(videoOptions), nil
}

func xaiVideoBillingUsage(options TestMediaOptions) testStreamUsage {
	return testStreamUsage{
		VideoSeconds:    options.Duration,
		VideoResolution: options.Resolution,
	}
}

func resolveXAIVideoTestOptions(model string, input TestMediaOptions) (TestMediaOptions, error) {
	resolved := TestMediaOptions{
		Duration:    input.Duration,
		AspectRatio: strings.TrimSpace(input.AspectRatio),
		Resolution:  strings.ToLower(strings.TrimSpace(input.Resolution)),
	}
	if resolved.Duration == 0 {
		resolved.Duration = 1
	}
	if resolved.AspectRatio == "" {
		resolved.AspectRatio = "16:9"
	}
	if resolved.Resolution == "" {
		resolved.Resolution = "720p"
	}
	if resolved.Duration < 1 || resolved.Duration > 15 {
		return TestMediaOptions{}, fmt.Errorf("视频时长必须在 1～15 秒之间")
	}
	switch resolved.AspectRatio {
	case "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3":
	default:
		return TestMediaOptions{}, fmt.Errorf("不支持的视频画幅：%s", resolved.AspectRatio)
	}
	switch resolved.Resolution {
	case "480p", "720p":
	case "1080p":
		if !supportsXAI1080pVideo(model) {
			return TestMediaOptions{}, fmt.Errorf("模型 %s 不支持 1080p 视频，请选择 Grok Imagine Video 1.5", model)
		}
	default:
		return TestMediaOptions{}, fmt.Errorf("不支持的视频分辨率：%s", resolved.Resolution)
	}
	return resolved, nil
}

func supportsXAI1080pVideo(model string) bool {
	base := strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(base, "/"); slash >= 0 && slash < len(base)-1 {
		base = base[slash+1:]
	}
	return base == "grok-imagine-video-1.5" || base == "grok-imagine-video-1.5-preview"
}

type xaiVideoTestResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Progress  any    `json:"progress"`
	Video     struct {
		URL string `json:"url"`
	} `json:"video"`
}

func (s *Service) pollXAIVideo(ctx context.Context, item *Account, proxyURL, requestID string, emit func(TestEvent)) (xaiVideoTestResult, error) {
	ticker := time.NewTicker(testXAIVideoPollInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(testXAIVideoPollTimeout)
	defer timeout.Stop()

	for {
		result, body, err := s.fetchXAIVideoResult(ctx, item, proxyURL, requestID)
		if err != nil {
			return xaiVideoTestResult{}, emitErr(emit, "查询视频任务失败: "+err.Error())
		}
		status := strings.ToLower(strings.TrimSpace(result.Status))
		if strings.TrimSpace(result.Video.URL) != "" && status == "" {
			status = "done"
			result.Status = status
		}
		if status == "" {
			status = "pending"
			result.Status = status
		}
		// 每轮推送状态，既刷新前端提示，也避免长时间生成时 SSE 被代理判定为空闲。
		emitXAIVideoStatus(emit, result)
		switch status {
		case "done":
			if strings.TrimSpace(result.Video.URL) == "" {
				return xaiVideoTestResult{}, emitErr(emit, "xAI 视频任务已完成，但响应未包含视频地址\n"+truncate(prettyUpstreamBody(body, string(body)), 4000))
			}
			result.RequestID = requestID
			return result, nil
		case "failed", "expired":
			return xaiVideoTestResult{}, emitErr(emit, fmt.Sprintf("xAI 视频任务状态为 %s\n%s", status, truncate(prettyUpstreamBody(body, string(body)), 4000)))
		}

		select {
		case <-ctx.Done():
			return xaiVideoTestResult{}, ctx.Err()
		case <-timeout.C:
			return xaiVideoTestResult{}, emitErr(emit, fmt.Sprintf("等待视频生成超时，任务 ID：%s", requestID))
		case <-ticker.C:
		}
	}
}

func (s *Service) fetchXAIVideoResult(ctx context.Context, item *Account, proxyURL, requestID string) (xaiVideoTestResult, []byte, error) {
	var result xaiVideoTestResult
	endpoint := "/videos/" + url.PathEscape(requestID)
	resp, err := s.executeXAIMediaTestRequest(ctx, item, proxyURL, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, body, fmt.Errorf("%s", formatUpstreamHTTPError(resp.StatusCode, body))
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return result, body, fmt.Errorf("解析 xAI 视频任务响应失败: %w", err)
	}
	result.RequestID = requestID
	return result, body, nil
}

func emitXAIVideoStatus(emit func(TestEvent), result xaiVideoTestResult) {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		return
	}
	emit(TestEvent{
		Type:      "media_status",
		Text:      "视频任务状态：" + status,
		MediaKind: testModelKindVideo,
		RequestID: strings.TrimSpace(result.RequestID),
		Status:    status,
	})
}

func emitXAIVideoMedia(emit func(TestEvent), result xaiVideoTestResult) {
	videoURL := strings.TrimSpace(result.Video.URL)
	if videoURL == "" {
		return
	}
	emit(TestEvent{Type: "content", Text: "视频地址：" + videoURL + "\n"})
	emit(TestEvent{
		Type:      "media",
		MediaKind: testModelKindVideo,
		URL:       videoURL,
		RequestID: strings.TrimSpace(result.RequestID),
		Status:    strings.TrimSpace(result.Status),
	})
}

func xaiTestEndpoint(model string) string {
	switch accountTestModelKind(model) {
	case testModelKindImage:
		return "/v1/images/generations"
	case testModelKindVideo:
		return "/v1/videos/generations"
	default:
		return "/v1/responses"
	}
}

func (s *Service) executeXAIMediaTestRequest(ctx context.Context, item *Account, proxyURL, method, endpoint string, body []byte) (*http.Response, error) {
	resp, err := executeXAIMediaTestRequest(ctx, *item, proxyURL, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if (resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden) ||
		NormalizeAccountType(item.Type) != TypeOAuth ||
		strings.TrimSpace(item.Credentials["refresh_token"]) == "" {
		return resp, nil
	}
	firstBody, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
	_ = resp.Body.Close()
	if !cpa.IsRefreshableAuthFailure(item.Platform, resp.StatusCode, string(firstBody)) {
		resp.Body = io.NopCloser(bytes.NewReader(firstBody))
		return resp, nil
	}
	firstStatus := resp.StatusCode
	if err := s.refreshOAuthCredentials(ctx, item, proxyURL); err != nil {
		return nil, fmt.Errorf("%s\naccess_token 刷新失败: %w", formatUpstreamHTTPError(firstStatus, firstBody), err)
	}
	return executeXAIMediaTestRequest(ctx, *item, proxyURL, method, endpoint, body)
}

func executeXAIMediaTestRequest(ctx context.Context, item Account, proxyURL, method, endpoint string, body []byte) (*http.Response, error) {
	apiURL, auth, err := resolveXAIMediaTestTarget(item, endpoint)
	if err != nil {
		return nil, err
	}
	var requestBody io.Reader
	if len(body) > 0 {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	applyXAIMediaTestHeaders(req, auth)
	return httpClient(proxyURL).Do(req)

}

func resolveXAIMediaTestTarget(item Account, endpoint string) (apiURL, auth string, err error) {
	auth, err = resolveXAITestAuth(item)
	if err != nil {
		return "", "", err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(item.Credentials["base_url"]), "/")
	if baseURL == "" || sameNormalizedURL(baseURL, testXAICLIBaseURL) {
		baseURL = testXAIOfficialBaseURL
	}
	return baseURL + endpoint, auth, nil

}

func applyXAIMediaTestHeaders(req *http.Request, auth string) {
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Connection", "Keep-Alive")
}

func executeXAITestRequest(ctx context.Context, item Account, proxyURL string, body []byte) (*http.Response, error) {
	apiURL, auth, useCLIHeaders, err := resolveXAITestTarget(item)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	applyXAITestHeaders(req, auth, useCLIHeaders)
	return httpClient(proxyURL).Do(req)
}

func resolveXAITestTarget(item Account) (apiURL, auth string, useCLIHeaders bool, err error) {
	creds := item.Credentials
	accountType := NormalizeAccountType(item.Type)
	accessToken := strings.TrimSpace(creds["access_token"])
	apiKey := strings.TrimSpace(creds["api_key"])
	baseURL := strings.TrimRight(strings.TrimSpace(creds["base_url"]), "/")
	usingAPI := strings.EqualFold(strings.TrimSpace(creds["using_api"]), "true")

	if accountType == TypeAPIKey && apiKey != "" {
		auth = apiKey
		if baseURL == "" {
			baseURL = testXAIOfficialBaseURL
		}
	} else if accessToken != "" {
		auth = accessToken
		if baseURL == "" {
			baseURL = testXAIOfficialBaseURL
		}
		if !usingAPI && sameNormalizedURL(baseURL, testXAIOfficialBaseURL) {
			baseURL = testXAICLIBaseURL
		}
		useCLIHeaders = !usingAPI && sameNormalizedURL(baseURL, testXAICLIBaseURL)
	} else if apiKey != "" {
		auth = apiKey
		if baseURL == "" {
			baseURL = testXAIOfficialBaseURL
		}
	} else {
		return "", "", false, fmt.Errorf("%s", credentialMissingMsg(creds))
	}

	return baseURL + "/responses", auth, useCLIHeaders, nil
}

func resolveXAITestAuth(item Account) (string, error) {
	creds := item.Credentials
	accountType := NormalizeAccountType(item.Type)
	accessToken := strings.TrimSpace(creds["access_token"])
	apiKey := strings.TrimSpace(creds["api_key"])
	switch {
	case accountType == TypeAPIKey && apiKey != "":
		return apiKey, nil
	case accessToken != "":
		return accessToken, nil
	case apiKey != "":
		return apiKey, nil
	default:
		return "", fmt.Errorf("%s", credentialMissingMsg(creds))
	}
}

func sameNormalizedURL(left, right string) bool {
	return strings.EqualFold(strings.TrimRight(strings.TrimSpace(left), "/"), strings.TrimRight(strings.TrimSpace(right), "/"))
}

func applyXAITestHeaders(req *http.Request, auth string, useCLIHeaders bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+auth)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "Keep-Alive")
	if useCLIHeaders {
		req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
		req.Header.Set("x-grok-client-version", testXAIClientVersion)
		req.Header.Set("User-Agent", "xai-grok-workspace/"+testXAIClientVersion)
	}
}

// ensureXAIAccessToken 在 access_token 缺失或临近过期时使用 refresh_token 换票。
func (s *Service) ensureXAIAccessToken(ctx context.Context, item *Account, proxyURL string, emit func(TestEvent)) error {
	if item == nil {
		return emitErr(emit, "账号为空")
	}
	if item.Credentials == nil {
		item.Credentials = map[string]string{}
	}
	normalizeCredentialKeys(item.Credentials)
	if strings.TrimSpace(item.Credentials["api_key"]) != "" {
		return nil
	}
	if strings.TrimSpace(item.Credentials["access_token"]) != "" && !oauthCredentialsNeedRefresh(*item, time.Now()) {
		return nil
	}
	if strings.TrimSpace(item.Credentials["refresh_token"]) == "" {
		return emitErr(emit, credentialMissingMsg(item.Credentials))
	}
	if err := s.refreshOAuthCredentials(ctx, item, proxyURL); err != nil {
		return emitErr(emit, "access_token 刷新失败: "+err.Error())
	}
	return nil
}

func (s *Service) refreshXAIAccessToken(ctx context.Context, item *Account, proxyURL string) error {
	if item == nil {
		return fmt.Errorf("账号为空")
	}
	refreshToken := strings.TrimSpace(item.Credentials["refresh_token"])
	if refreshToken == "" {
		return fmt.Errorf("refresh_token 为空")
	}
	tokenEndpoint := strings.TrimSpace(item.Credentials["token_endpoint"])
	if tokenEndpoint == "" {
		tokenEndpoint = testXAITokenEndpoint
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", testXAIClientID)
	form.Set("refresh_token", refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("创建 xAI token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(proxyURL).Do(req)
	if err != nil {
		return fmt.Errorf("xAI token 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, testMaxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("xAI token HTTP %d: %s", resp.StatusCode, truncate(strings.TrimSpace(string(body)), 1000))
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("解析 xAI token 响应失败: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return fmt.Errorf("xAI token 响应缺少 access_token")
	}
	merged := cloneStringMap(item.Credentials)
	if merged == nil {
		merged = map[string]string{}
	}
	merged["access_token"] = strings.TrimSpace(tokenResp.AccessToken)
	if strings.TrimSpace(tokenResp.RefreshToken) != "" {
		merged["refresh_token"] = strings.TrimSpace(tokenResp.RefreshToken)
	}
	if strings.TrimSpace(tokenResp.IDToken) != "" {
		merged["id_token"] = strings.TrimSpace(tokenResp.IDToken)
		applyXAIIdentityClaims(merged, tokenResp.IDToken)
	}
	if strings.TrimSpace(tokenResp.TokenType) != "" {
		merged["token_type"] = strings.TrimSpace(tokenResp.TokenType)
	}
	if tokenResp.ExpiresIn > 0 {
		merged["expires_in"] = fmt.Sprintf("%d", tokenResp.ExpiresIn)
		merged["expired"] = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	merged["token_endpoint"] = tokenEndpoint
	merged["auth_kind"] = TypeOAuth
	merged["credential_origin"] = TypeOAuth
	item.Credentials = merged

	// 测试可在无仓储的单元场景运行；生产环境换票成功后尽量持久化新凭证。
	if s != nil && s.repo != nil && item.ID > 0 {
		if _, err := s.Update(ctx, item.ID, UpdateInput{Credentials: merged}); err != nil {
			_ = err
		}
	}
	return nil
}

func emitErr(emit func(TestEvent), msg string) error {
	emit(TestEvent{Type: "error", Error: msg})
	return fmt.Errorf("%s", msg)
}

// formatUpstreamHTTPError 直接展示上游响应体（JSON 尽量 pretty-print），不二次摘要，避免重复。
func formatUpstreamHTTPError(status int, body []byte) string {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return fmt.Sprintf("上游 HTTP %d（空响应体）", status)
	}
	display := prettyUpstreamBody(body, raw)
	return fmt.Sprintf("上游 HTTP %d\n%s", status, truncate(display, 4000))
}

// prettyUpstreamBody JSON 则缩进格式化，否则原样返回。
func prettyUpstreamBody(body []byte, raw string) string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return raw
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return string(pretty)
}

// normalizeCredentialKeys 把常见别名归一到 snake_case（不改动原有值优先）。
func normalizeCredentialKeys(creds map[string]string) {
	if creds == nil {
		return
	}
	aliases := map[string]string{
		"accessToken":      "access_token",
		"AccessToken":      "access_token",
		"refreshToken":     "refresh_token",
		"RefreshToken":     "refresh_token",
		"idToken":          "id_token",
		"IdToken":          "id_token",
		"sessionToken":     "session_token",
		"SessionToken":     "session_token",
		"apiKey":           "api_key",
		"ApiKey":           "api_key",
		"API_KEY":          "api_key",
		"clientId":         "client_id",
		"ClientId":         "client_id",
		"chatgptAccountId": "chatgpt_account_id",
	}
	for from, to := range aliases {
		if v := strings.TrimSpace(creds[from]); v != "" {
			if strings.TrimSpace(creds[to]) == "" {
				creds[to] = v
			}
		}
	}
}

func credentialMissingMsg(creds map[string]string) string {
	keys := make([]string, 0, len(creds))
	for k, v := range creds {
		if strings.TrimSpace(v) == "" {
			continue
		}
		// 不泄露值，只列键名
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return "本地凭证为空（解密后无任何字段），未向上游发请求。请重新导入 refresh_token / session / OAuth 授权。"
	}
	// 明确标注：这是本地短路，不是上游错误体，避免管理员误判为「上游只回了 email」
	return fmt.Sprintf(
		"本地凭证缺少可用 token（需要 access_token / refresh_token / session_token / api_key 之一），未向上游发请求，因此没有上游响应体。\n"+
			"当前已存字段：%s\n"+
			"常见原因：编辑账号时只改了 email/分组等，旧版本会把 token 整包覆盖掉；请重新导入 AT/RT/Session 或 OAuth 授权补回凭证。",
		strings.Join(keys, ", "),
	)
}

// ensureCodexAccessToken 保证 item.Credentials 含未临近过期的 access_token。
func (s *Service) ensureCodexAccessToken(ctx context.Context, item *Account, proxyURL string, emit func(TestEvent)) error {
	if item == nil {
		return fmt.Errorf("账号为空")
	}
	if item.Credentials == nil {
		item.Credentials = map[string]string{}
	}
	normalizeCredentialKeys(item.Credentials)
	if strings.TrimSpace(item.Credentials["access_token"]) != "" && !oauthCredentialsNeedRefresh(*item, time.Now()) {
		return nil
	}
	// 纯 api_key 账号不走 OAuth 刷新
	if strings.TrimSpace(item.Credentials["api_key"]) != "" &&
		strings.TrimSpace(item.Credentials["refresh_token"]) == "" &&
		strings.TrimSpace(item.Credentials["session_token"]) == "" {
		return nil
	}
	if strings.TrimSpace(item.Credentials["refresh_token"]) == "" && strings.TrimSpace(item.Credentials["session_token"]) == "" {
		return emitErr(emit, credentialMissingMsg(item.Credentials))
	}
	if err := s.refreshOAuthCredentials(ctx, item, proxyURL); err != nil {
		return emitErr(emit, "access_token 刷新失败: "+err.Error())
	}
	return nil
}

func processClaudeStream(body io.Reader, emit func(TestEvent)) (testStreamUsage, error) {
	var usage testStreamUsage
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				emit(TestEvent{Type: "test_complete", Success: true})
				return usage, nil
			}
			return usage, emitErr(emit, "流读取失败: "+err.Error())
		}
		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}
		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			emit(TestEvent{Type: "test_complete", Success: true})
			return usage, nil
		}
		var data map[string]any
		if json.Unmarshal([]byte(jsonStr), &data) != nil {
			continue
		}
		switch data["type"] {
		case "content_block_delta":
			if delta, ok := data["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok && text != "" {
					emit(TestEvent{Type: "content", Text: text})
				}
			}
		case "message_delta", "message_start":
			// message.usage / usage 字段
			if msg, ok := data["message"].(map[string]any); ok {
				mergeUsageMap(&usage, msg["usage"])
			}
			mergeUsageMap(&usage, data["usage"])
		case "message_stop":
			emit(TestEvent{Type: "test_complete", Success: true})
			return usage, nil
		case "error":
			raw, _ := json.Marshal(data)
			return usage, emitErr(emit, "上游流式错误\n"+prettyUpstreamBody(raw, string(raw)))
		}
	}
}

func processOpenAIResponsesStream(body io.Reader, emit func(TestEvent)) (testStreamUsage, error) {
	var usage testStreamUsage
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				emit(TestEvent{Type: "test_complete", Success: true})
				return usage, nil
			}
			return usage, emitErr(emit, "流读取失败: "+err.Error())
		}
		line = strings.TrimSpace(line)
		if line == "" || !sseDataPrefix.MatchString(line) {
			continue
		}
		jsonStr := sseDataPrefix.ReplaceAllString(line, "")
		if jsonStr == "[DONE]" {
			emit(TestEvent{Type: "test_complete", Success: true})
			return usage, nil
		}
		var data map[string]any
		if json.Unmarshal([]byte(jsonStr), &data) != nil {
			continue
		}
		// Responses API / ChatGPT codex：多种 delta 形态
		etype, _ := data["type"].(string)
		switch {
		case etype == "response.output_text.delta":
			if t, ok := data["delta"].(string); ok && t != "" {
				emit(TestEvent{Type: "content", Text: t})
			}
		case etype == "response.completed", etype == "response.done":
			if resp, ok := data["response"].(map[string]any); ok {
				mergeUsageMap(&usage, resp["usage"])
			}
			mergeUsageMap(&usage, data["usage"])
			emit(TestEvent{Type: "test_complete", Success: true})
			return usage, nil
		case strings.Contains(etype, "error"):
			raw, _ := json.Marshal(data)
			return usage, emitErr(emit, "上游流式错误\n"+prettyUpstreamBody(raw, string(raw)))
		default:
			// chat.completions 兼容
			if choices, ok := data["choices"].([]any); ok && len(choices) > 0 {
				if c0, ok := choices[0].(map[string]any); ok {
					if delta, ok := c0["delta"].(map[string]any); ok {
						if t, ok := delta["content"].(string); ok && t != "" {
							emit(TestEvent{Type: "content", Text: t})
						}
					}
					if fr, ok := c0["finish_reason"].(string); ok && fr != "" {
						mergeUsageMap(&usage, data["usage"])
						emit(TestEvent{Type: "test_complete", Success: true})
						return usage, nil
					}
				}
			}
			mergeUsageMap(&usage, data["usage"])
		}
	}
}

func mergeUsageMap(dst *testStreamUsage, raw any) {
	if dst == nil || raw == nil {
		return
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	// OpenAI: input_tokens / output_tokens / prompt_tokens / completion_tokens
	// Claude: input_tokens / output_tokens / cache_read_input_tokens
	if n := anyToInt(m["input_tokens"]); n > 0 {
		dst.InputTokens = n
	}
	if n := anyToInt(m["prompt_tokens"]); n > 0 {
		dst.InputTokens = n
	}
	if n := anyToInt(m["output_tokens"]); n > 0 {
		dst.OutputTokens = n
	}
	if n := anyToInt(m["completion_tokens"]); n > 0 {
		dst.OutputTokens = n
	}
	if n := anyToInt(m["cache_read_input_tokens"]); n > 0 {
		dst.CachedTokens = n
	}
	if n := anyToInt(m["cached_tokens"]); n > 0 {
		dst.CachedTokens = n
	}
}

func anyToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}
