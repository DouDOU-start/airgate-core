package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// Context keys used by a Remote Control token middleware.  The HTTP handler
// intentionally does not interpret Authorization as a remote-control token:
// APIKeyAuth owns that header for the AirGate key.  A future middleware can
// authenticate a dedicated remote-control bearer and set these values before
// invoking HandleCodexRemoteControl.
const (
	CtxKeyCodexRemoteControlToken           = "codex_remote_control_token"
	CtxKeyCodexRemoteControlServerID        = "codex_remote_control_server_id"
	CtxKeyCodexRemoteControlName            = "codex_remote_control_name"
	CtxKeyCodexRemoteControlProtocolVersion = "codex_remote_control_protocol_version"
	CtxKeyCodexRemoteControlInstallationID  = "codex_remote_control_installation_id"
)

// codexRemoteControlRoute describes one finite HTTP contract. Dynamic client
// routes are expanded only after validating each path segment; arbitrary
// /remote/control paths are never accepted as a credentialed proxy.
type codexRemoteControlRoute struct {
	Endpoint string
	Method   string
	Path     string
	Dynamic  bool
}

var codexRemoteControlStaticRoutes = []codexRemoteControlRoute{
	{Endpoint: adaptor.EndpointCodexRemoteControlEnroll, Method: http.MethodPost, Path: "/remote/control/server/enroll"},
	{Endpoint: adaptor.EndpointCodexRemoteControlRefresh, Method: http.MethodPost, Path: "/remote/control/server/refresh"},
	{Endpoint: adaptor.EndpointCodexRemoteControlPair, Method: http.MethodPost, Path: "/remote/control/server/pair"},
	{Endpoint: adaptor.EndpointCodexRemoteControlPairStatus, Method: http.MethodPost, Path: "/remote/control/server/pair/status"},
}

// codexRemoteControlRoutePrefixes are the public aliases supported by Core.
// Keep the longest forms first so /backend-api/wham is not reduced to the
// shorter /backend-api prefix and accidentally leaves /wham in the suffix.
var codexRemoteControlRoutePrefixes = []string{
	"/backend-api/wham/v1", "/backend-api/wham",
	"/backend-api/codex/v1", "/backend-api/codex",
	"/backend-api/v1/wham",
	"/backend-api/v1", "/backend-api",
	"/api/codex/v1", "/api/codex",
	"/codex/v1", "/codex",
	"/wham/v1", "/wham",
	"/v1", "",
}

// HandleCodexRemoteControl forwards the official app-server Remote Control
// HTTP contracts as raw/native requests. All operations are OAuth-only,
// zero-billing and fail closed on unknown methods or unsafe environment/client
// identifiers. Pair and pair-status receive a dedicated server token through
// context/header metadata; enroll/refresh and client management use the
// selected OAuth lease supplied by Core.
func (p *Pipeline) HandleCodexRemoteControl(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	route, providerPath, ok := codexRemoteControlRouteForRequest(c)
	if !ok {
		writeError(c, http.StatusNotFound, "invalid_request_error", "unsupported_endpoint", "unsupported Codex remote-control endpoint")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(c.Request.Method), route.Method) {
		writeError(c, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "HTTP method is not supported for this Codex remote-control endpoint")
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
	if len(body) == 0 {
		body = []byte{}
	}

	// POST contracts are JSON objects in the official client. Validate only the
	// top-level shape while preserving the exact raw bytes for the provider;
	// unknown fields and future protocol additions remain untouched. GET/DELETE
	// client-management calls normally have no body, but if one is supplied it
	// must still be a JSON object rather than an arbitrary upload.
	parseBody := body
	if len(bytes.TrimSpace(parseBody)) == 0 {
		parseBody = []byte("{}")
	}
	req, err := dto.ParseChatRequest(parseBody)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "Codex remote-control request body must be a JSON object")
		return
	}
	req.Model = ""
	req.Stream = false

	metadata := codexRemoteControlMetadataFromRequest(c, body)
	// A bearer authenticated by the dedicated Remote Control middleware is
	// bound to the OAuth account selected during enrollment. Keep every later
	// control request on that account instead of letting weighted group routing
	// pick a different lease.
	metadata = remoteControlAccountMetadata(p, metadata, keyInfo)
	if !validCodexRemoteControlMetadata(metadata.token) ||
		!validCodexRemoteControlMetadata(metadata.serverID) ||
		!validCodexRemoteControlMetadata(metadata.environmentID) ||
		!validCodexRemoteControlMetadata(metadata.name) ||
		!validCodexRemoteControlMetadata(metadata.protocolVersion) ||
		!validCodexRemoteControlMetadata(metadata.installationID) {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_remote_control_metadata", "Codex remote-control metadata is invalid")
		return
	}
	providerHeaders := codexRemoteControlProviderHeaders(route.Endpoint, c)
	opts := forwardOptions{
		method:                       route.Method,
		rawBody:                      body,
		rawContentType:               c.GetHeader("Content-Type"),
		providerPath:                 providerPath,
		nativeCodexAccountsOnly:      true,
		nativeCodexOAuthOnly:         true,
		allowUnpriced:                true,
		zeroBilling:                  true,
		passthroughResponse:          true,
		providerHeaders:              providerHeaders,
		remoteControlToken:           metadata.token,
		remoteControlServerID:        metadata.serverID,
		remoteControlName:            metadata.name,
		remoteControlProtocolVersion: metadata.protocolVersion,
		installationID:               metadata.installationID,
		nativeCodexAccountID:         metadata.accountID,
	}
	// Enroll/refresh responses issue the bearer used by all subsequent Remote
	// Control calls. Register only after a successful native response has been
	// classified; the transform never rewrites the provider bytes.
	if route.Endpoint == adaptor.EndpointCodexRemoteControlEnroll || route.Endpoint == adaptor.EndpointCodexRemoteControlRefresh {
		opts.responseTransform = func(account *accountreg.Snapshot, result *attemptResult) error {
			return p.registerRemoteControlEnrollment(c, keyInfo, account, metadata, result)
		}
	}
	p.forwardOpt(c, keyInfo, req, route.Endpoint, opts)
}

// codexRemoteControlMetadata is deliberately not serialized into the request
// body or ordinary provider headers. It is carried in the Core/plugin envelope
// so a remote-control bearer cannot be confused with an AirGate API key.
type codexRemoteControlMetadata struct {
	token           string
	serverID        string
	environmentID   string
	name            string
	protocolVersion string
	installationID  string
	accountID       int
}

func codexRemoteControlMetadataFromRequest(c *gin.Context, body []byte) codexRemoteControlMetadata {
	accountID := 0
	if c != nil {
		accountID = c.GetInt(middleware.CtxKeyCodexRemoteControlAccountID)
	}
	metadata := codexRemoteControlMetadata{
		token:           contextString(c, CtxKeyCodexRemoteControlToken),
		serverID:        contextString(c, CtxKeyCodexRemoteControlServerID),
		environmentID:   contextString(c, middleware.CtxKeyCodexRemoteControlEnvironmentID),
		name:            contextString(c, CtxKeyCodexRemoteControlName),
		protocolVersion: contextString(c, CtxKeyCodexRemoteControlProtocolVersion),
		installationID:  contextString(c, CtxKeyCodexRemoteControlInstallationID),
		accountID:       accountID,
	}
	// Header fallbacks are useful for a deployment that authenticates the
	// dedicated token before entering the standard API-key middleware. Never
	// read Authorization here; that value is the AirGate key at this boundary.
	if metadata.token == "" {
		metadata.token = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Token", "X-Remote-Control-Token")
	}
	if metadata.serverID == "" {
		metadata.serverID = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Server-Id", "X-Remote-Control-Server-Id")
	}
	if metadata.environmentID == "" {
		metadata.environmentID = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Environment-Id", "X-Remote-Control-Environment-Id")
	}
	if metadata.name == "" {
		metadata.name = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Name", "X-Remote-Control-Name")
	}
	if metadata.protocolVersion == "" {
		metadata.protocolVersion = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Protocol-Version", "X-Remote-Control-Protocol-Version")
	}
	if metadata.installationID == "" {
		metadata.installationID = firstNonEmptyHeader(c, "X-Codex-Installation-Id", "X-Remote-Control-Installation-Id")
	}

	// Enroll/refresh carry identity metadata in their JSON body. Decode only a
	// small envelope of RawMessage values so large/future fields are not copied
	// or rewritten; body remains the exact bytes sent by the client.
	if len(bytes.TrimSpace(body)) > 0 {
		var envelope struct {
			ServerID        string `json:"server_id"`
			EnvironmentID   string `json:"environment_id"`
			Name            string `json:"name"`
			ProtocolVersion string `json:"protocol_version"`
			InstallationID  string `json:"installation_id"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			if metadata.serverID == "" {
				metadata.serverID = strings.TrimSpace(envelope.ServerID)
			}
			if metadata.environmentID == "" {
				metadata.environmentID = strings.TrimSpace(envelope.EnvironmentID)
			}
			if metadata.name == "" {
				metadata.name = strings.TrimSpace(envelope.Name)
			}
			if metadata.protocolVersion == "" {
				metadata.protocolVersion = strings.TrimSpace(envelope.ProtocolVersion)
			}
			if metadata.installationID == "" {
				metadata.installationID = strings.TrimSpace(envelope.InstallationID)
			}
		}
	}
	return metadata
}

// remoteControlAccountMetadata resolves an enrollment identity back to the
// account that created it. Refresh requests made by the official CLI carry the
// ChatGPT OAuth bearer (not the previously issued Remote Control bearer), so
// middleware cannot always attach the account id. A server id/environment id
// plus the already-authenticated AirGate key is sufficient to recover the
// binding without ever treating an arbitrary bearer as trusted.
func remoteControlAccountMetadata(p *Pipeline, metadata codexRemoteControlMetadata, keyInfo *auth.APIKeyInfo) codexRemoteControlMetadata {
	if metadata.accountID > 0 || p == nil || p.remoteControlTokens == nil || keyInfo == nil {
		return metadata
	}
	record, found := p.remoteControlTokens.Find(metadata.serverID, metadata.environmentID, keyInfo, 0)
	if !found {
		return metadata
	}
	metadata.accountID = record.AccountID
	if metadata.serverID == "" {
		metadata.serverID = record.ServerID
	}
	if metadata.environmentID == "" {
		metadata.environmentID = record.EnvironmentID
	}
	if metadata.name == "" {
		metadata.name = record.Name
	}
	if metadata.installationID == "" {
		metadata.installationID = record.InstallationID
	}
	return metadata
}

type codexRemoteControlEnrollmentResponse struct {
	ServerID           string          `json:"server_id"`
	EnvironmentID      string          `json:"environment_id"`
	RemoteControlToken string          `json:"remote_control_token"`
	ExpiresAt          json.RawMessage `json:"expires_at"`
}

// registerRemoteControlEnrollment validates and stores the upstream bearer
// returned by enroll/refresh. Only a SHA-256 digest is retained by the store;
// the raw token remains in memory for this synchronous transform and is never
// copied to audit metadata or ordinary response headers.
func (p *Pipeline) registerRemoteControlEnrollment(
	_ *gin.Context,
	keyInfo *auth.APIKeyInfo,
	account *accountreg.Snapshot,
	metadata codexRemoteControlMetadata,
	result *attemptResult,
) error {
	if p == nil || p.remoteControlTokens == nil {
		return errors.New("remote control token store is unavailable")
	}
	if keyInfo == nil || account == nil || account.ID <= 0 {
		return errors.New("remote control enrollment account binding is unavailable")
	}
	if result == nil || result.statusCode < http.StatusOK || result.statusCode >= http.StatusMultipleChoices {
		return errors.New("remote control enrollment response is not successful")
	}
	body := bytes.TrimSpace(result.body)
	if len(body) == 0 || len(body) > 64<<10 {
		return errors.New("remote control enrollment response is invalid")
	}
	var response codexRemoteControlEnrollmentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return errors.New("remote control enrollment response is invalid")
	}
	serverID := response.ServerID
	if strings.TrimSpace(serverID) == "" {
		serverID = metadata.serverID
	}
	environmentID := response.EnvironmentID
	if strings.TrimSpace(environmentID) == "" {
		environmentID = metadata.environmentID
	}
	// A refresh must not be allowed to rotate one enrollment into another
	// server/environment identity. Enroll has no prior identity and therefore
	// accepts the values issued by the upstream response.
	if metadata.serverID != "" && serverID != "" && metadata.serverID != serverID {
		return errors.New("remote control enrollment server identity changed")
	}
	if metadata.environmentID != "" && environmentID != "" && metadata.environmentID != environmentID {
		return errors.New("remote control enrollment environment identity changed")
	}
	expiresAt, err := parseCodexRemoteControlExpiry(response.ExpiresAt)
	if err != nil {
		return err
	}
	return p.remoteControlTokens.Register(middleware.RemoteControlTokenRegistration{
		Token:          strings.TrimSpace(response.RemoteControlToken),
		KeyInfo:        keyInfo,
		AccountID:      account.ID,
		ServerID:       serverID,
		EnvironmentID:  environmentID,
		Name:           strings.TrimSpace(metadata.name),
		InstallationID: strings.TrimSpace(metadata.installationID),
		ExpiresAt:      expiresAt,
	})
}

func parseCodexRemoteControlExpiry(raw json.RawMessage) (time.Time, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return time.Time{}, errors.New("remote control enrollment expiry is missing")
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return time.Time{}, errors.New("remote control enrollment expiry is invalid")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return time.Time{}, errors.New("remote control enrollment expiry is missing")
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return time.Time{}, errors.New("remote control enrollment expiry is invalid")
		}
		if !parsed.After(time.Now()) {
			return time.Time{}, errors.New("remote control enrollment token is expired")
		}
		return parsed, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return time.Time{}, errors.New("remote control enrollment expiry is invalid")
	}
	seconds, err := number.Int64()
	if err != nil || seconds <= 0 {
		return time.Time{}, errors.New("remote control enrollment expiry is invalid")
	}
	// Some API gateways normalize timestamps to milliseconds. Accept that
	// representation while keeping the canonical store value as time.Time.
	if seconds > 100000000000 {
		seconds /= 1000
	}
	parsed := time.Unix(seconds, 0).UTC()
	if !parsed.After(time.Now()) {
		return time.Time{}, errors.New("remote control enrollment token is expired")
	}
	return parsed, nil
}

func validCodexRemoteControlMetadata(value string) bool {
	// These values are optional on enroll/refresh requests, so an empty string
	// remains valid.  Once present, treat server/environment IDs and the other
	// protocol metadata as opaque Unicode strings: the official client may
	// carry spaces and URL punctuation (`/`, `?`, `#`, `%`) that will be escaped
	// by the path builder when an ID is used in a URL segment.
	if value == "" {
		return true
	}
	if strings.TrimSpace(value) == "" || len(value) > 512 || value == "." || value == ".." || !utf8.ValidString(value) {
		return false
	}
	if strings.ContainsRune(value, '\\') {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func contextString(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	value, ok := c.Get(key)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return ""
	}
}

func firstNonEmptyHeader(c *gin.Context, names ...string) string {
	if c == nil {
		return ""
	}
	for _, name := range names {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			return value
		}
	}
	return ""
}

func codexRemoteControlProviderHeaders(endpoint string, c *gin.Context) http.Header {
	// The selected OAuth credential and ChatGPT account id are injected by the
	// native executor. Preserve only safe protocol metadata from the caller;
	// pair/pair-status token travels in the explicit envelope field instead.
	if c == nil {
		return nil
	}
	// A nil map means “no overrides”; returning a map with a nil token header
	// makes the intent explicit if an upstream middleware copied one into the
	// safe projection in a future release.
	overrides := make(http.Header)
	// Enrollment and pairing responses are small JSON control records. Ask the
	// upstream for an identity representation so the lifecycle parser can
	// validate/register the token without needing to retain a second compressed
	// copy; the response bytes themselves are still passed through untouched by
	// the native transport.
	overrides.Set("Accept-Encoding", "identity")
	if endpoint == adaptor.EndpointCodexRemoteControlPair || endpoint == adaptor.EndpointCodexRemoteControlPairStatus {
		overrides["Authorization"] = nil
		overrides["X-Codex-Remote-Control-Token"] = nil
	}
	return overrides
}

func codexRemoteControlRouteForRequest(c *gin.Context) (codexRemoteControlRoute, string, bool) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return codexRemoteControlRoute{}, "", false
	}
	// Gin's default router matches URL.Path, where an encoded slash (%2F) has
	// already become a route separator.  Remote Control IDs are URL *path
	// segments*, however, and the official client deliberately escapes `/`,
	// `?`, `#`, and `%` inside those segments.  Use EscapedPath so the route
	// shape is evaluated before decoding the two dynamic segments.  The server
	// engine enables UseRawPath for the same reason (see server.NewServer).
	path, ok := codexRemoteControlEscapedPath(c.Request.URL)
	if !ok {
		return codexRemoteControlRoute{}, "", false
	}
	canonical := codexRemoteControlCanonicalPath(path)
	if canonical == "" {
		return codexRemoteControlRoute{}, "", false
	}
	for _, route := range codexRemoteControlStaticRoutes {
		if canonical == route.Path {
			return route, route.Path, true
		}
	}
	parts := strings.Split(strings.Trim(canonical, "/"), "/")
	if len(parts) != 5 || parts[0] != "remote" || parts[1] != "control" || parts[2] != "environments" || parts[4] != "clients" {
		if len(parts) != 6 || parts[0] != "remote" || parts[1] != "control" || parts[2] != "environments" || parts[4] != "clients" {
			return codexRemoteControlRoute{}, "", false
		}
	}
	environmentID, ok := decodeCodexRemoteControlSegment(parts[3])
	if !ok {
		return codexRemoteControlRoute{}, "", false
	}
	if len(parts) == 5 {
		if !strings.EqualFold(c.Request.Method, http.MethodGet) {
			return codexRemoteControlRoute{Endpoint: adaptor.EndpointCodexRemoteControlClientsList, Method: http.MethodGet, Dynamic: true}, "", true
		}
		return codexRemoteControlRoute{Endpoint: adaptor.EndpointCodexRemoteControlClientsList, Method: http.MethodGet, Path: "/remote/control/environments/" + url.PathEscape(environmentID) + "/clients", Dynamic: true}, "/remote/control/environments/" + url.PathEscape(environmentID) + "/clients", true
	}
	clientID, ok := decodeCodexRemoteControlSegment(parts[5])
	if !ok {
		return codexRemoteControlRoute{}, "", false
	}
	providerPath := "/remote/control/environments/" + url.PathEscape(environmentID) + "/clients/" + url.PathEscape(clientID)
	if !strings.EqualFold(c.Request.Method, http.MethodDelete) {
		return codexRemoteControlRoute{Endpoint: adaptor.EndpointCodexRemoteControlClientRevoke, Method: http.MethodDelete, Dynamic: true}, "", true
	}
	return codexRemoteControlRoute{Endpoint: adaptor.EndpointCodexRemoteControlClientRevoke, Method: http.MethodDelete, Path: providerPath, Dynamic: true}, providerPath, true
}

func codexRemoteControlCanonicalPath(path string) string {
	// Do not TrimSpace here: an encoded trailing space belongs to the opaque
	// environment/client ID.  Only a literal trailing slash is an optional URL
	// spelling variation.
	path = strings.TrimRight(path, "/")
	for _, prefix := range codexRemoteControlRoutePrefixes {
		if prefix == "" {
			if strings.HasPrefix(path, "/") {
				return path
			}
			continue
		}
		if path == prefix {
			return ""
		}
		if strings.HasPrefix(path, prefix+"/") {
			return strings.TrimPrefix(path, prefix)
		}
	}
	return ""
}

func validCodexRemoteControlSegment(value string) bool {
	// IDs are opaque and may contain spaces, URL punctuation, and Unicode.  Do
	// not trim the value because doing so would change the identifier sent to
	// the upstream.  Reject only empty/whitespace-only values and traversal
	// sentinels.
	if value == "" || strings.TrimSpace(value) == "" || value == "." || value == ".." || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	// A slash is valid *inside* an ID when it arrived as %2F and was decoded
	// exactly once.  Backslash remains forbidden because Windows-aware proxies
	// and path normalizers can treat it as a separator.  `?`, `#`, and `%` are
	// ordinary opaque ID bytes after one-pass path decoding.
	if strings.ContainsRune(value, '\\') {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// codexRemoteControlEscapedPath returns the request path in escaped form and
// fails closed when a caller supplied a malformed RawPath.  URL.EscapedPath
// normally preserves RawPath only when it is a valid encoding of Path; the
// explicit validation below prevents malformed encodings from being silently
// re-escaped into a different identifier.
func codexRemoteControlEscapedPath(u *url.URL) (string, bool) {
	if u == nil {
		return "", false
	}
	if u.RawPath != "" {
		decoded, err := url.PathUnescape(u.RawPath)
		if err != nil || decoded != u.Path {
			return "", false
		}
	}
	path := u.EscapedPath()
	if path == "" {
		path = u.Path
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", false
	}
	return path, true
}

// decodeCodexRemoteControlSegment performs exactly one RFC 3986 path-segment
// decode.  In particular, `%2F` becomes a slash in the opaque ID while
// `%252F` remains the literal string `%2F`; recursive decoding would turn the
// latter into an unintended separator.
func decodeCodexRemoteControlSegment(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || !validCodexRemoteControlSegment(decoded) {
		return "", false
	}
	return decoded, true
}
