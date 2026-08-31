package cpa

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// ErrCodexTranslatorUnavailable indicates that a Codex CPA request cannot be
// translated by the installed CPA registry.  The SDK translator deliberately
// returns the input payload when a route is missing; that fallback is useful
// for generic callers but unsafe for a Codex account because it would send
// Anthropic/Gemini/Chat payloads to the Responses endpoint unchanged.
var ErrCodexTranslatorUnavailable = errors.New("codex CPA translator unavailable")

// ErrCodexEndpointUnsupported indicates that the CPA bridge does not have a
// wire contract for the requested Codex endpoint.  Keep this check at the
// Bridge boundary as well as in Pipeline: account tests, embedders and legacy
// callers can invoke Bridge.Forward directly and must not accidentally send a
// non-Responses endpoint through the generic Gemini/OpenAI translator.
var ErrCodexEndpointUnsupported = errors.New("codex CPA endpoint unsupported")

// validateCodexCPAContract fails closed for schema-translated Codex requests.
// OpenAI Images are intentionally excluded: the CPA Codex executor has a
// dedicated multipart/Images adapter and does not use the generic translator
// registry for that wire contract.
func validateCodexCPAContract(provider, endpoint, entryProtocol string, stream bool) error {
	if ResolveProvider(provider) != "codex" {
		return nil
	}
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	if endpoint == adaptor.EndpointImagesGenerations || endpoint == adaptor.EndpointImagesEdits {
		return nil
	}
	// Compact, alpha_search and Gemini Imagen predict have distinct upstream
	// contracts (or no Codex equivalent) and must never be represented as a
	// normal Responses translation.  This also rejects unknown endpoints for
	// direct Bridge callers, where Pipeline's endpoint gate is not involved.
	switch endpoint {
	case adaptor.EndpointChatCompletions, adaptor.EndpointResponses,
		adaptor.EndpointMessages, adaptor.EndpointMessagesCountTokens,
		adaptor.EndpointGenerateContent, adaptor.EndpointCountTokens:
		// continue with the schema/transformer checks below
	default:
		if endpoint == adaptor.EndpointCompact {
			return ErrCompactUnsupported
		}
		return fmt.Errorf("%w: %s", ErrCodexEndpointUnsupported, endpoint)
	}

	source := sourceFormatFor(endpoint, entryProtocol)
	return validateCodexCPAFormat(endpoint, source, stream)
}

func validateCodexCPAFormat(endpoint string, source sdktranslator.Format, stream bool) error {
	target := sdktranslator.FormatCodex
	if !sdktranslator.HasRequestTransformer(source, target) {
		return fmt.Errorf("%w: request %s -> %s", ErrCodexTranslatorUnavailable, source, target)
	}
	// Count-token responses use the token-count transform rather than a normal
	// stream/non-stream response transform.  The request transform above is the
	// critical safety check for that endpoint.
	if endpoint == adaptor.EndpointMessagesCountTokens || endpoint == adaptor.EndpointCountTokens {
		return nil
	}
	// CPA registers the response half under the same source/target pair as its
	// request translator, even though execution translates the response in the
	// reverse wire direction (Codex upstream -> entry protocol).
	if stream {
		if !sdktranslator.HasStreamResponseTransformer(source, target) {
			return fmt.Errorf("%w: stream response %s -> %s", ErrCodexTranslatorUnavailable, target, source)
		}
	} else if !sdktranslator.HasNonStreamResponseTransformer(source, target) {
		return fmt.Errorf("%w: response %s -> %s", ErrCodexTranslatorUnavailable, target, source)
	}
	return nil
}
