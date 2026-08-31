package pipeline

import (
	"context"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/relayhook"
)

// transformSelectedCodexAttempt applies a provider-attempt Responses
// enhancement after Core has selected one concrete account. The identity gate
// is the inbound Codex client, not the selected account platform: a Codex CLI
// request may legitimately land on an OpenAI/XAI CPA account and must retain
// the same client-scoped enhancement contract. The plugin itself decides
// which account-dependent features (for example OAuth-only overage history)
// are valid for that account. Always return a new replacement slice and never
// mutate payload, preserving a clean body for later failover targets.
func (p *Pipeline) transformSelectedCodexAttempt(
	ctx context.Context,
	c *gin.Context,
	acc *accountreg.Snapshot,
	req *dto.ChatRequest,
	endpoint, entryProtocol string,
	payload []byte,
	opts forwardOptions,
	requestID string,
) []byte {
	if p == nil || acc == nil || req == nil || len(payload) == 0 || opts.rawBody != nil ||
		!strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointResponses) ||
		!isCodexClientRequest(c) {
		return payload
	}
	transformer, ok := p.relayHook.(relayhook.ProviderAttemptTransformer)
	if !ok {
		return payload
	}
	client := relayClientType(c)
	decision, err := transformer.TransformProviderAttempt(ctx, relayhook.ProviderAttemptRequest{
		Version:   relayhook.VersionV1,
		RequestID: requestID,
		GroupID:   groupIDFromContext(c),
		Client:    client,
		Endpoint:  endpoint,
		Protocol:  entryProtocol,
		Model:     req.Model,
		Stream:    req.Stream,
		Body:      append([]byte(nil), payload...),
		Account: relayhook.Candidate{
			Kind: "account", ID: acc.ID, Name: acc.Name,
			Platform: acc.Platform, Type: acc.Type, State: acc.State,
		},
	})
	if err != nil {
		slog.Warn("selected Codex account transform failed open", "account_id", acc.ID, "endpoint", endpoint, "error", err)
		return payload
	}
	if decision.Version != relayhook.VersionV1 || len(decision.RequestBody) == 0 || len(decision.RequestBody) > maxRequestBodyBytes {
		return payload
	}
	replaced, err := dto.ParseChatRequest(decision.RequestBody)
	if err != nil || replaced.Model != req.Model || replaced.Stream != req.Stream {
		return payload
	}
	return append([]byte(nil), decision.RequestBody...)
}
