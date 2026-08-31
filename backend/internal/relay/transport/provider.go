// Package transport defines the provider data-plane contract used by relay
// pipelines.  Concrete implementations may execute a request natively (for
// example, the Codex transport) or translate it through CPA.
package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// ProviderTransport executes one provider request for an already selected
// account. Request and Result are Core-owned and deliberately do not expose
// CPA or HTTP framework types. A transport may use LegacyForwarder internally
// while implementations migrate to this contract.
type ProviderTransport interface {
	Execute(context.Context, Request) Result
}

// AccountEligibility is an optional guard for native transports that own only
// a subset of provider account families. The pipeline consults it before
// Execute and leaves unmatched accounts on the CPA translation plane.
// Transports that do not implement this interface retain the historical
// execute-first behavior.
type AccountEligibility interface {
	SupportsAccount(Account) bool
}

// UpstreamAuditSink lets an out-of-process native transport create an audit
// row immediately before its real provider HTTP request. The event contains
// only a controlled, credential-free request snapshot.
type UpstreamAuditSink interface {
	BeginUpstreamAttempt(context.Context, UpstreamAuditRequest) (UpstreamAuditAttempt, error)
}

type UpstreamAuditAttempt interface {
	FinishUpstreamAttempt(UpstreamAuditResult)
}

type UpstreamAuditRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

type UpstreamAuditResult struct {
	StatusCode      int
	RetryAfter      string
	LatencyMs       int64
	FirstTokenMs    int64
	ResponseStarted bool
	StreamCompleted bool
	NetworkError    bool
	ErrorCode       string
}

// LegacyForwarder is the source-compatible bridge used by the existing relay
// pipeline. It is kept separate from ProviderTransport so new transports do
// not depend on CPA or gin.Context.
type LegacyForwarder interface {
	Forward(context.Context, *gin.Context, cpa.ForwardRequest) cpa.ForwardResult
}

// Account contains the selected account's provider-facing credentials.
type Account struct {
	ID          int
	Name        string
	Platform    string
	Type        string
	Credentials map[string]string
	ProxyURL    string
}

// Request is the Core-owned provider request envelope.
type Request struct {
	Account   Account
	RequestID string
	// Client is the normalized inbound client type (for example "codex"). It
	// is metadata only; account eligibility remains based on Account.Platform.
	Client        string
	GroupID       int
	Method        string
	BaseURL       string
	Path          string
	Query         map[string][]string
	Transport     string
	Model         string
	UpstreamModel string
	Endpoint      string
	EntryProtocol string
	Stream        bool
	Payload       []byte
	// RawBody is non-nil only for byte-oriented native contracts (for example
	// Realtime SDP or multipart file operations). Payload is still the effective
	// wire payload (and therefore equals RawBody for those requests); keeping the
	// presence bit prevents the CPA bridge from mistaking every JSON request for
	// a raw passthrough.
	RawBody          []byte
	RawContentType   string
	Headers          http.Header
	RequestStartedAt time.Time
	CursorSessionKey string
	// UpstreamAudit is optional. Native transports running outside Core use it
	// to retain the invariant that an audit skeleton exists before touching the
	// provider network. In-process CPA transports keep using their RoundTripper.
	UpstreamAudit UpstreamAuditSink
	// LegacyContext is an opaque framework context used only by the CPA bridge
	// during migration. Native transports should ignore it.
	LegacyContext any
	// Remote Control metadata is kept separate from ordinary provider headers.
	// The native Codex executor uses the server token only for the enrolled
	// pair/status/websocket contracts and applies the protocol headers itself.
	RemoteControlToken           string
	RemoteControlServerID        string
	RemoteControlName            string
	RemoteControlProtocolVersion string
	InstallationID               string
	// Optional Remote Control websocket metadata is kept separate from the
	// caller header projection.  The executor applies these fields explicitly
	// so a generic X-Codex-* header cannot override the protocol values.
	RemoteControlHostDeviceKind  string
	RemoteControlSubscribeCursor string
}

// Result is the Core-owned provider result envelope.
type Result struct {
	StatusCode int
	Headers    http.Header
	// ResponseStarted records that the provider transport observed the
	// upstream response boundary (normally a response_headers event), even
	// when the provider supplied an empty status/header set.  It is distinct
	// from DataReceived/Written: once this boundary is crossed, replaying the
	// request through CPA can duplicate a provider-side side effect.
	ResponseStarted     bool
	Body                []byte
	ContentType         string
	Usage               *dto.Usage
	FirstTokenMs        int64
	RequestFirstTokenMs int64
	ExecutorBootstrapMs int64
	Written             bool
	// DataReceived records whether the provider emitted any data event. It is
	// independent of Written: non-2xx streaming bodies are buffered so Core can
	// classify them without committing downstream headers.
	DataReceived         bool
	StreamErr            error
	Done                 bool
	NetErr               error
	BuildErr             error
	RefreshedCredentials map[string]string
}

// Capabilities describes optional protocol support exposed by a transport.
// It is deliberately separate from ProviderTransport so existing test doubles
// and third-party forwarders continue to satisfy the minimal contract.
type Capabilities struct {
	HTTP        bool
	Streaming   bool
	WebSocket   bool
	Translation bool
}

// CapabilityProvider is implemented by transports that can report supported
// wire modes.  Callers should treat an absent implementation as unknown (and
// preserve the historical behavior).
type CapabilityProvider interface {
	Capabilities() Capabilities
}

// CodexTransportPolicy is an optional plugin-level routing policy. The same
// configured policy applies to every Codex account; account credentials carry
// authentication and identity only.
type CodexTransportPolicy interface {
	CodexTransportMode() string
}

// CPAAdapter exposes an existing CPA forwarder as a ProviderTransport.  This
// adapter is intentionally thin: CPA remains responsible for protocol
// translation, OAuth refresh and stream framing, while Core owns scheduling,
// accounting and persistence.
type CPAAdapter struct {
	backend LegacyForwarder
}

var (
	_ ProviderTransport = (*CPAAdapter)(nil)
	_ LegacyForwarder   = (*CPAAdapter)(nil)
)

// NewCPAAdapter wraps backend.  A nil backend yields nil, which preserves the
// current "CPA unavailable" behavior in the pipeline.
func NewCPAAdapter(backend LegacyForwarder) ProviderTransport {
	if backend == nil {
		return nil
	}
	if alreadyAdapted, ok := backend.(*CPAAdapter); ok {
		return alreadyAdapted
	}
	return &CPAAdapter{backend: backend}
}

func (a *CPAAdapter) Forward(ctx context.Context, c *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	if a == nil || a.backend == nil {
		return cpa.ForwardResult{BuildErr: errCPAAdapterUnavailable{}}
	}
	return a.backend.Forward(ctx, c, req)
}

// Execute maps the Core-owned envelope to the legacy CPA request/result. The
// opaque LegacyContext is expected to contain *gin.Context for streaming
// requests; non-streaming callers may leave it nil.
func (a *CPAAdapter) Execute(ctx context.Context, req Request) Result {
	var c *gin.Context
	if req.LegacyContext != nil {
		c, _ = req.LegacyContext.(*gin.Context)
	}
	legacy := cpa.ForwardRequest{
		Account: cpa.AccountAuthInput{AccountID: req.Account.ID, Name: req.Account.Name, Platform: req.Account.Platform, Type: req.Account.Type, Credentials: req.Account.Credentials, ProxyURL: req.Account.ProxyURL},
		Model:   req.Model, UpstreamModel: req.UpstreamModel, Endpoint: req.Endpoint,
		Method: req.Method, Path: req.Path, Query: req.Query, EntryProtocol: req.EntryProtocol,
		Stream: req.Stream, Payload: req.Payload, RawBody: req.RawBody, RawContentType: req.RawContentType,
		Headers: req.Headers, RequestStartedAt: req.RequestStartedAt, CursorSessionKey: req.CursorSessionKey,
	}
	return fromCPAResult(a.Forward(ctx, c, legacy))
}

func fromCPAResult(in cpa.ForwardResult) Result {
	return Result{StatusCode: in.StatusCode, Headers: in.Headers, ResponseStarted: in.StatusCode != 0 || len(in.Headers) > 0 || in.DataReceived || in.Written, Body: in.Body, ContentType: in.ContentType, Usage: in.Usage,
		FirstTokenMs: in.FirstTokenMs, RequestFirstTokenMs: in.RequestFirstTokenMs, ExecutorBootstrapMs: in.ExecutorBootstrapMs,
		Written: in.Written, DataReceived: in.DataReceived, StreamErr: in.StreamErr, Done: in.Done, NetErr: in.NetErr, BuildErr: in.BuildErr, RefreshedCredentials: in.RefreshedCredentials}
}

// Capabilities marks CPA as an HTTP/SSE translation backend.  WebSocket
// support is intentionally false until a dedicated duplex adapter exists.
func (a *CPAAdapter) Capabilities() Capabilities {
	return Capabilities{HTTP: true, Streaming: true, Translation: true}
}

type errCPAAdapterUnavailable struct{}

func (errCPAAdapterUnavailable) Error() string { return "CPA provider transport unavailable" }
