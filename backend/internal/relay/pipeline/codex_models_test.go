package pipeline

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

func TestMergeCodexModelMetadataPreservesBundledValuesWhenOverlayIsSparse(t *testing.T) {
	base := cpa.ModelInfo{
		ID:                       "gpt-test",
		DisplayName:              "Bundled Test",
		OwnedBy:                  "openai",
		Type:                     "openai",
		Description:              "bundled description",
		ContextLength:            272000,
		MaxCompletionTokens:      128000,
		Thinking:                 cpa.ModelThinkingInfo{Levels: []string{"low", "high"}},
		SupportedInputModalities: []string{"text", "image"},
	}
	overlay := cpa.ModelInfo{ID: "gpt-test"}

	got := mergeCodexModelMetadata(base, overlay)
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("sparse overlay changed bundled metadata:\n got: %#v\nwant: %#v", got, base)
	}

	// The merge must clone slices: callers may mutate the returned descriptor
	// without changing the catalog baseline.
	got.Thinking.Levels[0] = "mutated"
	got.SupportedInputModalities[0] = "mutated"
	if base.Thinking.Levels[0] != "low" || base.SupportedInputModalities[0] != "text" {
		t.Fatal("merged metadata aliases bundled slices")
	}
}

func TestMergeCodexModelMetadataAddsOnlyMeaningfulOverlayFields(t *testing.T) {
	base := cpa.ModelInfo{ID: "gpt-test", ContextLength: 100, Thinking: cpa.ModelThinkingInfo{Levels: []string{"low"}}}
	overlay := cpa.ModelInfo{
		ID:                       "gpt-test",
		DisplayName:              "Remote display",
		Description:              "Remote description",
		ContextLength:            200,
		MaxCompletionTokens:      1000,
		Thinking:                 cpa.ModelThinkingInfo{Levels: []string{"medium", "low"}},
		SupportedInputModalities: []string{"image", "audio"},
	}

	got := mergeCodexModelMetadata(base, overlay)
	if got.DisplayName != "Remote display" || got.Description != "Remote description" {
		t.Fatalf("text fields were not filled: %#v", got)
	}
	if got.ContextLength != 200 || got.MaxCompletionTokens != 1000 {
		t.Fatalf("numeric fields were not merged: %#v", got)
	}
	if want := []string{"low", "medium"}; !reflect.DeepEqual(got.Thinking.Levels, want) {
		t.Fatalf("reasoning levels = %v, want %v", got.Thinking.Levels, want)
	}
	if want := []string{"image", "audio"}; !reflect.DeepEqual(got.SupportedInputModalities, want) {
		t.Fatalf("modalities = %v, want %v", got.SupportedInputModalities, want)
	}
}

func TestCodexModelInfoEmitsCurrentNullableModelMessagesContract(t *testing.T) {
	model := newCodexModelInfo("custom-model", cpa.ModelInfo{ID: "custom-model"}, true, 1)
	encoded, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	base, present := object["base_instructions"]
	if !present || string(base) != `""` {
		t.Fatalf("base_instructions = %s, want explicit empty legacy template", base)
	}
	var messages map[string]json.RawMessage
	if err := json.Unmarshal(object["model_messages"], &messages); err != nil {
		t.Fatalf("model_messages is not an object: %v", err)
	}
	for _, key := range []string{
		"instructions_template", "instructions_variables", "approvals",
		"collaboration_modes", "auto_review", "permissions", "multi_agent",
		"guardian_v2", "confirmation_policies",
	} {
		if _, present := messages[key]; !present {
			t.Errorf("model_messages is missing %q: %s", key, object["model_messages"])
		}
	}
	if got := string(messages["instructions_template"]); got != `""` {
		t.Errorf("instructions_template = %s, want explicit empty template", got)
	}
	for _, key := range []string{"guardian_v2", "confirmation_policies"} {
		if got := string(messages[key]); got != "null" {
			t.Errorf("model_messages.%s = %s, want null", key, got)
		}
	}
	if got, present := object["multi_agent_reasoning_effort"]; !present || string(got) != "null" {
		t.Errorf("multi_agent_reasoning_effort = %s (present=%v), want null", got, present)
	}
}

func TestCodexModelsETagIsStableAcrossClientVersions(t *testing.T) {
	body := []byte(`{"models":[]}`)
	first := codexModelsETag("0.144.0", body)
	second := codexModelsETag("0.145.0", body)
	if first == "" || first != second {
		t.Fatalf("etag is not stable: %q vs %q", first, second)
	}
	if first[0] != '"' || first[len(first)-1] != '"' {
		t.Fatalf("etag is not a quoted entity tag: %q", first)
	}
	if first == codexModelsETag("0.144.0", []byte(`{"models":[{}]}`)) {
		t.Fatal("different response bodies must have different etags")
	}
}

func TestCodexIfNoneMatchSupportsWeakAndWildcardTags(t *testing.T) {
	const current = `"catalog"`
	for _, test := range []struct {
		header string
		want   bool
	}{
		{header: `W/"catalog"`, want: true},
		{header: `"other", W/"catalog"`, want: true},
		{header: `*`, want: true},
		{header: `"other"`, want: false},
	} {
		if got := codexIfNoneMatch(test.header, current); got != test.want {
			t.Errorf("codexIfNoneMatch(%q) = %v, want %v", test.header, got, test.want)
		}
	}
}

func TestHandleCodexModelsSetsBothCatalogValidators(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://upstream"))
	engine := gin.New()
	engine.GET("/codex/v1/models", func(c *gin.Context) {
		c.Set(middleware.CtxKeyKeyInfo, testKeyInfo())
		env.pipe.HandleCodexModels(c)
	})

	firstRequest := httptest.NewRequest(http.MethodGet, "/codex/v1/models?client_version=0.144.0", nil)
	first := httptest.NewRecorder()
	engine.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d; body = %s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response is missing ETag")
	}
	if got := first.Header().Get("X-Models-Etag"); got != etag {
		t.Fatalf("X-Models-Etag = %q, want %q", got, etag)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/codex/v1/models?client_version=0.144.0", nil)
	secondRequest.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	engine.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d; body = %s", second.Code, second.Body.String())
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 response unexpectedly has a body: %q", second.Body.String())
	}
	if got := second.Header().Get("X-Models-Etag"); got != etag {
		t.Fatalf("304 X-Models-Etag = %q, want %q", got, etag)
	}
}

func TestHandleModelsWithClientVersionUsesCodexShape(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://upstream"))
	req := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.144.0", nil)
	req.Header.Set("Originator", "codex_cli_rs")
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, ok := body["models"]; !ok {
		t.Fatalf("Codex response missing models: %s", w.Body.String())
	}
	if _, ok := body["data"]; ok {
		t.Fatalf("Codex response unexpectedly used OpenAI data shape: %s", w.Body.String())
	}
	var catalog codexModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode Codex response: %v", err)
	}
	if len(catalog.Models) == 0 || catalog.Models[0].BaseInstructions != "" {
		t.Fatalf("unexpected Codex catalog: %+v", catalog.Models)
	}
}

func TestClientVersionOnlyDoesNotIdentifySharedCodexModelsCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)

	modelsContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	modelsContext.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.144.0", nil)
	if isCodexModelRequest(modelsContext) {
		t.Fatal("client_version alone must not select the Codex catalog")
	}
	if isCodexClientRequest(modelsContext) {
		t.Fatal("models-only client_version must not become a general Codex client identity")
	}

	responsesContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	responsesContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?client_version=1.2.3", nil)
	if isCodexClientRequest(responsesContext) {
		t.Fatal("client_version alone misclassified an ordinary Responses request as Codex")
	}
	if got := relayClientType(responsesContext); got != "" {
		t.Fatalf("relayClientType = %q, want empty for an ordinary Responses request", got)
	}
	p := &Pipeline{codexTransportPolicy: staticCodexPolicy("native_only")}
	opts := p.codexResponsesForwardOptions(responsesContext)
	if opts.preferNativeCodex || opts.nativeCodexAccountsOnly {
		t.Fatalf("client_version alone unexpectedly enabled native Codex routing: %+v", opts)
	}
}

func TestIsCodexModelRequestRecognizesBackendAPIPath(t *testing.T) {
	for _, path := range []string{
		"/backend-api/codex/models",
		"/backend-api/codex/v1/models",
		"/api/codex/models",
		"/api/codex/v1/models",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		if !isCodexModelRequest(c) {
			t.Errorf("path %q was not recognized as a Codex route", path)
		}
	}
	for _, path := range []string{"/backend-api/codex/", "/api/codex/", "/codex/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		if isCodexModelRequest(c) {
			t.Errorf("bare namespace %q was incorrectly treated as a models request", path)
		}
	}
}

func TestHandleModelsWithInvalidClientVersionKeepsOpenAIShape(t *testing.T) {
	env := newTestEnv(t, testSnap(1, "http://upstream"))
	for _, version := range []string{"", "latest", "0.144", "v0.144.0", "0.144.0-beta"} {
		t.Run(version, func(t *testing.T) {
			path := "/v1/models"
			if version != "" {
				path += "?client_version=" + version
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			env.engine.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if _, ok := body["data"]; !ok {
				t.Fatalf("OpenAI response missing data: %s", w.Body.String())
			}
			if _, ok := body["models"]; ok {
				t.Fatalf("invalid client_version selected Codex shape: %s", w.Body.String())
			}
		})
	}
}

func TestHandleModelsRecognizesCodexHeadersWithInvalidQuery(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "originator", header: "Originator", value: "codex_cli_rs"},
		{name: "user agent", header: "User-Agent", value: "codex_cli_rs/0.144.0 (Windows; x86_64) rust"},
		{name: "openai client user agent", header: "X-OpenAI-Client-User-Agent", value: "codex_cli_rs/0.144.0"},
		{name: "codex fingerprint", header: "X-Codex-Window-Id", value: "window-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnv(t, testSnap(1, "http://upstream"))
			req := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=latest", nil)
			req.Header.Set(test.header, test.value)
			w := httptest.NewRecorder()
			env.engine.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if _, ok := body["models"]; !ok {
				t.Fatalf("Codex response missing models: %s", w.Body.String())
			}
			if _, ok := body["data"]; ok {
				t.Fatalf("Codex response unexpectedly used OpenAI data shape: %s", w.Body.String())
			}
		})
	}
}

func TestIsWholeClientVersion(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "0.144.0", want: true},
		{value: " 1.2.3 ", want: true},
		{value: "", want: false},
		{value: "1.2", want: false},
		{value: "1.2.3-beta", want: false},
		{value: "v1.2.3", want: false},
		{value: "1.2.3.4", want: false},
	} {
		if got := isWholeClientVersion(test.value); got != test.want {
			t.Errorf("isWholeClientVersion(%q)=%v want %v", test.value, got, test.want)
		}
	}
}
