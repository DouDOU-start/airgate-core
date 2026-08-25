package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// websocketUpgrader is deliberately kept at the Core boundary.  The Core
// authenticates the request before this upgrader is reached; the plugin only
// receives the already-authenticated connection and a filtered metadata view.
// We do not manufacture a browser/user-agent, cookie, TLS or attestation
// identity here.
var websocketUpgrader = websocket.Upgrader{
	CheckOrigin: checkWebSocketOrigin,
}

// checkWebSocketOrigin prevents cross-site browser WebSocket handshakes while
// keeping non-browser clients (which normally omit Origin) compatible. The
// API key is still required by the outer router; Origin is only an additional
// CSWSH safeguard and is never forwarded as an identity signal.
func checkWebSocketOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u == nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if u.Host == "" || (scheme != "http" && scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if !strings.EqualFold(u.Hostname(), requestHostName(r.Host)) {
		return false
	}
	defaultPort := "80"
	if scheme == "https" {
		defaultPort = "443"
	}
	originPort := u.Port()
	if originPort == "" {
		originPort = defaultPort
	}
	hostPort := requestHostPort(r.Host)
	if hostPort == "" {
		hostPort = defaultPort
	}
	return originPort == hostPort
}

func requestHostName(hostport string) string {
	if u, err := url.Parse("//" + hostport); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return strings.TrimSpace(hostport)
}

func requestHostPort(hostport string) string {
	if u, err := url.Parse("//" + hostport); err == nil {
		return u.Port()
	}
	return ""
}

// ForwardWebSocket handles an authenticated inbound WebSocket request.
//
// A WebSocket handshake cannot be transparently retried after it has been
// upgraded.  Therefore all Core-side admission checks (balance, user/API-key
// quota, route/account selection, middleware and account slot) happen before
// the upgrade.  Once upgraded, the selected plugin owns the bidirectional
// message loop; Core still applies the normal scheduler outcome and usage
// bookkeeping when that loop returns.
func (f *Forwarder) ForwardWebSocket(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	if f == nil || f.manager == nil {
		protocolError(c, http.StatusServiceUnavailable, "server_error", "plugin_unavailable", "插件系统未就绪")
		return
	}

	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	path := requestPath(c)
	requestedPlatform := requestedPlatform(c, keyInfo)
	inst := f.matchPlugin(c, keyInfo, requestedPlatform, path)
	if inst == nil {
		return
	}
	setRequestErrorFormat(c, f.manager.ErrorFormat(inst.Name, path))
	if !f.manager.PluginSupportsRoute(inst.Name, "WS", path) {
		protocolError(c, http.StatusNotFound, "invalid_request_error", "route_not_found", "当前平台不支持该 WebSocket 路径")
		return
	}

	// A model in the handshake query is the only model signal available before
	// the first application frame.  If it is absent, the scheduler is called
	// once with an empty model; the plugin still receives the unmodified frames.
	model := websocketRequestModel(c.Request)
	state := &forwardState{
		startedAt:         time.Now(),
		requestPath:       path,
		model:             model,
		schedulingModels:  websocketSchedulingModels(f.manager, requestedPlatform, inst.Name, path, model),
		stream:            true,
		realtime:          true,
		websocket:         true,
		requestedPlatform: requestedPlatform,
		accountReq:        accountRequirementsForRequest(f.manager, path, model, nil),
		keyInfo:           keyInfo,
		plugin:            inst,
	}
	if len(state.schedulingModels) > 0 {
		state.schedulingModel = state.schedulingModels[0]
	}

	c.Set(ginCtxKeyModel, state.model)
	c.Set(ginCtxKeyPlatform, state.plugin.Name)

	if !f.checkBalance(c, state) {
		return
	}

	releaseClientQuota := f.acquireClientQuota(c, state)
	if releaseClientQuota == nil {
		return
	}
	defer releaseClientQuota()

	// Relay hooks are body-only by contract.  Calling the common entry point is
	// harmless (the empty body is skipped) and keeps the lifecycle explicit for
	// future hook protocol versions.
	f.applyRelayHookV2(c, state)

	requirements := routing.Requirements{NeedsImage: requestNeedsImageCached(f.manager, state)}
	routes := routesForAPIKey(state, requirements)
	if len(routes) == 0 {
		if errResp, ok := apiKeyGroupRequirementError(state.keyInfo, requirements); ok {
			protocolError(c, errResp.status, errResp.errType, errResp.code, errResp.message)
			return
		}
		protocolError(c, http.StatusServiceUnavailable, "server_error", "no_available_route", "请求暂时无法完成，请稍后重试")
		return
	}
	state.selectedRoute = routes[0]
	state.keyInfo = keyInfoForRoute(state.keyInfo, state.selectedRoute)

	if err := f.pickAccount(c, state); err != nil {
		if errors.Is(err, scheduler.ErrNoAvailableAccount) {
			protocolError(c, http.StatusServiceUnavailable, "server_error", "no_available_account", "暂无可用上游账号，请稍后重试")
			return
		}
		protocolError(c, http.StatusServiceUnavailable, "server_error", "account_selection_failed", "上游账号选择失败，请稍后重试")
		return
	}

	releaseAccountSlot, ok := f.acquireAccountSlot(c, state)
	if !ok {
		protocolError(c, http.StatusServiceUnavailable, "server_error", "account_capacity_exhausted", "上游账号当前繁忙，请稍后重试")
		return
	}
	releasedSlot := false
	releaseSlot := func() {
		if releasedSlot {
			return
		}
		releasedSlot = true
		releaseAccountSlot()
	}
	rpmSettled := false
	defer func() {
		releaseSlot()
		if !rpmSettled && f.scheduler != nil && state.account != nil {
			// No outcome reached scheduler.Apply (middleware deny, failed
			// handshake, panic, or cancellation before plugin dispatch).
			f.scheduler.DecrementRPM(context.Background(), state.account.ID)
		}
	}()

	// Keep the lifecycle aligned with HTTP Forward: middleware runs after an
	// account slot has been reserved, and a deny must undo both the slot and
	// the RPM reservation.
	allowed, metadata := f.runForwardBeginChain(c, state)
	if !allowed {
		releaseSlot()
		return
	}

	// If the handshake itself fails, no plugin outcome exists to feed into the
	// scheduler.  Undo the RPM reservation explicitly before returning HTTP.
	wsConn, err := websocketUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		releaseSlot()
		return
	}

	conn := newGorillaWebSocketConn(wsConn, websocketConnectInfo(c, state))
	defer func() {
		_ = wsConn.Close()
	}()

	start := time.Now()
	outcome, callErr := inst.Gateway.HandleWebSocket(c.Request.Context(), conn)
	execution := forwardExecution{
		outcome:  outcome,
		err:      callErr,
		duration: time.Since(start),
	}

	ctx := finalizeRequestContext(c.Request.Context())
	f.applyOutcome(ctx, state, execution)
	rpmSettled = true
	f.persistUpdatedCredentials(state.account.ID, execution.outcome.UpdatedCredentials)
	if execution.outcome.Usage != nil {
		f.recordUsage(c, state, execution)
	}
	f.runForwardEndChain(c, state, execution, metadata)

	// The HTTP response is already a WebSocket.  For a terminal plugin outcome
	// send a protocol close (and, when available, the plugin's JSON error event)
	// instead of attempting to write an HTTP error body after Upgrade.
	if callErr != nil || execution.outcome.Kind != sdk.OutcomeSuccess {
		writeWebSocketFailure(conn, execution)
	}

	c.Set(ginCtxKeyAccountID, state.account.ID)
	c.Set(ginCtxKeyAttempts, 1)
}

func websocketSchedulingModels(manager *Manager, platform, pluginName, path, model string) []string {
	if strings.TrimSpace(model) == "" {
		// The first WebSocket application frame may carry the model.  Keep the
		// empty candidate local to the WS path; ordinary HTTP model-less
		// requests retain their existing scheduling semantics.
		return []string{""}
	}
	return schedulingModelsForRequest(manager, platform, pluginName, path, model)
}

func websocketRequestModel(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("model"))
}

func websocketConnectInfo(c *gin.Context, state *forwardState) *sdk.WebSocketConnectInfo {
	path := ""
	query := ""
	remoteAddr := ""
	var source http.Header
	var keyInfo *auth.APIKeyInfo
	if c != nil && c.Request != nil {
		if c.Request.URL != nil {
			path = c.Request.URL.Path
			query = c.Request.URL.RawQuery
		}
		remoteAddr = c.Request.RemoteAddr
		source = c.Request.Header
	}
	if state != nil && state.requestPath != "" {
		path = state.requestPath
	}
	if state != nil {
		keyInfo = state.keyInfo
	}

	connectionID := middleware.RequestIDFromGinContext(c)
	if connectionID == "" {
		connectionID = uuid.NewString()
	}

	var account *sdk.Account
	if state != nil && state.account != nil {
		account = buildSDKAccount(state.account)
	}
	return &sdk.WebSocketConnectInfo{
		Path:         path,
		Query:        query,
		Headers:      buildWebSocketHeaders(source, keyInfo),
		RemoteAddr:   remoteAddr,
		ConnectionID: connectionID,
		Account:      account,
	}
}

// buildWebSocketHeaders copies ordinary client metadata while removing
// credentials and HTTP/WebSocket hop-by-hop state.  The gorilla dialer in a
// gateway plugin creates a fresh upstream handshake, so forwarding the
// client's Sec-WebSocket-Key/Version/Extensions would be incorrect.
func buildWebSocketHeaders(source http.Header, keyInfo *auth.APIKeyInfo) http.Header {
	headers := buildHeaders(source, keyInfo)
	for key := range headers {
		lowerKey := strings.ToLower(key)
		if strings.HasPrefix(lowerKey, "sec-websocket-") {
			delete(headers, key)
			continue
		}
		switch lowerKey {
		case "authorization", "x-api-key", "cookie", "host",
			"connection", "keep-alive", "proxy-connection",
			"proxy-authenticate", "proxy-authorization", "te", "trailer",
			"transfer-encoding", "content-length", "upgrade", "sec-websocket-key",
			"sec-websocket-version", "sec-websocket-extensions",
			"x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
			"x-forwarded-port", "x-forwarded-prefix", "forwarded":
			delete(headers, key)
		}
	}
	return headers
}

// gorillaWebSocketConn adapts gorilla's framing API to the SDK contract.
type gorillaWebSocketConn struct {
	conn *websocket.Conn
	info *sdk.WebSocketConnectInfo
}

func newGorillaWebSocketConn(conn *websocket.Conn, info *sdk.WebSocketConnectInfo) *gorillaWebSocketConn {
	return &gorillaWebSocketConn{conn: conn, info: info}
}

func (c *gorillaWebSocketConn) ReadMessage() (int, []byte, error) {
	if c == nil || c.conn == nil {
		return 0, nil, errors.New("websocket connection is nil")
	}
	messageType, data, err := c.conn.ReadMessage()
	if err != nil {
		return 0, nil, err
	}
	switch messageType {
	case websocket.TextMessage:
		return sdk.WSMessageText, data, nil
	case websocket.BinaryMessage:
		return sdk.WSMessageBinary, data, nil
	default:
		return 0, nil, fmt.Errorf("unsupported websocket message type: %d", messageType)
	}
}

func (c *gorillaWebSocketConn) WriteMessage(messageType int, data []byte) error {
	if c == nil || c.conn == nil {
		return errors.New("websocket connection is nil")
	}
	var wireType int
	switch messageType {
	case sdk.WSMessageText:
		wireType = websocket.TextMessage
	case sdk.WSMessageBinary:
		wireType = websocket.BinaryMessage
	default:
		return fmt.Errorf("unsupported sdk websocket message type: %d", messageType)
	}
	return c.conn.WriteMessage(wireType, data)
}

func (c *gorillaWebSocketConn) ConnectInfo() *sdk.WebSocketConnectInfo {
	if c == nil {
		return nil
	}
	return c.info
}

func (c *gorillaWebSocketConn) Close(code int, reason string) error {
	if c == nil || c.conn == nil {
		return nil
	}
	if code <= 0 {
		code = websocket.CloseNormalClosure
	}
	// FormatCloseMessage applies the RFC payload limit.  Ignore a best-effort
	// close-frame write error and still close the underlying socket.
	_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
	return c.conn.Close()
}

func writeWebSocketFailure(conn *gorillaWebSocketConn, execution forwardExecution) {
	if conn == nil {
		return
	}
	if body := execution.outcome.Upstream.Body; len(body) > 0 && execution.outcome.Kind == sdk.OutcomeClientError {
		_ = conn.WriteMessage(sdk.WSMessageText, body)
	}

	code := websocket.CloseInternalServerErr
	switch execution.outcome.Kind {
	case sdk.OutcomeClientError:
		code = websocket.ClosePolicyViolation
	case sdk.OutcomeAccountRateLimited, sdk.OutcomeUpstreamTransient:
		code = websocket.CloseTryAgainLater
	case sdk.OutcomeAccountDead:
		code = websocket.CloseInternalServerErr
	case sdk.OutcomeUnknown:
		if errors.Is(execution.err, sdk.ErrNotSupported) {
			code = websocket.CloseUnsupportedData
		}
	}
	reason := strings.TrimSpace(execution.outcome.Reason)
	if reason == "" {
		reason = "upstream websocket request failed"
	}
	_ = conn.Close(code, truncateWebSocketCloseReason(reason))
}

// truncateWebSocketCloseReason keeps the close control frame within the
// RFC 6455 payload limit (125 bytes, including the two-byte status code) and
// never cuts a UTF-8 sequence in half.  Reasons can originate from an
// upstream/plugin and therefore must be bounded before FormatCloseMessage.
func truncateWebSocketCloseReason(reason string) string {
	const maxReasonBytes = 125 - 2
	if reason == "" {
		return ""
	}
	reason = strings.ToValidUTF8(reason, "")
	if len(reason) <= maxReasonBytes {
		return reason
	}
	b := []byte(reason[:maxReasonBytes])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}
