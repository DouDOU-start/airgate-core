package cpa

import (
	"errors"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestValidateCodexCPAContractTextProtocols(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		protocol string
		stream   bool
	}{
		{"chat completions", adaptor.EndpointChatCompletions, "openai", false},
		{"chat completions stream", adaptor.EndpointChatCompletions, "openai", true},
		{"responses", adaptor.EndpointResponses, "openai", false},
		{"responses stream", adaptor.EndpointResponses, "openai", true},
		{"anthropic", adaptor.EndpointMessages, "anthropic", false},
		{"anthropic stream", adaptor.EndpointMessages, "anthropic", true},
		{"gemini", adaptor.EndpointGenerateContent, "gemini", false},
		{"gemini stream", adaptor.EndpointGenerateContent, "gemini", true},
		{"anthropic count tokens", adaptor.EndpointMessagesCountTokens, "anthropic", false},
		{"gemini count tokens", adaptor.EndpointCountTokens, "gemini", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCodexCPAContract("codex", tt.endpoint, tt.protocol, tt.stream); err != nil {
				t.Fatalf("Codex CPA contract rejected: %v", err)
			}
		})
	}
}

func TestValidateCodexCPAContractImagesUseDedicatedExecutor(t *testing.T) {
	for _, endpoint := range []string{adaptor.EndpointImagesGenerations, adaptor.EndpointImagesEdits} {
		if err := validateCodexCPAContract("codex", endpoint, "openai", false); err != nil {
			t.Fatalf("image endpoint %q must use dedicated Codex image executor: %v", endpoint, err)
		}
	}
}

func TestValidateCodexCPAContractRejectsNonTextCodexEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		adaptor.EndpointCompact,
		adaptor.EndpointAlphaSearch,
		adaptor.EndpointPredict,
		"unknown_endpoint",
	} {
		t.Run(endpoint, func(t *testing.T) {
			err := validateCodexCPAContract("codex", endpoint, "openai", false)
			if endpoint == adaptor.EndpointCompact {
				if !errors.Is(err, ErrCompactUnsupported) {
					t.Fatalf("compact error = %v, want ErrCompactUnsupported", err)
				}
				return
			}
			if !errors.Is(err, ErrCodexEndpointUnsupported) {
				t.Fatalf("endpoint %q error = %v, want ErrCodexEndpointUnsupported", endpoint, err)
			}
		})
	}
}

func TestValidateCodexCPAContractNonCodexProviderUnchanged(t *testing.T) {
	if err := validateCodexCPAContract("openai", adaptor.EndpointChatCompletions, "openai", false); err != nil {
		t.Fatalf("non-Codex CPA provider should not be gated by Codex registry: %v", err)
	}
}

func TestValidateCodexCPAFormatFailsClosedWhenTranslatorIsMissing(t *testing.T) {
	err := validateCodexCPAFormat(adaptor.EndpointMessages, sdktranslator.FromString("missing-codex-source"), false)
	if !errors.Is(err, ErrCodexTranslatorUnavailable) {
		t.Fatalf("missing translator error = %v, want ErrCodexTranslatorUnavailable", err)
	}
}
