package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
)

// Remote Control uses a bearer token issued by the upstream ChatGPT backend.
// It is intentionally kept separate from the AirGate API key context so an
// upstream token can never be mistaken for a local billing credential.
const (
	CtxKeyCodexRemoteControlToken           = "codex_remote_control_token"
	CtxKeyCodexRemoteControlRecord          = "codex_remote_control_record"
	CtxKeyCodexRemoteControlServerID        = "codex_remote_control_server_id"
	CtxKeyCodexRemoteControlName            = "codex_remote_control_name"
	CtxKeyCodexRemoteControlProtocolVersion = "codex_remote_control_protocol_version"
	CtxKeyCodexRemoteControlInstallationID  = "codex_remote_control_installation_id"
	CtxKeyCodexRemoteControlEnvironmentID   = "codex_remote_control_environment_id"
	CtxKeyCodexRemoteControlAccountID       = "codex_remote_control_account_id"
)

const (
	RemoteControlProtocolVersion = "3"
	remoteControlTokenMaxBytes   = 4096
	remoteControlStoreMaxEntries = 4096
)

// RemoteControlTokenRegistration is the only input accepted by the token
// store. Token plaintext is consumed synchronously and is never retained in a
// record or returned by Lookup.
type RemoteControlTokenRegistration struct {
	Token          string
	KeyInfo        *auth.APIKeyInfo
	AccountID      int
	ServerID       string
	EnvironmentID  string
	Name           string
	InstallationID string
	ExpiresAt      time.Time
}

// RemoteControlTokenRecord is the authenticated binding attached to a
// request. The token itself is deliberately absent; callers only receive the
// metadata needed for account pinning and authorization checks.
type RemoteControlTokenRecord struct {
	KeyID          int
	UserID         int
	GroupID        int
	AccountID      int
	ServerID       string
	EnvironmentID  string
	Name           string
	InstallationID string
	ExpiresAt      time.Time
	KeyInfo        *auth.APIKeyInfo
}

type remoteControlTokenEntry struct {
	hash [32]byte
	RemoteControlTokenRecord
}

// RemoteControlTokenStore is an in-memory, bounded enrollment store. The
// upstream token is a bearer secret, so only SHA-256 digests are held in
// memory. Entries expire with the upstream lease and are rotated on refresh.
type RemoteControlTokenStore struct {
	mu      sync.RWMutex
	entries map[[32]byte]remoteControlTokenEntry
}

func NewRemoteControlTokenStore() *RemoteControlTokenStore {
	return &RemoteControlTokenStore{entries: make(map[[32]byte]remoteControlTokenEntry)}
}

func (s *RemoteControlTokenStore) Register(reg RemoteControlTokenRegistration) error {
	if s == nil {
		return errors.New("remote control token store is nil")
	}
	token, ok := normalizeRemoteControlToken(reg.Token)
	if !ok {
		return errors.New("invalid remote control token")
	}
	if reg.AccountID <= 0 || !validRemoteControlOpaqueID(reg.ServerID) || !validRemoteControlOpaqueID(reg.EnvironmentID) {
		return errors.New("remote control enrollment binding is incomplete")
	}
	if reg.ExpiresAt.IsZero() || !reg.ExpiresAt.After(time.Now()) {
		return errors.New("remote control token is expired")
	}
	serverID := reg.ServerID
	environmentID := reg.EnvironmentID
	if !validRemoteControlOpaqueID(serverID) || !validRemoteControlOpaqueID(environmentID) ||
		!validRemoteControlMetadata(reg.Name) || !validRemoteControlMetadata(reg.InstallationID) {
		return errors.New("invalid remote control enrollment metadata")
	}
	hash := sha256.Sum256([]byte(token))
	record := RemoteControlTokenRecord{
		KeyID:          keyInfoID(reg.KeyInfo),
		UserID:         keyInfoUserID(reg.KeyInfo),
		GroupID:        keyInfoGroupID(reg.KeyInfo),
		AccountID:      reg.AccountID,
		ServerID:       serverID,
		EnvironmentID:  environmentID,
		Name:           reg.Name,
		InstallationID: reg.InstallationID,
		ExpiresAt:      reg.ExpiresAt,
		KeyInfo:        cloneAPIKeyInfo(reg.KeyInfo),
	}
	if record.KeyInfo == nil || record.UserID <= 0 || record.GroupID <= 0 {
		return errors.New("remote control API-key binding is incomplete")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for digest, entry := range s.entries {
		if !entry.ExpiresAt.After(now) || (entry.ServerID == serverID && entry.EnvironmentID == environmentID && entry.AccountID == reg.AccountID && entry.GroupID == record.GroupID) {
			delete(s.entries, digest)
		}
	}
	if len(s.entries) >= remoteControlStoreMaxEntries {
		// Purge the oldest/soonest-expiring entries first. This is a bounded
		// emergency path and intentionally avoids retaining any secret data.
		for len(s.entries) >= remoteControlStoreMaxEntries {
			var victim [32]byte
			var victimExpiry time.Time
			for digest, entry := range s.entries {
				if victimExpiry.IsZero() || entry.ExpiresAt.Before(victimExpiry) {
					victim, victimExpiry = digest, entry.ExpiresAt
				}
			}
			if victimExpiry.IsZero() {
				break
			}
			delete(s.entries, victim)
		}
	}
	entry := remoteControlTokenEntry{hash: hash, RemoteControlTokenRecord: record}
	s.entries[hash] = entry
	return nil
}

// Lookup resolves a bearer token and returns a defensive copy of its binding.
func (s *RemoteControlTokenStore) Lookup(token string) (RemoteControlTokenRecord, bool) {
	if s == nil {
		return RemoteControlTokenRecord{}, false
	}
	normalized, ok := normalizeRemoteControlToken(token)
	if !ok {
		return RemoteControlTokenRecord{}, false
	}
	digest := sha256.Sum256([]byte(normalized))
	s.mu.RLock()
	entry, found := s.entries[digest]
	s.mu.RUnlock()
	if !found {
		return RemoteControlTokenRecord{}, false
	}
	// Keep the explicit constant-time comparison even though the map is keyed
	// by a digest; it prevents a future alternate map implementation from
	// accidentally turning this into a string-prefix comparison.
	if subtle.ConstantTimeCompare(entry.hash[:], digest[:]) != 1 || !entry.ExpiresAt.After(time.Now()) {
		if !entry.ExpiresAt.After(time.Now()) {
			s.Revoke(token)
		}
		return RemoteControlTokenRecord{}, false
	}
	entry.KeyInfo = cloneAPIKeyInfo(entry.KeyInfo)
	return entry.RemoteControlTokenRecord, true
}

// Find returns a token binding by its enrollment identity without requiring
// the bearer. It is used for official refresh/pair/client-management calls,
// which authenticate with the selected ChatGPT OAuth lease rather than the
// server token itself.
func (s *RemoteControlTokenStore) Find(serverID, environmentID string, info *auth.APIKeyInfo, accountID int) (RemoteControlTokenRecord, bool) {
	if s == nil {
		return RemoteControlTokenRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	var match *remoteControlTokenEntry
	for _, entry := range s.entries {
		if !entry.ExpiresAt.After(now) || (serverID != "" && entry.ServerID != serverID) || (environmentID != "" && entry.EnvironmentID != environmentID) {
			continue
		}
		if accountID > 0 && entry.AccountID != accountID {
			continue
		}
		if info != nil && !remoteControlRecordMatchesKey(entry.RemoteControlTokenRecord, info) {
			continue
		}
		if match != nil {
			// Ambiguous identity must fail closed rather than pinning a request to
			// an arbitrary account when several Codex accounts are enrolled.
			return RemoteControlTokenRecord{}, false
		}
		copyEntry := entry
		match = &copyEntry
	}
	if match == nil {
		return RemoteControlTokenRecord{}, false
	}
	match.KeyInfo = cloneAPIKeyInfo(match.KeyInfo)
	return match.RemoteControlTokenRecord, true
}

func (s *RemoteControlTokenStore) Revoke(token string) {
	if s == nil {
		return
	}
	normalized, ok := normalizeRemoteControlToken(token)
	if !ok {
		return
	}
	digest := sha256.Sum256([]byte(normalized))
	s.mu.Lock()
	delete(s.entries, digest)
	s.mu.Unlock()
}

func (s *RemoteControlTokenStore) RevokeServer(serverID string, accountID int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for digest, entry := range s.entries {
		if entry.ServerID == serverID && (accountID <= 0 || entry.AccountID == accountID) {
			delete(s.entries, digest)
		}
	}
	s.mu.Unlock()
}

func normalizeRemoteControlToken(raw string) (string, bool) {
	token := strings.TrimSpace(raw)
	if token == "" || len(token) > remoteControlTokenMaxBytes || token != raw {
		return "", false
	}
	for _, r := range token {
		if r < 0x21 || r == 0x7f {
			return "", false
		}
	}
	return token, true
}

func validRemoteControlMetadata(value string) bool {
	// Name and installation ID are optional metadata fields.  Preserve the
	// empty value, but apply the same opaque-string safety rules whenever a
	// caller supplies one.
	return value == "" || validRemoteControlOpaqueID(value)
}

// validRemoteControlOpaqueID validates an upstream server/environment identity
// as one opaque value.  The official client URL-encodes path separators and
// punctuation inside these IDs (for example `env /?`), so `/`, `?`, `#`, and
// `%` are intentionally allowed after the request path has been decoded once.
// Backslash remains forbidden because Windows-aware proxies and path
// normalizers may treat it as a separator.  The value is never recursively
// decoded here.
func validRemoteControlOpaqueID(value string) bool {
	if value == "" || strings.TrimSpace(value) == "" || value == "." || value == ".." || len(value) > 512 || !utf8.ValidString(value) {
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

func keyInfoID(info *auth.APIKeyInfo) int {
	if info == nil {
		return 0
	}
	return info.KeyID
}

func keyInfoUserID(info *auth.APIKeyInfo) int {
	if info == nil {
		return 0
	}
	return info.UserID
}

func keyInfoGroupID(info *auth.APIKeyInfo) int {
	if info == nil {
		return 0
	}
	return info.GroupID
}

func remoteControlRecordMatchesKey(record RemoteControlTokenRecord, info *auth.APIKeyInfo) bool {
	if info == nil {
		return false
	}
	if record.KeyID > 0 && info.KeyID != record.KeyID {
		return false
	}
	return record.UserID == info.UserID && record.GroupID == info.GroupID
}

func cloneAPIKeyInfo(info *auth.APIKeyInfo) *auth.APIKeyInfo {
	if info == nil {
		return nil
	}
	out := *info
	if info.UserGroupRates != nil {
		out.UserGroupRates = make(map[int64]float64, len(info.UserGroupRates))
		for key, value := range info.UserGroupRates {
			out.UserGroupRates[key] = value
		}
	}
	if info.TierGroupRates != nil {
		out.TierGroupRates = make(map[int64]float64, len(info.TierGroupRates))
		for key, value := range info.TierGroupRates {
			out.TierGroupRates[key] = value
		}
	}
	if info.GroupAlphaSearchPrice != nil {
		value := *info.GroupAlphaSearchPrice
		out.GroupAlphaSearchPrice = &value
	}
	out.GroupAllowedClients = append([]string(nil), info.GroupAllowedClients...)
	if info.GroupFallbackID != nil {
		value := *info.GroupFallbackID
		out.GroupFallbackID = &value
	}
	return &out
}

// CodexRemoteControlAuth accepts either the normal AirGate API key or an
// enrolled Remote Control bearer. Explicit Remote Control headers are always
// authoritative; Authorization is interpreted as the server bearer only for
// pair/pair-status and the server WebSocket path. On OAuth endpoints, a
// separate X-API-Key (or X-AirGate-API-Key) remains the local credential. When
// both a valid Remote Control bearer and a local key are present, the key is
// checked against the enrollment binding.
func CodexRemoteControlAuth(db *ent.Client, store *RemoteControlTokenStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c == nil || c.Request == nil {
			return
		}
		// There are two wire representations of a Remote Control bearer:
		//
		//   * an explicit X-Codex-Remote-Control-Token (or legacy
		//     X-Remote-Control-Token) header, which is unambiguous on every
		//     Remote Control route; and
		//   * Authorization: Bearer <token> on the upstream pair/pair-status
		//     calls and the server WebSocket handshake.
		//
		// Enroll/refresh/client-management calls also use Authorization, but
		// that bearer is the ChatGPT OAuth lease.  Those calls must continue to
		// authenticate through the separately supplied AirGate API key.  Do not
		// probe the token store for an arbitrary OAuth bearer: doing so both
		// misclassifies OAuth and lets an invalid explicit token fall through to
		// the local key path.
		remoteToken, remotePresented, explicitRemoteToken := remoteControlCredential(c)
		if remotePresented {
			// An explicit token is authoritative.  In particular, an unknown or
			// malformed explicit header must never fall back to X-API-Key.  The
			// same fail-closed rule applies to Authorization on the two protocol
			// operations where the official client defines it as the server token.
			if store == nil {
				abortRemoteControlAuthError(c, http.StatusUnauthorized, "remote_control_unavailable", "Remote Control authentication is unavailable")
				return
			}
			if record, found := store.Lookup(remoteToken); found {
				info, err := validateOptionalBoundAPIKey(c, db, record)
				if err != nil {
					abortRemoteControlAuthError(c, err.status, err.code, err.message)
					return
				}
				if info == nil {
					info = record.KeyInfo
				}
				if info == nil || !remoteControlRecordMatchesKey(record, info) {
					abortRemoteControlAuthError(c, http.StatusForbidden, "remote_control_binding_mismatch", "Remote Control token is not bound to this API key")
					return
				}
				setRemoteControlContext(c, remoteToken, record, info)
				c.Next()
				return
			}
			// Keep the explicit flag in the branch condition/documentation even
			// though both credential forms currently reject an unknown token.  It
			// makes the security boundary obvious and prevents a future change
			// from accidentally restoring API-key fallback for explicit headers.
			if explicitRemoteToken || remoteControlAuthorizationRequiresToken(c) {
				abortRemoteControlAuthError(c, http.StatusUnauthorized, "invalid_remote_control_token", "Remote Control token is invalid or expired")
				return
			}
		}

		key := airGateAPIKey(c)
		if key == "" {
			abortWithRelayError(c, http.StatusUnauthorized, "missing_api_key", "缺少 API Key")
			return
		}
		if db == nil {
			// Never pass a nil ent client into ValidateAPIKey. An unregistered
			// OAuth bearer is allowed to continue only when its separate AirGate
			// key can be validated and bound to the request.
			abortWithRelayError(c, http.StatusServiceUnavailable, "service_unavailable", "service unavailable")
			return
		}
		info, err := auth.ValidateAPIKey(c.Request.Context(), db, key)
		if err != nil {
			status, code, message := apiKeyAuthError(err)
			abortWithRelayError(c, status, code, message)
			return
		}
		c.Set(CtxKeyUserID, info.UserID)
		c.Set(CtxKeyKeyInfo, info)
		c.Next()
	}
}

type remoteControlAuthError struct {
	status  int
	code    string
	message string
}

func validateOptionalBoundAPIKey(c *gin.Context, db *ent.Client, record RemoteControlTokenRecord) (*auth.APIKeyInfo, *remoteControlAuthError) {
	key := airGateAPIKey(c)
	if key == "" {
		return nil, nil
	}
	if db == nil {
		return nil, &remoteControlAuthError{status: http.StatusServiceUnavailable, code: "service_unavailable", message: "service unavailable"}
	}
	info, err := auth.ValidateAPIKey(c.Request.Context(), db, key)
	if err != nil {
		status, code, message := apiKeyAuthError(err)
		return nil, &remoteControlAuthError{status: status, code: code, message: message}
	}
	if !remoteControlRecordMatchesKey(record, info) {
		return nil, &remoteControlAuthError{status: http.StatusForbidden, code: "remote_control_binding_mismatch", message: "Remote Control token is not bound to this API key"}
	}
	return info, nil
}

func setRemoteControlContext(c *gin.Context, token string, record RemoteControlTokenRecord, info *auth.APIKeyInfo) {
	c.Set(CtxKeyUserID, info.UserID)
	c.Set(CtxKeyKeyInfo, info)
	c.Set(CtxKeyCodexRemoteControlToken, token)
	c.Set(CtxKeyCodexRemoteControlRecord, record)
	c.Set(CtxKeyCodexRemoteControlServerID, record.ServerID)
	c.Set(CtxKeyCodexRemoteControlEnvironmentID, record.EnvironmentID)
	c.Set(CtxKeyCodexRemoteControlName, record.Name)
	c.Set(CtxKeyCodexRemoteControlInstallationID, record.InstallationID)
	c.Set(CtxKeyCodexRemoteControlProtocolVersion, RemoteControlProtocolVersion)
	c.Set(CtxKeyCodexRemoteControlAccountID, record.AccountID)
}

// remoteControlCredential returns the credential that is unambiguously a
// Remote Control bearer for this request.  The third result reports whether
// the credential came from an explicit Remote Control header; presence is
// retained even when that header is empty/malformed so callers can reject it
// instead of falling back to an AirGate key.
func remoteControlCredential(c *gin.Context) (token string, presented bool, explicit bool) {
	if c == nil || c.Request == nil {
		return "", false, false
	}
	if token, present := explicitRemoteControlHeader(c.Request.Header); present {
		return token, true, true
	}
	if !remoteControlAuthorizationRequiresToken(c) {
		// Authorization on enroll/refresh/client-management is the upstream
		// OAuth lease.  Leave it to airGateAPIKey below.
		return "", false, false
	}
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", true, false
	}
	// Unlike the ordinary API-key path, a server-token path treats every
	// Bearer value as a Remote Control candidate.  A legitimate upstream token
	// is opaque and may itself happen to begin with "sk-".
	return strings.TrimSpace(parts[1]), true, false
}

// explicitRemoteControlHeader distinguishes an absent header from an empty
// one.  Header names are compared case-insensitively because callers that
// construct http.Request values directly can bypass net/http's canonical key
// normalization.  Multiple differing values are treated as malformed rather
// than selecting one attacker-controlled value.
func explicitRemoteControlHeader(headers http.Header) (string, bool) {
	if headers == nil {
		return "", false
	}
	var (
		present bool
		value   string
		set     bool
	)
	for name, values := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower != "x-codex-remote-control-token" && lower != "x-remote-control-token" {
			continue
		}
		present = true
		if len(values) == 0 {
			return "", true
		}
		for _, candidate := range values {
			if !set {
				value, set = candidate, true
				continue
			}
			if candidate != value {
				return "", true
			}
		}
	}
	if !present {
		return "", false
	}
	return value, true
}

// remoteControlAuthorizationRequiresToken identifies the exact operations
// where the official Codex client puts the enrolled server bearer in
// Authorization.  Enroll/refresh and client-management requests use the
// OAuth bearer in that header and therefore intentionally return false here.
func remoteControlAuthorizationRequiresToken(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	canonical := canonicalRemoteControlAuthPath(path)
	switch canonical {
	case "/server/pair", "/server/pair/status":
		return true
	case "/server":
		// The official server endpoint is a WebSocket.  Some reverse proxies
		// strip Upgrade/Connection before the request reaches Core, so when no
		// separate AirGate key is present treat the endpoint's bearer as the
		// Remote Control token even if the upgrade markers are gone.  Inspect
		// only dedicated API-key headers here: airGateAPIKey also accepts
		// Authorization: Bearer sk-..., and using it would make an opaque
		// Remote Control token that happens to start with sk- look local.
		if isRemoteControlWebSocketRequest(c) {
			return true
		}
		if dedicatedAirGateAPIKey(c) != "" {
			return false
		}
		if token, ok := authorizationBearerValue(c); ok && strings.HasPrefix(token, "sk-") {
			return false
		}
		return true
	default:
		return false
	}
}

func canonicalRemoteControlAuthPath(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return ""
	}
	// Keep this list in sync with the finite aliases registered by the server.
	// Longest prefixes must precede shorter ones to avoid reducing
	// /backend-api/wham to /backend-api and leaving /wham in the suffix.
	for _, prefix := range []string{
		"/backend-api/wham/v1", "/backend-api/wham",
		"/backend-api/codex/v1", "/backend-api/codex",
		"/backend-api/v1/wham", "/backend-api/v1", "/backend-api",
		"/api/codex/v1", "/api/codex",
		"/codex/v1", "/codex",
		"/wham/v1", "/wham",
		"/v1", "",
	} {
		base := prefix + "/remote/control"
		if path == base {
			return "/remote/control"
		}
		if strings.HasPrefix(path, base+"/") {
			rest := strings.TrimPrefix(path, base)
			if rest == "/server" || rest == "/server/pair" || rest == "/server/pair/status" {
				return rest
			}
		}
	}
	return ""
}

func isRemoteControlWebSocketRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.GetHeader("Upgrade")), "websocket") {
		return false
	}
	// A few reverse proxies strip Connection while preserving Upgrade.  Accept
	// that valid-enough handshake shape; when Connection is present, require an
	// upgrade token to avoid classifying an ordinary HTTP GET as a WS request.
	connection := strings.TrimSpace(c.GetHeader("Connection"))
	if connection == "" {
		return true
	}
	for _, part := range strings.Split(connection, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}

func airGateAPIKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if key := dedicatedAirGateAPIKey(c); key != "" {
		return key
	}
	if token, ok := authorizationBearerValue(c); ok && strings.HasPrefix(token, "sk-") {
		return token
	}
	return ""
}

// dedicatedAirGateAPIKey reads only non-Authorization credential headers.
// Remote Control path classification uses this helper so an upstream OAuth
// bearer (or an opaque server token) cannot be mistaken for a local key merely
// because its text happens to begin with "sk-".
func dedicatedAirGateAPIKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	for _, name := range []string{"X-AirGate-API-Key", "X-AirGate-Key", "X-API-Key", "x-api-key", "x-goog-api-key", "api-key"} {
		if key := strings.TrimSpace(c.GetHeader(name)); strings.HasPrefix(key, "sk-") {
			return key
		}
	}
	return ""
}

func authorizationBearerValue(c *gin.Context) (string, bool) {
	if c == nil || c.Request == nil {
		return "", false
	}
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", true
	}
	return token, true
}

func apiKeyAuthError(err error) (int, string, string) {
	status := http.StatusUnauthorized
	code := "invalid_api_key"
	message := err.Error()
	switch err {
	case auth.ErrAPIKeyExpired:
		code = "api_key_expired"
	case auth.ErrAPIKeyQuota:
		status, code = http.StatusPaymentRequired, "insufficient_quota"
	case auth.ErrAPIKeyGroupUnbound:
		status, code = http.StatusForbidden, "api_key_misconfigured"
	case auth.ErrAPIKeyGroupExclusive:
		status, code = http.StatusForbidden, "api_key_group_restricted"
	case auth.ErrUserDisabled:
		status, code = http.StatusForbidden, "account_disabled"
	case auth.ErrInvalidAPIKey:
	default:
		status, code, message = http.StatusServiceUnavailable, "service_unavailable", "服务暂不可用，请稍后重试"
	}
	return status, code, message
}

func abortRemoteControlAuthError(c *gin.Context, status int, code, message string) {
	if c == nil {
		return
	}
	protocol := errfmt.ProtocolForPath(c.Request.URL.Path)
	c.AbortWithStatusJSON(status, errfmt.Render(protocol, status, "authentication_error", code, message, RequestIDFromGinContext(c)))
}
