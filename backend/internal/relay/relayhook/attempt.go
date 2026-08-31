package relayhook

import (
	"context"
	"encoding/json"
)

const (
	// ProviderAttemptPath is the request-body transform invoked after Core has
	// selected one concrete account. Unlike BeforeDispatch, it cannot alter
	// routing and its replacement body is scoped to that single attempt.
	ProviderAttemptPath = "/relay-hook/v1/provider-attempt"
)

// ProviderAttemptTransformer rewrites a provider request after account
// selection. The transformer receives only non-secret account metadata; Core
// retains credentials, proxy details, and provider URLs inside the transport
// boundary.
type ProviderAttemptTransformer interface {
	TransformProviderAttempt(context.Context, ProviderAttemptRequest) (ProviderAttemptDecision, error)
}

// ProviderAttemptRequest describes one selected-account attempt. Body starts
// from the request produced by the inbound Relay Hook, so transforms can add
// account-specific state without mutating the shared failover payload.
type ProviderAttemptRequest struct {
	Version   string          `json:"version"`
	RequestID string          `json:"request_id,omitempty"`
	GroupID   int             `json:"group_id"`
	Client    string          `json:"client,omitempty"`
	Endpoint  string          `json:"endpoint"`
	Protocol  string          `json:"protocol"`
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	Body      json.RawMessage `json:"body"`
	Account   Candidate       `json:"account"`
}

// ProviderAttemptDecision can replace only the current attempt body. Route
// plans are intentionally absent because account selection has already
// completed.
type ProviderAttemptDecision struct {
	Version     string          `json:"version"`
	RequestBody json.RawMessage `json:"request_body,omitempty"`
}
