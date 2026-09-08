package cpa_test

import (
	"context"
	"encoding/json"
	"testing"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"

	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
)

// TestCodexCPARequestContracts exercises the actual SDK registry rather than
// only checking dispatch metadata.  A Codex account used in cpa_translate
// mode must be able to accept every text protocol that AirGate exposes and
// produce a real Codex request body.
func TestCodexCPARequestContracts(t *testing.T) {
	model := "gpt-5-codex"
	tests := []struct {
		name    string
		from    sdktranslator.Format
		payload string
	}{
		{
			name:    "OpenAI Responses",
			from:    sdktranslator.FormatOpenAIResponse,
			payload: `{"model":"gpt-5-codex","input":"hello","stream":false}`,
		},
		{
			name:    "OpenAI Chat Completions",
			from:    sdktranslator.FormatOpenAI,
			payload: `{"model":"gpt-5-codex","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name:    "Anthropic Messages",
			from:    sdktranslator.FormatClaude,
			payload: `{"model":"gpt-5-codex","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name:    "Gemini Generate Content",
			from:    sdktranslator.FormatGemini,
			payload: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !sdktranslator.HasRequestTransformer(tt.from, sdktranslator.FormatCodex) {
				t.Fatalf("missing request transformer %q -> codex", tt.from)
			}
			// Response transforms are registered on the same source/target pair as
			// their request transform, even though execution runs Codex -> source.
			if !sdktranslator.HasNonStreamResponseTransformer(tt.from, sdktranslator.FormatCodex) {
				t.Fatalf("missing non-stream response transformer codex -> %q", tt.from)
			}
			if !sdktranslator.HasStreamResponseTransformer(tt.from, sdktranslator.FormatCodex) {
				t.Fatalf("missing stream response transformer codex -> %q", tt.from)
			}

			translated := sdktranslator.TranslateRequest(tt.from, sdktranslator.FormatCodex, model, []byte(tt.payload), false)
			var request map[string]any
			if err := json.Unmarshal(translated, &request); err != nil {
				t.Fatalf("translated request is not JSON: %v; body=%s", err, translated)
			}
			if request["model"] != model {
				t.Fatalf("translated request model=%v, want %q; body=%s", request["model"], model, translated)
			}
			if _, ok := request["input"]; !ok {
				t.Fatalf("translated request has no Codex input field: %s", translated)
			}
			// `instructions` is optional in the Codex Responses contract.  The
			// OpenAI Responses translator omits it when the source request has no
			// system/developer instruction; requiring it here would reject a valid
			// request shape.

			terminal := []byte(`{"type":"response.completed","response":{"id":"resp_contract","object":"response","model":"gpt-5-codex","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`)
			translatedResponse := sdktranslator.TranslateNonStream(context.Background(), sdktranslator.FormatCodex, tt.from, model, []byte(tt.payload), translated, terminal, nil)
			var response map[string]any
			if err := json.Unmarshal(translatedResponse, &response); err != nil {
				t.Fatalf("translated response is not JSON: %v; body=%s", err, translatedResponse)
			}
			if len(response) == 0 {
				t.Fatalf("translated response is empty for %q", tt.from)
			}
			// Each target protocol names the usage object differently (Gemini uses
			// usageMetadata, while OpenAI/Claude use usage).  Check the protocol
			// contract without assuming one provider's field spelling globally.
			if _, usage := response["usage"]; !usage {
				if _, usageMetadata := response["usageMetadata"]; !usageMetadata {
					t.Fatalf("translated response lost usage for %q: %s", tt.from, translatedResponse)
				}
			}
		})
	}
}

// Predict is a Gemini Imagen endpoint, not a text protocol accepted by the
// Codex Responses translator.  Keeping it out of the CPA contract is
// important: the generic Gemini->Codex converter would otherwise silently
// turn an Imagen request into a text Responses request.
func TestCodexCPATranslationContractRejectsGeminiPredict(t *testing.T) {
	if providertransport.CodexCPATranslationContract("predict") {
		t.Fatal("Gemini predict must not be advertised as a Codex CPA contract")
	}
}
