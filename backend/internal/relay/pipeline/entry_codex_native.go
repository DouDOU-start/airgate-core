package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// HandleRealtimeCalls relays the official Codex WebRTC call bootstrap wire.
// The body may be raw SDP, backend JSON, or multipart SDP+session JSON.
func (p *Pipeline) HandleRealtimeCalls(c *gin.Context) {
	p.handleRealtimeCallCreate(c, "/realtime/calls")
}

// HandleRealtimeLive relays the Frameless call-create shape. It shares the
// realtime_calls endpoint contract but must retain the provider /live path.
func (p *Pipeline) HandleRealtimeLive(c *gin.Context) {
	p.handleRealtimeCallCreate(c, "/live")
}

func (p *Pipeline) handleRealtimeCallCreate(c *gin.Context, providerPath string) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}

	req, err := dto.ParseChatRequest([]byte("{}"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "failed to initialize realtime request")
		return
	}
	req.Model = strings.TrimSpace(realtimeCallModel(body, c.GetHeader("Content-Type")))
	// A raw SDP offer carries no model metadata. The official Codex client
	// already has a native account/ephemeral-token contract for that shape, but
	// the shared OpenAI API-key route cannot infer a billable model safely. Do
	// not let an empty model reach the ordinary pricing/routing path; require a
	// model-bearing JSON or multipart session instead. Codex requests are
	// intentionally exempt because they are native-only and zero-billing.
	if req.Model == "" && !isCodexClientRequest(c) {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "missing model field")
		return
	}
	req.Stream = false
	contentType := c.GetHeader("Content-Type")
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointRealtimeCalls,
		realtimeCallForwardOptions(c, body, contentType, providerPath))
}

func realtimeCallForwardOptions(c *gin.Context, body []byte, contentType, providerPath string) forwardOptions {
	opts := forwardOptions{
		rawBody:             body,
		rawContentType:      contentType,
		providerPath:        providerPath,
		passthroughResponse: true,
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		opts.providerQueryDefaults = realtimeCallProviderQueryDefaults(body, contentType, c.Request.URL.Query())
	}
	if isCodexClientRequest(c) {
		// Explicit Codex aliases and requests carrying an official Codex client
		// identity use the native executor and canonical Codex accounts only.
		opts.nativeCodexAccountsOnly = true
		// The Codex call bootstrap is a control-plane exchange with no model
		// token usage. It is intentionally unpriced only after the request has
		// crossed the Codex client boundary; a normal OpenAI caller on the
		// shared route must retain the ordinary model-price and billing gates.
		opts.allowUnpriced = true
		opts.zeroBilling = true
	} else {
		// /v1/realtime/calls and /v1/live are shared OpenAI endpoints. Ordinary
		// callers keep the normal OpenAI routing protocol so existing account/CPA
		// and channel selection remains available; the Codex executor is still
		// unable to claim a non-Codex account at the transport boundary.
		opts.channelRoutingProtocol = registry.ProtocolOpenAI
	}
	return opts
}

// HandleMemoriesTraceSummarize relays Codex's unary memory-generation wire
// without treating it as a Responses request or CPA translation.
func (p *Pipeline) HandleMemoriesTraceSummarize(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, "memories") {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "request body must be a JSON object")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "missing model field")
		return
	}
	req.Stream = false
	p.forwardOpt(c, keyInfo, req, adaptor.EndpointMemoriesTraceSummarize, forwardOptions{
		rawBody: body, rawContentType: c.GetHeader("Content-Type"),
		nativeCodexAccountsOnly: true, allowUnpriced: true,
		zeroBilling: true, passthroughResponse: true,
	})
}

// HandleCodexGuardian forwards the official Codex approval-review Responses
// contract to the native executor. Guardian is a ChatGPT backend control
// plane (not a generic CPA translation endpoint), so the request body and
// response stream remain untouched just like the native Responses route.
func (p *Pipeline) HandleCodexGuardian(c *gin.Context) {
	p.handleCodexGuardianEndpoint(c, adaptor.EndpointGuardian)
}

// HandleCodexGuardianClassifier forwards the official asynchronous Guardian
// risk-classifier Responses contract to the native executor.
func (p *Pipeline) HandleCodexGuardianClassifier(c *gin.Context) {
	p.handleCodexGuardianEndpoint(c, adaptor.EndpointGuardianClassifier)
}

// codexRawControlSpec describes one raw JSON POST exposed by the optional
// official Codex History/Notes extension or analytics client. Keeping this
// table explicit is intentional: these endpoints are not a generic proxy
// catch-all and must never be redirected into CPA translation.
type codexRawControlSpec struct {
	Endpoint  string
	Path      string
	Encrypted bool
	// OAuthOnly marks private Codex-backend extension calls that the official
	// client exposes only when its auth uses the ChatGPT/Codex backend. Direct
	// API-key accounts must not be selected for these routes.
	OAuthOnly bool
}

var codexRawControlSpecs = []codexRawControlSpec{
	{Endpoint: adaptor.EndpointHistoryListWindows, Path: "/alpha/history/v2/list_windows", OAuthOnly: true},
	{Endpoint: adaptor.EndpointHistoryListItems, Path: "/alpha/history/v2/list_items", OAuthOnly: true},
	{Endpoint: adaptor.EndpointHistoryReadItem, Path: "/alpha/history/v2/read_item", OAuthOnly: true},
	{Endpoint: adaptor.EndpointHistorySearchContents, Path: "/alpha/history/v2/search_contents", Encrypted: true, OAuthOnly: true},
	{Endpoint: adaptor.EndpointNotesListFilesByPrefix, Path: "/alpha/notes/v2/list_files_by_prefix", OAuthOnly: true},
	{Endpoint: adaptor.EndpointNotesReadFile, Path: "/alpha/notes/v2/read_file", OAuthOnly: true},
	{Endpoint: adaptor.EndpointNotesSearchContents, Path: "/alpha/notes/v2/search_contents", Encrypted: true, OAuthOnly: true},
	{Endpoint: adaptor.EndpointNotesAppendToFile, Path: "/alpha/notes/v2/append_to_file", Encrypted: true, OAuthOnly: true},
	{Endpoint: adaptor.EndpointNotesWriteFile, Path: "/alpha/notes/v2/write_file", Encrypted: true, OAuthOnly: true},
	{Endpoint: adaptor.EndpointNotesThreadHint, Path: "/alpha/notes/v2/thread_hint", OAuthOnly: true},
	{Endpoint: adaptor.EndpointAnalyticsEvents, Path: "/analytics-events/events"},
	{Endpoint: adaptor.EndpointCodexTurnCosts, Path: "/analytics/codex/turn-costs"},
}

// codexControlRoutePrefixes are the route families registered by server.go.
// They are checked again at the handler boundary so a future route refactor
// cannot accidentally turn this control-plane handler into an arbitrary path
// forwarder.
var codexControlRoutePrefixes = []string{
	// Keep the longest/versioned aliases explicit. The official CLI has used
	// both the backend model base and the backend-client/WHAM base, while
	// private reverse proxies may expose either spelling at their edge.
	"/backend-api/v1/wham",
	"/backend-api/wham/v1",
	"/backend-api/codex/v1",
	"/backend-api/wham",
	"/backend-api/codex",
	"/backend-api/v1",
	"/backend-api",
	"/api/codex/v1",
	"/api/codex",
	"/codex/v1",
	"/codex",
	"/wham/v1",
	"/wham",
	"/v1",
	"",
}

func codexRawControlSpecForPath(rawPath string) (codexRawControlSpec, bool) {
	path := strings.TrimRight(strings.TrimSpace(rawPath), "/")
	if path == "" {
		return codexRawControlSpec{}, false
	}
	for _, prefix := range codexControlRoutePrefixes {
		for _, spec := range codexRawControlSpecs {
			if path == prefix+spec.Path {
				return spec, true
			}
		}
	}
	return codexRawControlSpec{}, false
}

// HandleCodexControlPlane forwards the official History/Notes extension and
// analytics JSON contracts as-is. It deliberately does not require a model:
// these calls carry private history/notes arguments or an events envelope and
// are selected against any native Codex account in the caller's group.
func (p *Pipeline) HandleCodexControlPlane(c *gin.Context) {
	spec, ok := codexRawControlSpecForPath(c.Request.URL.Path)
	if !ok {
		setEntryProtocol(c, registry.ProtocolOpenAI)
		writeError(c, http.StatusNotFound, "invalid_request_error", "unsupported_endpoint", "unsupported Codex control-plane endpoint")
		return
	}
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, spec.Endpoint) {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	if c.Request.Method != http.MethodPost {
		writeError(c, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Codex control-plane endpoints require POST")
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	// The official backend client serializes a JSON object for every operation;
	// ParseChatRequest validates that shape while retaining the original bytes
	// for exact forwarding of unknown fields and nested encrypted arguments.
	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "request body must be a JSON object")
		return
	}
	req.Stream = false
	providerHeaders := codexControlProviderHeaderOverrides(spec)
	p.forwardOpt(c, keyInfo, req, spec.Endpoint, forwardOptions{
		rawBody: body, rawContentType: c.GetHeader("Content-Type"),
		providerPath: spec.Path, nativeCodexAccountsOnly: true,
		nativeCodexAPIKeyOnly: spec.Endpoint == adaptor.EndpointCodexTurnCosts,
		nativeCodexOAuthOnly:  spec.OAuthOnly,
		allowUnpriced:         true, zeroBilling: true, passthroughResponse: true,
		providerHeaders: providerHeaders,
	})
}

func codexControlProviderHeaderOverrides(spec codexRawControlSpec) http.Header {
	if !spec.Encrypted {
		return nil
	}
	// Search and write operations carry encrypted tool arguments in the
	// official client. Force the marker on the provider leg even when a caller
	// omitted it; credentials and all other safe protocol headers still come
	// from accountProviderHeadersForRequest.
	return http.Header{"X-OpenAI-Encrypted-Tool-Arguments": []string{"true"}}
}

func (p *Pipeline) handleCodexGuardianEndpoint(c *gin.Context, endpoint string) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, endpoint) {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "request body must be a JSON object")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "missing model field")
		return
	}
	// Official Guardian clients construct a Responses request with
	// service_tier=None, which serializes as an omitted field.  Remove an
	// inherited tier before the raw body reaches the native executor so a
	// parent turn's priority/flex setting cannot accidentally charge or route
	// the control-plane request.
	body, err = normalizeCodexGuardianRequestBody(req, body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "request body could not be normalized")
		return
	}
	// Guardian endpoints use the Responses SSE contract even when the caller
	// omits stream=true; the native executor decides the exact upstream framing
	// from the original request and response headers. Keep the parsed stream bit
	// for Core scheduling/billing, while always preserving the raw JSON body.
	p.forwardOpt(c, keyInfo, req, endpoint, forwardOptions{
		rawBody: body, rawContentType: c.GetHeader("Content-Type"),
		nativeCodexAccountsOnly: true, allowUnpriced: true,
		zeroBilling: true, passthroughResponse: true,
		providerHeaders: codexGuardianProviderHeaderOverrides(endpoint),
	})
}

func normalizeCodexGuardianRequestBody(req *dto.ChatRequest, original []byte) ([]byte, error) {
	if req == nil {
		return original, nil
	}
	if _, present := req.Get("service_tier"); !present {
		return original, nil
	}
	req.Remove("service_tier")
	return req.Marshal()
}

func codexGuardianProviderHeaderOverrides(endpoint string) http.Header {
	if endpoint != adaptor.EndpointGuardian && endpoint != adaptor.EndpointGuardianClassifier {
		return nil
	}
	// Guardian's backend route is selected by the endpoint itself.  Force the
	// subagent marker used by the official client and strip ordinary Responses
	// routing hints; classifier additionally opts into the lite contract.
	overrides := http.Header{
		"X-OpenAI-Subagent":    []string{"guardian"},
		"X-Codex-Routing-Hint": nil,
	}
	if endpoint == adaptor.EndpointGuardianClassifier {
		overrides.Set("X-OpenAI-Internal-Codex-Responses-Lite", "true")
	}
	return overrides
}

func realtimeCallModel(body []byte, contentType string) string {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.EqualFold(mediaType, "application/json"):
		return realtimeModelFromJSON(body)
	case strings.HasPrefix(strings.ToLower(mediaType), "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return ""
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				return ""
			}
			if err != nil {
				return ""
			}
			if part.FormName() != "session" || part.FileName() != "" {
				continue
			}
			session, err := io.ReadAll(io.LimitReader(part, 1<<20))
			if err != nil {
				return ""
			}
			return realtimeModelFromJSON(session)
		}
	default:
		return ""
	}
}

func realtimeModelFromJSON(body []byte) string {
	var value struct {
		Model   string `json:"model"`
		Session struct {
			Model string `json:"model"`
		} `json:"session"`
	}
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	if model := strings.TrimSpace(value.Model); model != "" {
		return model
	}
	return strings.TrimSpace(value.Session.Model)
}

// realtimeCallProviderQueryDefaults returns the query parameters that the
// official AVAS session-create client adds for a session-bearing call. A raw
// SDP offer has no AVAS session metadata and must be forwarded without these
// defaults. Frameless public /live sessions carry a delegation object and also
// intentionally omit the legacy quicksilver/avas pair; ChatGPT backend JSON
// sessions do not carry that marker and therefore use the AVAS defaults.
//
// Existing caller-provided values always win. The returned map only contains
// keys that are absent from the inbound query (including case-insensitive
// spellings), so the merge layer can safely append it to the provider query.
func realtimeCallProviderQueryDefaults(body []byte, contentType string, inboundQuery map[string][]string) map[string][]string {
	session, ok, backendJSON := realtimeCallSessionJSON(body, contentType)
	if !ok || (!backendJSON && !realtimeSessionUsesAVAS(session)) {
		return nil
	}
	defaults := map[string][]string{
		"intent":       {"quicksilver"},
		"architecture": {"avas"},
	}
	for key := range defaults {
		if providerQueryKeyPresent(inboundQuery, key) {
			delete(defaults, key)
		}
	}
	if len(defaults) == 0 {
		return nil
	}
	return defaults
}

func realtimeCallSessionJSON(body []byte, contentType string) ([]byte, bool, bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
		params = nil
	}
	switch {
	case strings.EqualFold(mediaType, "application/json") || mediaType == "":
		var envelope map[string]json.RawMessage
		if json.Unmarshal(body, &envelope) != nil {
			return nil, false, false
		}
		session, ok := envelope["session"]
		if !ok || len(session) == 0 || string(session) == "null" {
			return nil, false, false
		}
		// The JSON `{sdp, session}` envelope is the ChatGPT backend wire
		// shape. Official backend calls add AVAS query defaults even for a
		// Frameless Bidi session (which carries a `delegation` object).
		return append([]byte(nil), session...), true, true
	case strings.HasPrefix(strings.ToLower(mediaType), "multipart/"):
		boundary := params["boundary"]
		if strings.TrimSpace(boundary) == "" {
			return nil, false, false
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				return nil, false, false
			}
			if err != nil {
				return nil, false, false
			}
			if part.FormName() != "session" || part.FileName() != "" {
				continue
			}
			session, err := io.ReadAll(io.LimitReader(part, 1<<20))
			if err != nil {
				return nil, false, false
			}
			return session, true, false
		}
	default:
		return nil, false, false
	}
}

func realtimeSessionUsesAVAS(session []byte) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(session, &value) != nil {
		return false
	}
	// Frameless Bidi's public /live session is explicitly client-delegated and
	// must not receive the ordinary AVAS query pair.
	if _, frameless := value["delegation"]; frameless {
		return false
	}
	if rawType, ok := value["type"]; ok {
		var sessionType string
		if json.Unmarshal(rawType, &sessionType) == nil && strings.TrimSpace(sessionType) != "" {
			return strings.EqualFold(strings.TrimSpace(sessionType), "quicksilver")
		}
	}
	// Older/session-proxy envelopes may omit `type`; the presence of a valid
	// session object is enough to select the AVAS contract in that shape.
	return true
}

func providerQueryKeyPresent(query map[string][]string, key string) bool {
	for name, values := range query {
		if strings.EqualFold(strings.TrimSpace(name), key) && len(values) > 0 {
			return true
		}
	}
	return false
}
