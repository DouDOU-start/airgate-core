package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/clientid"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/requestaudit"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

const (
	websocketFirstFrameTimeout = 10 * time.Second
	codexWebSocketWriteTimeout = 30 * time.Second
	// Give a native executor a short window to consume a downstream close
	// frame and complete its upstream close handshake.  A plugin which does
	// not observe the close (or ignores the frame queue) must still be
	// cancelled so the HTTP handler cannot remain blocked forever.
	// Keep a small bounded burst buffer between the downstream reader and the
	// native executor.  A bounded queue prevents an unresponsive plugin from
	// turning one WebSocket into an unbounded memory consumer; enqueue failures
	// cancel the session instead of silently dropping protocol frames.
	codexWebSocketFrameQueueSize = 32
)

var codexWebSocketCloseGracePeriod = time.Second

var errCodexWebSocketFrameQueueFull = errors.New("codex websocket frame queue is full")
var errCodexWebSocketFrameQueueClosed = errors.New("codex websocket frame queue is closed")
var errWebsocketNamedStreamLimit = errors.New("websocket named stream limit reached")
var errWebsocketResponseTurnQueueClosed = errors.New("websocket response turn queue is closed")
var errWebsocketMalformedStreamID = errors.New("websocket response terminal has malformed stream_id")

const codexWebSocketMaxNamedStreams = 32

// codexWebSocketFrameQueue serializes the bounded input channel's producers
// with its close operation. Gorilla may invoke ping/pong/close handlers from
// the downstream reader goroutine while that same reader is returning and
// closing the channel; a bare `select { case ch <- frame: }` is racy because a
// send can panic after close even when the channel has capacity.
type codexWebSocketFrameQueue struct {
	mu     sync.Mutex
	ch     chan protocol.CodexWebSocketFrame
	closed bool
}

func newCodexWebSocketFrameQueue(size int) *codexWebSocketFrameQueue {
	if size < 1 {
		size = 1
	}
	return &codexWebSocketFrameQueue{ch: make(chan protocol.CodexWebSocketFrame, size)}
}

func (q *codexWebSocketFrameQueue) send(ctx context.Context, frame protocol.CodexWebSocketFrame) error {
	if q == nil {
		return errCodexWebSocketFrameQueueClosed
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errCodexWebSocketFrameQueueClosed
	}
	select {
	case q.ch <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errCodexWebSocketFrameQueueFull
	}
}

func (q *codexWebSocketFrameQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.ch)
}

var codexWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 << 10,
	WriteBufferSize: 16 << 10,
	// The CLI does not send a browser Origin. Keep Gorilla's default origin
	// policy for browser callers instead of disabling CSRF protection globally.
}

type codexWebSocketRoute struct {
	endpoint              string
	providerPath          string
	requireResponseCreate bool
	// callID is the validated opaque Realtime call identifier used to resolve
	// account affinity before the sideband websocket is opened. It is kept on
	// the route because the generic websocket handler otherwise has no local
	// call-id variable (and must not re-parse the raw path after validation).
	callID string
	// remoteControl marks the app-server Remote Control websocket.  It is a
	// distinct protocol from Responses/Guardian: the first frame is an
	// already-formed JSON-RPC envelope, the enrolled server bearer is used for
	// upstream authentication, and no Responses enhancement/beta headers may
	// be applied.
	remoteControl bool
	displayName   string
}

// HandleResponsesWebSocket serves the native Codex Responses WebSocket. The
// API-key middleware runs before this handler; account selection is delayed
// until the first response.create frame reveals the requested model.
func (p *Pipeline) HandleResponsesWebSocket(c *gin.Context) {
	p.handleCodexWebSocket(c, codexWebSocketRoute{
		endpoint: adaptor.EndpointResponses, providerPath: "/responses",
		requireResponseCreate: true, displayName: "Responses",
	})
}

// HandleCodexGuardianWebSocket serves the native Guardian approval-review
// Responses WebSocket.  Guardian is a dedicated control-plane route rather
// than an ordinary Responses request, so the route remains native-only and
// receives endpoint-specific headers/body normalization in the shared relay.
func (p *Pipeline) HandleCodexGuardianWebSocket(c *gin.Context) {
	p.handleCodexWebSocket(c, codexWebSocketRoute{
		endpoint: adaptor.EndpointGuardian, providerPath: "/guardian",
		requireResponseCreate: true, displayName: "Guardian",
	})
}

// HandleCodexGuardianClassifierWebSocket serves the lightweight Guardian
// risk-classifier Responses WebSocket used by the official async scorer.
func (p *Pipeline) HandleCodexGuardianClassifierWebSocket(c *gin.Context) {
	p.handleCodexWebSocket(c, codexWebSocketRoute{
		endpoint: adaptor.EndpointGuardianClassifier, providerPath: "/guardian-classifier",
		requireResponseCreate: true, displayName: "Guardian classifier",
	})
}

// HandleCodexRemoteControlWebSocket serves the official app-server Remote
// Control duplex endpoint.  Authentication is performed by the dedicated
// CodexRemoteControlAuth middleware installed by the router; this handler only
// verifies the bound enrollment metadata and relays the JSON-RPC data plane.
func (p *Pipeline) HandleCodexRemoteControlWebSocket(c *gin.Context) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return
	}
	// Keep this endpoint finite even when a caller reaches the handler through a
	// custom router.  The public aliases normalize to the same canonical path,
	// while an arbitrary /remote/control path must never become a websocket
	// proxy.
	canonical := codexRemoteControlCanonicalPath(strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/"))
	if canonical != "/remote/control/server" {
		writeError(c, http.StatusNotFound, "invalid_request_error", "unsupported_endpoint", "unsupported Codex remote-control websocket endpoint")
		return
	}
	token := contextString(c, middleware.CtxKeyCodexRemoteControlToken)
	if token == "" {
		// The middleware normally sets this context.  Keep the explicit header
		// fallback for embedders that perform authentication in an outer layer.
		token = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Token", "X-Remote-Control-Token")
	}
	if token == "" {
		writeError(c, http.StatusUnauthorized, "authentication_error", "missing_remote_control_token", "Remote Control token is required")
		return
	}
	if c.GetInt(middleware.CtxKeyCodexRemoteControlAccountID) <= 0 {
		writeError(c, http.StatusForbidden, "authentication_error", "remote_control_binding_missing", "Remote Control token is not bound to a Codex account")
		return
	}
	if _, _, err := codexRemoteControlWebSocketOptionalHeaders(c); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_remote_control_header", err.Error())
		return
	}
	p.handleCodexWebSocket(c, codexWebSocketRoute{
		endpoint: adaptor.EndpointCodexRemoteControlServerWebSocket,
		// OAuth ChatGPT backend management routes are rooted at /wham after
		// codexProviderBaseURLForAccount strips the terminal /codex segment.
		providerPath:  "/wham/remote/control/server",
		remoteControl: true,
		displayName:   "Remote Control",
	})
}

// HandleRealtimeSidebandWebSocket relays an already-created Realtime call's
// control/data WebSocket. Unlike Responses, the sideband protocol has no
// response.create bootstrap frame: Core must select a native account before
// upgrading and start the provider connection immediately.
func (p *Pipeline) HandleRealtimeSidebandWebSocket(c *gin.Context) {
	callID, err := realtimeSidebandCallIDChecked(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type": "invalid_request_error", "code": "invalid_call_id", "message": err.Error(),
		}})
		return
	}
	providerPath := "/realtime"
	if c.Param("call_id") != "" {
		providerPath = "/live/" + url.PathEscape(callID)
	}
	p.handleCodexWebSocket(c, codexWebSocketRoute{
		endpoint: adaptor.EndpointRealtimeSideband, providerPath: providerPath, callID: callID,
		displayName: "Realtime sideband",
	})
}

func (p *Pipeline) handleCodexWebSocket(c *gin.Context, route codexWebSocketRoute) {
	clientid.Detect(c)
	// Responses, Guardian and Realtime sideband are Codex-owned websocket
	// protocols.  Their public aliases overlap the ordinary OpenAI/ChatGPT
	// paths (for example /v1/responses and /realtime), so the route shape alone
	// must not opt a normal caller into the native Codex account plane.  Remote
	// Control is deliberately exempt: it has its own enrolled-bearer
	// authentication boundary and is not exposed through the API-key aliases.
	if !route.remoteControl && !isCodexClientRequest(c) {
		writeCodexWebSocketUpgradeRequired(c, route.displayName)
		return
	}
	clientType := relayClientType(c)
	ordinaryResponses := route.endpoint == adaptor.EndpointResponses
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	if p == nil || p.accounts == nil || p.providerTransport == nil || p.concurrency == nil || p.rpm == nil {
		writeCodexWebSocketUpgradeRequired(c, route.displayName)
		return
	}
	// Ordinary Responses WebSockets are billable API requests.  Run the same
	// client/balance/rate/concurrency gates as HTTP before committing 101.  The
	// first client slot is reserved here and transferred to the first
	// response.create turn after its model is available.  Guardian, Realtime
	// sideband and Remote Control are control-plane protocols and intentionally
	// do not inherit these checks.
	var firstClientRelease func()
	if ordinaryResponses {
		firstClientRelease, ok = p.preflightResponsesWebSocket(c, keyInfo, clientType)
		if !ok {
			return
		}
		defer func() {
			if firstClientRelease != nil {
				firstClientRelease()
			}
		}()
	}
	// CPA translation is an HTTP/SSE contract. A WebSocket client cannot be
	// converted after the 101 response, so reject it before upgrading and let
	// the official CLI retry over HTTP/SSE.
	mode := p.codexTransportMode()
	if codexWebSocketBlockedByTransportMode(route, mode) {
		writeCodexWebSocketUpgradeRequired(c, route.displayName)
		return
	}
	// A WebSocket client cannot fall back to HTTP after a 101 response.  Do a
	// cheap group-level preflight before upgrading so CPA-only deployments and
	// older HTTP/SSE-only plugins receive the official 426 signal instead of a
	// post-upgrade close that the Codex CLI treats as a hard stream failure.
	if capabilities, ok := p.providerTransport.(providertransport.CapabilityProvider); ok && !capabilities.Capabilities().WebSocket {
		writeCodexWebSocketUpgradeRequired(c, route.displayName)
		return
	}
	if !route.remoteControl && !p.hasNativeCodexAccount(keyInfo.GroupID) {
		writeCodexWebSocketUpgradeRequired(c, route.displayName)
		return
	}
	// Newer Codex clients include the requested model in the routing-hint
	// handshake header (for example, "model=gpt-5-codex;tier=..."), while the
	// actual response.create frame still arrives only after HTTP 101.  When the
	// hint is available we can avoid upgrading a socket that can never be
	// served natively (e.g. the model is CPA-only), allowing the official CLI to
	// switch to HTTP/SSE immediately.  Clients without a hint retain the
	// model-from-first-frame flow below.
	model := websocketRequestModelHint(c)
	hintedModel := model
	var acc *accountreg.Snapshot
	if route.remoteControl {
		accountID := c.GetInt(middleware.CtxKeyCodexRemoteControlAccountID)
		if accountID <= 0 {
			writeError(c, http.StatusForbidden, "authentication_error", "remote_control_binding_missing", "Remote Control token is not bound to a Codex account")
			return
		}
		var boundOK bool
		if target, ok := p.pickBoundNativeCodexAccount(keyInfo.GroupID, "", accountID); ok && target.account != nil && providertransport.IsCodexOAuthAuthType(target.account.Type) {
			acc = target.account
			boundOK = true
			model = websocketAccountRoutingModel(acc, "")
		}
		if !boundOK {
			writeCodexWebSocketUpgradeRequired(c, route.displayName)
			return
		}
	} else if route.requireResponseCreate {
		if ordinaryResponses && model != "" {
			acc = p.pickNativeCodexAccount(keyInfo.GroupID, model)
			if acc == nil {
				writeCodexWebSocketUpgradeRequired(c, route.displayName)
				return
			}
		} else if model != "" && !p.hasNativeCodexAccountForModel(keyInfo.GroupID, model) {
			writeCodexWebSocketUpgradeRequired(c, route.displayName)
			return
		}
	} else {
		// A Realtime call is scoped to the credential that created it. Prefer
		// the call_id affinity recorded from POST /realtime/calls; falling back
		// to another account when a binding exists but is unavailable would be a
		// guaranteed cross-account join failure and must fail closed.
		if bound, found := p.realtimeCallAccount(keyInfo.UserID, keyInfo.GroupID, route.callID); found {
			acc = bound
			model = websocketAccountRoutingModel(acc, model)
		} else if model != "" {
			acc = p.pickNativeCodexAccount(keyInfo.GroupID, model)
		} else {
			acc, model = p.pickAnyNativeCodexAccount(keyInfo.GroupID)
		}
		if acc == nil {
			writeCodexWebSocketUpgradeRequired(c, route.displayName)
			return
		}
	}
	// Keep the audit skeleton and executor context available before a prepared
	// upstream handshake.  A downstream WebSocket 101 is irreversible, so a
	// native executor must prove that its own handshake succeeded first.
	start := time.Now()
	var audit *requestaudit.Handle
	auditStatus := http.StatusSwitchingProtocols
	var auditResponseBytes int64
	auditCompleted := false
	defer func() {
		if audit != nil {
			audit.Finish(auditStatus, auditResponseBytes, auditCompleted)
		}
	}()

	transport, transportOK := p.providerTransport.(providertransport.CodexWebSocketTransport)
	if !transportOK {
		writeCodexWebSocketUpgradeRequired(c, route.displayName)
		return
	}
	prepared := (ordinaryResponses && model != "") || !route.requireResponseCreate
	var err error
	var sessionRequestID string
	var sessionRPMMinute int64
	var sessionProviderStarted bool
	defer func() {
		if ordinaryResponses || sessionRequestID == "" || acc == nil {
			return
		}
		p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, sessionRequestID)
		if !sessionProviderStarted && sessionRPMMinute > 0 {
			p.rpm.DecrementAccountRPM(context.Background(), acc.ID, sessionRPMMinute)
		}
	}()
	var preparedAccountRequestID string
	var preparedAccountRPMMinute int64
	preparedAccountReserved := false
	defer func() {
		if !preparedAccountReserved || acc == nil {
			return
		}
		p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, preparedAccountRequestID)
		if preparedAccountRPMMinute > 0 {
			p.rpm.DecrementAccountRPM(context.Background(), acc.ID, preparedAccountRPMMinute)
		}
	}()
	var preparedSession *codexPreparedWebSocket
	var preparedHeader http.Header
	var preparedContext context.Context
	var preparedCancel context.CancelFunc
	var preparedFrameQueue *codexWebSocketFrameQueue
	preparedFinished := false
	defer func() {
		if preparedCancel != nil {
			preparedCancel()
		}
	}()
	defer func() {
		if preparedSession == nil || preparedFinished {
			return
		}
		if preparedFrameQueue != nil {
			preparedFrameQueue.close()
		}
		if preparedCancel != nil {
			preparedCancel()
		}
		refreshed, _ := preparedSession.cancelAndDrain()
		if len(refreshed) > 0 && acc != nil && p.accounts != nil {
			p.accounts.UpdateCredentials(acc.ID, refreshed)
		}
	}()
	if prepared && transportOK {
		// Control-plane sessions own an account slot for their whole lifetime;
		// acquire it before dialing upstream so a successful provider handshake
		// can never outrun Core's capacity gate.
		if !ordinaryResponses {
			sessionRequestID, sessionRPMMinute, _, ok = p.acquireAccountSlots(c.Request.Context(), acc, true)
			if !ok {
				auditStatus = http.StatusServiceUnavailable
				writeCodexWebSocketUpgradeRequired(c, route.displayName)
				return
			}
		} else {
			if _, priced := p.responsesWebSocketPrice(model); !priced {
				writeError(c, http.StatusBadRequest, "invalid_request_error", "model_price_not_configured", "model price is not configured")
				return
			}
			preparedAccountRequestID, preparedAccountRPMMinute, ok = p.acquireResponsesWebSocketAccountTurn(c.Request.Context(), acc, false)
			if !ok {
				writeTemporaryUnavailableError(c, "account_capacity_exhausted", "Codex account capacity is exhausted", time.Second)
				return
			}
			preparedAccountReserved = true
		}
		if p.requestAudit != nil {
			// The first Responses frame is sent only after HTTP 101.  For a
			// prepared session the audit skeleton therefore starts with an empty
			// body; the provider handshake still remains covered by the audit row.
			audit, err = p.requestAudit.StartFast(c.Request.Context(), requestaudit.RequestInput{
				RequestID: requestIDOf(c), UserID: keyInfo.UserID, UserEmail: keyInfo.UserEmail,
				APIKeyID: keyInfo.KeyID, GroupID: keyInfo.GroupID, Client: clientType,
				Protocol: registry.ProtocolOpenAI, Endpoint: route.endpoint, Model: model, Stream: true,
				Method: http.MethodGet, Path: c.Request.URL.Path, RawQuery: c.Request.URL.RawQuery,
				Host: c.Request.Host, RequestProto: c.Request.Proto, RemoteAddr: c.Request.RemoteAddr,
				IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(), Headers: c.Request.Header,
				Body: nil, BodyImmutable: true,
			})
			if err != nil {
				auditStatus = http.StatusInternalServerError
				writeError(c, http.StatusInternalServerError, "server_error", "audit_unavailable", "audit unavailable")
				return
			}
		}
		var upstreamAudit providertransport.UpstreamAuditSink
		if audit != nil {
			upstreamAudit = newNativeAuditSink(audit, requestaudit.Target{
				RouteKind: "account", AccountID: acc.ID, AccountName: acc.Name,
				AccountEmail: accountEmail(acc), AccountPlatform: acc.Platform, AccountType: acc.Type,
			})
		}
		preparedContext, preparedCancel = context.WithCancel(c.Request.Context())
		// Keep an unconditional cancellation defer adjacent to the allocation.
		// The guarded cleanup below handles the executor drain, while this direct
		// defer guarantees every early-return path releases the child context.
		defer preparedCancel()
		preparedFrameQueue = newCodexWebSocketFrameQueue(codexWebSocketFrameQueueSize)
		preparedRequest := codexWebSocketProviderRequest(c, route, acc, keyInfo, clientType, model, start, upstreamAudit)
		preparedSession = startCodexPreparedWebSocket(preparedContext, transport, preparedRequest, preparedFrameQueue.ch)
		headerFrame, prepareErr := preparedSession.waitHandshake(c.Request.Context())
		if prepareErr != nil {
			if preparedCancel != nil {
				preparedCancel()
			}
			auditStatus = http.StatusBadGateway
			writeCodexWebSocketPrepareFailure(c, route.displayName, prepareErr)
			return
		}
		preparedHeader = projectCodexWebSocketHandshakeHeaders(headerFrame.Header)
	}
	upgradeHeaders := preparedHeader
	ws, err := codexWebSocketUpgrader.Upgrade(c.Writer, c.Request, upgradeHeaders)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(32 << 20)
	messageType := websocket.TextMessage
	var first []byte
	var responseTurns *websocketResponseTurnQueue
	responseReaderDone := make(chan struct{})
	responseReaderStarted := false
	if ordinaryResponses {
		responseTurns = newWebsocketResponseTurnQueue()
		defer func() {
			// Stop accepting response.create frames before waiting for the
			// downstream reader.  The reader is a separate goroutine and may be
			// delayed briefly after the socket is closed; without this guard it
			// could enqueue a turn after the final popAll, leaking its slots.
			responseTurns.close()
			if responseReaderStarted {
				select {
				case <-responseReaderDone:
				case <-time.After(time.Second):
				}
			}
			for _, turn := range responseTurns.popAll() {
				// A turn which was queued locally but never handed to the
				// executor must not consume the account RPM reservation.  This
				// matters on pre-101 validation/audit failures as well as when a
				// provider exits while the downstream reader is still unwinding.
				p.releaseWebSocketResponseTurn(turn, !turn.accountProviderStarted)
			}
		}()
	}
	if route.requireResponseCreate {
		_ = ws.SetReadDeadline(time.Now().Add(websocketFirstFrameTimeout))
		messageType, first, err = ws.ReadMessage()
		if err != nil || (messageType != websocket.TextMessage && messageType != websocket.BinaryMessage) {
			code := websocket.CloseProtocolError
			if ordinaryResponses {
				code = websocket.ClosePolicyViolation
			}
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "first frame must be response.create"), time.Now().Add(time.Second))
			return
		}
		// A prepared Responses session created its audit skeleton before the
		// downstream 101 so the provider handshake could be covered by an
		// upstream attempt.  The actual client payload is only available now;
		// replace the skeleton body as soon as the first frame arrives, before
		// any validation can return early.  This keeps malformed/mismatched
		// first frames auditable as well as successful turns.
		if ordinaryResponses && preparedSession != nil && audit != nil {
			audit.UpdateInboundBody(first)
		}
		model = websocketResponseCreateModel(first)
		if model == "" {
			code := websocket.CloseProtocolError
			if ordinaryResponses {
				code = websocket.ClosePolicyViolation
			}
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "response.create model is required"), time.Now().Add(time.Second))
			return
		}
		if preparedSession != nil && hintedModel != "" && model != hintedModel {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "response.create model does not match the routing hint"), time.Now().Add(time.Second))
			return
		}
		_ = ws.SetReadDeadline(time.Time{})
		firstPrice := pricing.Price{}
		if ordinaryResponses {
			if _, streamErr := websocketResponseCreateStreamID(first); streamErr != nil {
				_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid response.create stream_id"), time.Now().Add(time.Second))
				return
			}
			var priced bool
			firstPrice, priced = p.responsesWebSocketPrice(model)
			if !priced {
				_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "model price is not configured"), time.Now().Add(time.Second))
				return
			}
		}

		if preparedSession == nil {
			acc = p.pickNativeCodexAccount(keyInfo.GroupID, model)
		}
		if acc == nil {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "no native Codex account available"), time.Now().Add(time.Second))
			return
		}
		if ordinaryResponses {
			accountRequestID := preparedAccountRequestID
			accountRPMMinute := preparedAccountRPMMinute
			if !preparedAccountReserved {
				var accountSlotOK bool
				accountRequestID, accountRPMMinute, accountSlotOK = p.acquireResponsesWebSocketAccountTurn(c.Request.Context(), acc, websocketResponseCreateWarmup(first))
				if !accountSlotOK {
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "account capacity exhausted"), time.Now().Add(time.Second))
					return
				}
			} else if websocketResponseCreateWarmup(first) && accountRPMMinute > 0 {
				// The reservation conservatively charged RPM before Core could see
				// generate=false. Warmups retain the account slot but never consume
				// the inference RPM budget.
				p.rpm.DecrementAccountRPM(context.Background(), acc.ID, accountRPMMinute)
				accountRPMMinute = 0
			}
			if _, pushErr := responseTurns.push(first, time.Now(), func(turn *websocketResponseCreateEvent) {
				turn.price, turn.priced = firstPrice, true
				turn.clientRelease = firstClientRelease
				turn.accountID = acc.ID
				turn.accountRequest = accountRequestID
				turn.accountRPMMinute = accountRPMMinute
			}); pushErr != nil {
				preparedAccountReserved = false
				p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, accountRequestID)
				p.rpm.DecrementAccountRPM(context.Background(), acc.ID, accountRPMMinute)
				_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid response.create frame"), time.Now().Add(time.Second))
				return
			}
			preparedAccountReserved = false // ownership transferred to responseTurns
			firstClientRelease = nil        // ownership transferred to the queued turn
		}
	}
	if !ordinaryResponses {
		if sessionRequestID == "" {
			sessionRequestID, sessionRPMMinute, _, ok = p.acquireAccountSlots(c.Request.Context(), acc, true)
			if !ok {
				_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "account capacity exhausted"), time.Now().Add(time.Second))
				return
			}
		}
	}

	if p.requestAudit != nil && audit == nil {
		audit, err = p.requestAudit.StartFast(c.Request.Context(), requestaudit.RequestInput{
			RequestID: requestIDOf(c), UserID: keyInfo.UserID, UserEmail: keyInfo.UserEmail,
			APIKeyID: keyInfo.KeyID, GroupID: keyInfo.GroupID, Client: clientType,
			Protocol: registry.ProtocolOpenAI, Endpoint: route.endpoint, Model: model, Stream: true,
			Method: http.MethodGet, Path: c.Request.URL.Path, RawQuery: c.Request.URL.RawQuery,
			Host: c.Request.Host, RequestProto: c.Request.Proto, RemoteAddr: c.Request.RemoteAddr,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(), Headers: c.Request.Header,
			Body: first, BodyImmutable: true,
		})
		if err != nil {
			// The first response.create has already been queued so its client
			// and account reservations are owned by responseTurns.  Audit
			// creation happens before the frame is handed to the executor;
			// remove that exact lane now and roll its RPM reservation back.
			if ordinaryResponses && responseTurns != nil {
				if turn, found := responseTurns.pop(websocketResponseStreamID(first)); found {
					p.releaseWebSocketResponseTurn(turn, true)
				}
			}
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "audit unavailable"), time.Now().Add(time.Second))
			return
		}
	}
	var upstreamAudit providertransport.UpstreamAuditSink
	if audit != nil {
		upstreamAudit = newNativeAuditSink(audit, requestaudit.Target{
			RouteKind: "account", AccountID: acc.ID, AccountName: acc.Name,
			AccountEmail: accountEmail(acc), AccountPlatform: acc.Platform, AccountType: acc.Type,
		})
	}

	var ctx context.Context
	var cancel context.CancelFunc
	var frameQueue *codexWebSocketFrameQueue
	if preparedSession != nil {
		ctx = preparedContext
		cancel = preparedCancel
		frameQueue = preparedFrameQueue
	} else {
		ctx, cancel = context.WithCancel(c.Request.Context())
		frameQueue = newCodexWebSocketFrameQueue(codexWebSocketFrameQueueSize)
	}
	defer cancel()
	frames := frameQueue.ch
	if route.requireResponseCreate {
		firstKind := protocol.CodexWebSocketFrameText
		if messageType == websocket.BinaryMessage {
			firstKind = protocol.CodexWebSocketFrameBinary
		}
		firstData := first
		if messageType == websocket.TextMessage && isCodexGuardianWebSocketEndpoint(route.endpoint) {
			firstData = normalizeCodexGuardianResponseCreate(first)
		}
		if ordinaryResponses {
			// Mark ownership before the non-blocking hand-off.  If the send
			// itself fails, the error path below explicitly rolls the turn back;
			// otherwise a concurrent provider return could drain an unmarked turn
			// before this goroutine gets to update it.
			responseTurns.markProviderStarted(websocketResponseStreamID(first), "")
		}
		if err := frameQueue.send(context.Background(), protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: firstKind, Data: append([]byte(nil), firstData...)}); err != nil {
			if ordinaryResponses {
				if turn, found := responseTurns.pop(websocketResponseStreamID(first)); found {
					p.releaseWebSocketResponseTurn(turn, true)
				}
			}
			auditStatus = http.StatusInternalServerError
			return
		}
	}
	// Closing the downstream socket when the request context is cancelled is
	// important: Gorilla's ReadMessage is otherwise allowed to remain blocked
	// forever while the provider/plugin side has already terminated.
	var closeSocketOnce sync.Once
	closeSocket := func() {
		closeSocketOnce.Do(func() { _ = ws.Close() })
	}
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeSocket()
		case <-watchDone:
		}
	}()
	defer func() {
		close(watchDone)
		closeSocket()
	}()
	var downstreamCloseOnce sync.Once
	var downstreamClosed atomic.Bool
	// A close control frame is first handed to the executor so it can perform
	// the upstream close handshake. Some third-party plugins, however, may
	// stop reading the frame stream after the peer has gone away. Schedule a
	// bounded cancellation in that case; the normal handler defer cancels the
	// context earlier when the executor returns on its own.
	var downstreamCloseCancelOnce sync.Once
	scheduleDownstreamCloseCancel := func() {
		downstreamCloseCancelOnce.Do(func() {
			timer := time.NewTimer(codexWebSocketCloseGracePeriod)
			go func() {
				defer timer.Stop()
				select {
				case <-ctx.Done():
				case <-timer.C:
					cancel()
				}
			}()
		})
	}
	clientCloseSent := false
	queueDownstreamControl := func(frame protocol.CodexWebSocketFrame) error {
		err := frameQueue.send(ctx, frame)
		if errors.Is(err, errCodexWebSocketFrameQueueFull) {
			// Do not block a Gorilla control handler (or the downstream reader)
			// behind a stalled executor. Every frame is significant, so fail
			// closed and let the session teardown propagate to both sides.
			cancel()
		}
		return err
	}
	forwardDownstreamClose := func(code int, reason string) {
		if err := queueDownstreamControl(protocol.CodexWebSocketFrame{
			Version: protocol.CodexExecutorVersion,
			Type:    protocol.CodexWebSocketFrameClose,
			Code:    code,
			Reason:  reason,
		}); err != nil {
			// queueDownstreamControl already cancels on a full queue. Cancel on
			// every other enqueue failure as well so a close that cannot be
			// forwarded never leaves the executor running indefinitely.
			cancel()
			return
		}
		// Let the executor consume the close frame first; if it does not
		// complete the session promptly, the grace timer below cancels it.
		scheduleDownstreamCloseCancel()
	}
	// Gorilla consumes control frames while reading unless handlers are
	// overridden. Forward them to the native executor as well as satisfying
	// the RFC-required ping/pong response locally. Close is sent upstream once;
	// the normal CloseError path below is deduplicated by the same Once.
	ws.SetPingHandler(func(appData string) error {
		if err := ws.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second)); err != nil {
			return err
		}
		return queueDownstreamControl(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFramePing, Data: []byte(appData)})
	})
	ws.SetPongHandler(func(appData string) error {
		return queueDownstreamControl(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFramePong, Data: []byte(appData)})
	})
	ws.SetCloseHandler(func(code int, reason string) error {
		downstreamClosed.Store(true)
		downstreamCloseOnce.Do(func() {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
			forwardDownstreamClose(code, reason)
		})
		return nil
	})
	responseReaderStarted = true
	go func() {
		defer close(responseReaderDone)
		defer frameQueue.close()
		for {
			t, data, readErr := ws.ReadMessage()
			if readErr != nil {
				var closeErr *websocket.CloseError
				if errors.As(readErr, &closeErr) {
					downstreamClosed.Store(true)
					downstreamCloseOnce.Do(func() {
						forwardDownstreamClose(closeErr.Code, closeErr.Text)
					})
					// Let the executor consume the close frame and perform its own
					// upstream close handshake. The request context will still cancel
					// if the peer disappears without a close frame.
					return
				}
				cancel()
				return
			}
			kind := protocol.CodexWebSocketFrameText
			if t == websocket.BinaryMessage {
				kind = protocol.CodexWebSocketFrameBinary
			} else if t == websocket.PingMessage {
				kind = protocol.CodexWebSocketFramePing
			} else if t == websocket.PongMessage {
				kind = protocol.CodexWebSocketFramePong
			}
			if t == websocket.TextMessage && isCodexGuardianWebSocketEndpoint(route.endpoint) {
				data = normalizeCodexGuardianResponseCreate(data)
			}
			if ordinaryResponses && route.requireResponseCreate && (t == websocket.TextMessage || t == websocket.BinaryMessage) &&
				websocketResponseEventType(data) == "response.create" {
				nextModel := websocketResponseCreateModel(data)
				if nextModel == "" {
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "response.create model is required"), time.Now().Add(time.Second))
					cancel()
					return
				}
				if !nativeCodexAccountSupportsWebSocketModel(acc, nextModel) {
					// A persistent socket cannot be moved to another account safely.
					// Ask the official client to reconnect so the normal model-aware
					// account picker can select a compatible Codex account.
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "model is not available on the selected Codex account"), time.Now().Add(time.Second))
					cancel()
					return
				}
				nextPrice, priced := p.responsesWebSocketPrice(nextModel)
				if !priced {
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "model price is not configured"), time.Now().Add(time.Second))
					cancel()
					return
				}
				clientRelease, limitCode := p.acquireResponsesWebSocketClientSlot(c, keyInfo, false)
				if limitCode != "" {
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, limitCode), time.Now().Add(time.Second))
					cancel()
					return
				}
				nextRequestID, nextRPMMinute, accountSlotOK := p.acquireResponsesWebSocketAccountTurn(ctx, acc, websocketResponseCreateWarmup(data))
				if !accountSlotOK {
					if clientRelease != nil {
						clientRelease()
					}
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "account request rate exhausted"), time.Now().Add(time.Second))
					cancel()
					return
				}
				// Seed the metadata before push.  Queue admission can fail before
				// its configure callback runs (notably when the distinct named
				// stream limit is reached), but the client/account reservations
				// below still belong to this attempted turn and must be released.
				turnMeta := websocketResponseCreateEvent{
					clientRelease:    clientRelease,
					accountID:        acc.ID,
					accountRequest:   nextRequestID,
					accountRPMMinute: nextRPMMinute,
				}
				streamID := websocketResponseStreamID(data)
				if _, pushErr := responseTurns.push(data, time.Now(), func(turn *websocketResponseCreateEvent) {
					turn.price, turn.priced = nextPrice, true
					turn.clientRelease = clientRelease
					turn.accountID = acc.ID
					turn.accountRequest = nextRequestID
					turn.accountRPMMinute = nextRPMMinute
					turnMeta = *turn
				}); pushErr != nil {
					if errors.Is(pushErr, errWebsocketNamedStreamLimit) {
						p.releaseWebSocketResponseTurn(turnMeta, true)
						if err := queueDownstreamControl(protocol.CodexWebSocketFrame{
							Version: protocol.CodexExecutorVersion,
							Type:    protocol.CodexWebSocketFrameText,
							Data:    websocketNamedStreamLimitError(streamID),
						}); err != nil {
							return
						}
						continue
					}
					if clientRelease != nil {
						clientRelease()
					}
					p.concurrency.ReleaseAccountSlot(context.Background(), acc.ID, nextRequestID)
					p.rpm.DecrementAccountRPM(context.Background(), acc.ID, nextRPMMinute)
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid response.create frame"), time.Now().Add(time.Second))
					cancel()
					return
				}
				responseTurns.markProviderStarted(streamID, turnMeta.accountRequest)
				if err := queueDownstreamControl(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: kind, Data: data}); err != nil {
					responseTurns.discardLast(streamID)
					p.releaseWebSocketResponseTurn(turnMeta, true)
					return
				}
				continue
			}
			if err := queueDownstreamControl(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: kind, Data: data}); err != nil {
				return
			}
		}
	}()

	providerRequest := codexWebSocketProviderRequest(c, route, acc, keyInfo, clientType, model, start, upstreamAudit)
	providerEmit := func(frame protocol.CodexWebSocketFrame) error {
		if frame.Type == protocol.CodexWebSocketFrameHeaders || frame.Type == protocol.CodexWebSocketFrameText || frame.Type == protocol.CodexWebSocketFrameBinary {
			if !ordinaryResponses {
				sessionProviderStarted = true
			}
		}
		if ordinaryResponses && frame.Type == protocol.CodexWebSocketFrameText && len(frame.Data) > 0 {
			if terminal, terminalEvent := websocketResponseTerminal(frame.Data); terminalEvent {
				malformedTerminal := terminal.streamIDMalformed
				if malformedTerminal {
					// An explicit malformed stream_id cannot be correlated safely.
					// Keep the turns queued so the common teardown path records them
					// as aborted and releases their resources; never fall back to the
					// implicit default lane.
					if err := writeCodexWebSocketFrame(ws, frame); err != nil {
						return err
					}
					auditResponseBytes += int64(len(frame.Data))
					return errWebsocketMalformedStreamID
				} else if terminal.connectionError {
					// An unscoped error terminates the connection-level request;
					// release every in-flight lane, but retain RPM because the
					// executor may already have sent the request upstream.
					for _, turn := range responseTurns.popAll() {
						p.releaseWebSocketResponseTurn(turn, !turn.accountProviderStarted)
					}
				} else if turn, found := responseTurns.pop(terminal.StreamID); found {
					if !turn.warmup && (terminal.Type == "response.completed" || terminal.Type == "response.incomplete") {
						billReq := turn.req
						if billReq == nil {
							billReq = &dto.ChatRequest{Model: turn.model, Stream: true}
						}
						billReq.Model = turn.model
						billReq.Stream = true
						var usage *dto.Usage
						if terminal.usageFound {
							u := terminal.Usage
							usage = &u
						}
						// Both completed and incomplete are explicit terminal
						// events. 无计量不落 usage_log。
						p.recordAccountUsage(c, keyInfo, acc, billReq, route.endpoint,
							attemptResult{usage: usage, written: true, done: true}, turn.start, turn.price)
					}
					// failed/error turns are intentionally not billed, but all
					// terminal classes release their client/account slots.
					p.releaseWebSocketResponseTurn(turn, false)
				}
			}
		}
		if err := writeCodexWebSocketFrame(ws, frame); err != nil {
			return err
		}
		if frame.Type == protocol.CodexWebSocketFrameClose {
			clientCloseSent = true
		}
		if frame.Type == protocol.CodexWebSocketFrameText || frame.Type == protocol.CodexWebSocketFrameBinary {
			auditResponseBytes += int64(len(frame.Data))
		}
		return nil
	}
	var result map[string]string
	var execErr error
	if preparedSession != nil {
		result, execErr = preparedSession.run(providerEmit)
		// The prepared session owns a child context that is no longer needed
		// once run has returned. Cancel it explicitly on both success and error
		// paths so static analysis and long-lived request contexts cannot retain
		// the executor's resources unnecessarily.
		if preparedCancel != nil {
			preparedCancel()
		}
		preparedFinished = true
	} else {
		result, execErr = transport.ExecuteWebSocket(ctx, providerRequest, frames, providerEmit)
	}
	if ordinaryResponses {
		// The executor cannot consume another response.create after returning.
		// Close the turn queue before inspecting pending lanes so a concurrent
		// downstream reader cannot race a late turn past the aborted-turn audit
		// and resource cleanup below. The deferred close remains as an idempotent
		// safety net for earlier returns.
		responseTurns.close()
	}
	if len(result) > 0 && p.accounts != nil {
		p.accounts.UpdateCredentials(acc.ID, result)
	}
	downstreamCanceled := downstreamClosed.Load() && !clientCloseSent
	if ordinaryResponses && responseTurns.pending() > 0 {
		// A provider return is not a successful Responses turn when one or more
		// lanes never emitted an explicit terminal event.  Record non-warmup
		// inference turns as aborted (using their creation-time price snapshot),
		// release all turn resources, and ensure a clean EOF cannot be audited as
		// completed. generate=false is connection setup, so it is released but
		// never creates a usage row.
		terminalErr := execErr
		if terminalErr == nil {
			terminalErr = errors.New("responses websocket ended before terminal event")
		}
		for _, turn := range responseTurns.popAll() {
			if !turn.warmup {
				billReq := turn.req
				if billReq == nil {
					billReq = &dto.ChatRequest{Model: turn.model, Stream: true}
				}
				p.recordAccountUsage(c, keyInfo, acc, billReq, route.endpoint,
					attemptResult{written: true, done: false, streamErr: terminalErr}, turn.start, turn.price)
			}
			p.releaseWebSocketResponseTurn(turn, !turn.accountProviderStarted)
		}
		if execErr == nil && !downstreamCanceled {
			execErr = terminalErr
		}
	}
	if execErr == nil && !clientCloseSent && !downstreamClosed.Load() {
		// A provider may end a WebSocket cleanly without sending an explicit
		// close control frame.  Emit a normal close frame so the official CLI
		// observes a protocol-level shutdown instead of an EOF from ws.Close().
		if err := ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err == nil {
			clientCloseSent = true
		}
	}
	if downstreamCanceled {
		auditStatus = statusClientClosedRequest
	} else if execErr == nil {
		auditCompleted = true
	} else if ctx.Err() != nil {
		auditStatus = statusClientClosedRequest
	} else {
		auditStatus = http.StatusBadGateway
		code, reason := codexWebSocketCloseForError(execErr)
		if !clientCloseSent {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
		}
	}
}

// codexWebSocketBlockedByTransportMode keeps the plugin-wide translation
// policy fail-closed before Core commits the irreversible downstream 101.
// cpa_only disables every native Codex websocket, including Remote Control;
// that control socket has no CPA wire contract and must not bypass the policy
// merely because it authenticates with an enrolled server bearer. In
// cpa_translate, only Responses is forced onto the HTTP/SSE translation plane;
// native-only websocket contracts such as Realtime and Remote Control remain
// available.
func codexWebSocketBlockedByTransportMode(route codexWebSocketRoute, mode providertransport.CodexTransportMode) bool {
	switch providertransport.NormalizeCodexTransportMode(string(mode)) {
	case providertransport.CodexModeCPAOnly:
		return true
	case providertransport.CodexModeCPATranslate:
		return route.endpoint == adaptor.EndpointResponses
	default:
		return false
	}
}

func codexWebSocketCloseForError(err error) (int, string) {
	if err == nil {
		return websocket.CloseNormalClosure, ""
	}
	var pluginErr *providertransport.CodexPluginError
	if errors.As(err, &pluginErr) {
		info := pluginErr.Info
		switch {
		case info.Code == "invalid_frame" || info.Code == "unsupported_version":
			return websocket.CloseProtocolError, "invalid websocket frame"
		case info.Code == "unsupported_capability" || info.Code == "unsupported_transport" || info.UpstreamStatus == http.StatusUnauthorized || info.UpstreamStatus == http.StatusTooManyRequests || info.UpstreamStatus >= 500:
			return websocket.CloseTryAgainLater, "native Codex transport unavailable"
		default:
			return websocket.CloseInternalServerErr, "native Codex transport failed"
		}
	}
	if errors.Is(err, providertransport.ErrCodexPluginUnavailable) || errors.Is(err, providertransport.ErrCodexPluginUnsupported) {
		return websocket.CloseTryAgainLater, "native Codex transport unavailable"
	}
	return websocket.CloseInternalServerErr, "native Codex transport failed"
}

func writeResponsesWebSocketUpgradeRequired(c *gin.Context) {
	writeCodexWebSocketUpgradeRequired(c, "Responses")
}

func writeCodexWebSocketUpgradeRequired(c *gin.Context, displayName string) {
	if c == nil {
		return
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "Codex"
	}
	c.Header("Connection", "Upgrade")
	c.Header("Upgrade", "websocket")
	c.Header("Sec-WebSocket-Version", "13")
	c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{"error": gin.H{
		"type": "unsupported_transport", "code": "websocket_not_available",
		"message": displayName + " WebSocket transport is not available",
	}})
}

func cpaRequestForWebSocket(acc *accountreg.Snapshot) (r cpa.ForwardRequest) {
	if acc != nil {
		r.Account.Platform, r.Account.Type, r.Account.Credentials = acc.Platform, acc.Type, acc.Credentials
	}
	return r
}

func (p *Pipeline) pickNativeCodexAccount(groupID int, model string) *accountreg.Snapshot {
	if p == nil || p.accounts == nil {
		return nil
	}
	target, ok := p.pickNativeCodexRoute(groupID, model, nil)
	if ok {
		return target.account
	}
	return nil
}

// pickAnyNativeCodexAccount selects a schedulable native account for a
// Realtime sideband whose call_id already fixes the upstream session and whose
// handshake consequently carries no model. The returned model is only the
// account-registry routing key used for capacity and audit attribution.
func (p *Pipeline) pickAnyNativeCodexAccount(groupID int) (*accountreg.Snapshot, string) {
	if p == nil || p.accounts == nil {
		return nil, ""
	}
	target, ok := p.pickNativeCodexRoute(groupID, "", nil)
	if !ok || target.account == nil {
		return nil, ""
	}
	return target.account, websocketAccountRoutingModel(target.account, "")
}

// hasNativeCodexAccount answers the only question that is knowable before a
// WebSocket upgrade: whether this group has at least one schedulable native
// Codex account.  Model-specific selection still happens after the first
// response.create frame, because the official CLI sends the model in that
// frame rather than as a handshake header/query parameter.
func (p *Pipeline) hasNativeCodexAccount(groupID int) bool {
	if p == nil || p.accounts == nil {
		return false
	}
	for _, acc := range p.accounts.ListGroupCandidates(groupID, nil) {
		if isNativeCodexAccount(acc) {
			return true
		}
	}
	return false
}

const defaultCodexRealtimeSidebandBaseURL = "https://api.openai.com/v1"

// codexWebSocketBaseURL follows the official split between Responses and an
// already-created WebRTC sideband. Sideband joins through the public OpenAI
// API by default even when OAuth call creation used the ChatGPT backend. An
// explicit realtime_sideband_base_url account override wins; a generic
// account base_url remains useful for private gateways but must not make an
// OAuth ChatGPT backend URL leak into the public sideband handshake.
func codexWebSocketBaseURL(acc *accountreg.Snapshot, endpoint string) string {
	request := cpaRequestForWebSocket(acc)
	if strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointCodexRemoteControlServerWebSocket) {
		// Remote Control is part of the ChatGPT backend management family.  Use
		// the same endpoint-specific base rewrite as native HTTP so a
		// `/backend-api/codex` account becomes a host/backend-api base and the
		// caller-supplied `/wham/remote/control/server` path is preserved.
		return codexProviderBaseURLForAccount(request.Account, endpoint)
	}
	if strings.EqualFold(strings.TrimSpace(endpoint), adaptor.EndpointRealtimeSideband) {
		if base := strings.TrimRight(strings.TrimSpace(request.Account.Credentials["realtime_sideband_base_url"]), "/"); base != "" {
			return base
		}
		if base := strings.TrimRight(strings.TrimSpace(request.Account.Credentials["base_url"]), "/"); base != "" && !isChatGPTCodexBackendBaseURL(base) {
			return base
		}
		return defaultCodexRealtimeSidebandBaseURL
	}
	return providerBaseURL(request)
}

func isChatGPTCodexBackendBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host != "chatgpt.com" && host != "chat.openai.com" {
		return false
	}
	return isCodexBackendURLPath(u)
}

// isCodexBackendBaseURLPath recognizes the ChatGPT backend URL shape without
// making any claim about the host.  Call-create is often sent through a
// private reverse proxy (for example https://gateway.example/backend-api/codex)
// and the official CLI still expects the backend's /realtime/calls path there.
// Sideband deliberately does not use this helper: an OAuth backend URL must
// not accidentally be used for the public api.openai.com sideband join.
func isCodexBackendBaseURLPath(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return isCodexBackendURLPath(u)
}

func isCodexBackendURLPath(u *url.URL) bool {
	if u == nil || u.Host == "" || u.User != nil {
		return false
	}
	path := strings.TrimRight(strings.ToLower(u.EscapedPath()), "/")
	// A private reverse proxy may prepend one or more routing segments before
	// the official ChatGPT backend path (for example
	// `/tenant-a/backend-api/codex`).  Classify by the path segment rather than
	// requiring the marker to start at `/`; otherwise OAuth realtime call
	// creation and sideband-origin selection take the wrong transport shape.
	const marker = "/backend-api/codex"
	// Compare complete path segments. A naive suffix/substring check would
	// misclassify look-alikes such as `/not-backend-api/codex`, changing the
	// upstream realtime path shape for an unrelated custom gateway.
	if path == marker || strings.HasPrefix(path, marker+"/") {
		return true
	}
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	markerParts := strings.Split(strings.TrimPrefix(marker, "/"), "/")
	if len(pathParts) < len(markerParts) {
		return false
	}
	for start := 0; start+len(markerParts) <= len(pathParts); start++ {
		match := true
		for index, expected := range markerParts {
			if !strings.EqualFold(pathParts[start+index], expected) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func (p *Pipeline) hasNativeCodexAccountForModel(groupID int, model string) bool {
	if p == nil || p.accounts == nil || strings.TrimSpace(model) == "" {
		return false
	}
	for _, acc := range p.accounts.ListGroupCandidatesForModel(groupID, model, nil) {
		if isNativeCodexAccount(acc) {
			return true
		}
	}
	return false
}

func websocketRequestModelHint(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if model := strings.TrimSpace(c.Query("model")); model != "" {
		return model
	}
	for _, name := range []string{"X-Codex-Model", "OpenAI-Model"} {
		if model := strings.TrimSpace(c.GetHeader(name)); model != "" {
			return model
		}
	}
	hint := strings.TrimSpace(c.GetHeader("X-Codex-Routing-Hint"))
	if hint == "" {
		return ""
	}
	for _, part := range strings.FieldsFunc(hint, func(r rune) bool { return r == ';' || r == ',' }) {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "model") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func websocketResponseCreateModel(data []byte) string {
	var envelope struct {
		Type     string `json:"type"`
		Model    string `json:"model"`
		Response struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &envelope) != nil || !strings.EqualFold(strings.TrimSpace(envelope.Type), "response.create") {
		return ""
	}
	if envelope.Model != "" {
		return strings.TrimSpace(envelope.Model)
	}
	return strings.TrimSpace(envelope.Response.Model)
}

// websocketResponseCreateEvent reports the model-bearing request metadata for
// one Responses WebSocket turn. The official Codex client keeps a single
// socket open across sequential response.create frames and may change the
// model between turns. Keep the parsed request around so billing can use the
// service_tier/reasoning fields belonging to that specific turn.
type websocketResponseCreateEvent struct {
	// streamID identifies the ordered Responses lane.  The empty string is the
	// implicit default lane (the official protocol omits stream_id for it).
	streamID string
	model    string
	req      *dto.ChatRequest
	start    time.Time
	warmup   bool
	// price is the pricing snapshot captured when response.create was
	// accepted.  It must never be reloaded when the terminal event arrives;
	// pricing may have been hot-reloaded while the turn was in flight.
	price  pricing.Price
	priced bool
	// Client/account capacity is owned by this turn and released exactly once
	// when a terminal event is observed or the socket is torn down.
	clientRelease          func()
	accountID              int
	accountRequest         string
	accountRPMMinute       int64
	accountProviderStarted bool
}

type websocketResponseTurnQueue struct {
	mu           sync.Mutex
	lanes        map[string][]websocketResponseCreateEvent
	knownStreams map[string]struct{}
	closed       bool
}

func newWebsocketResponseTurnQueue() *websocketResponseTurnQueue {
	return &websocketResponseTurnQueue{
		lanes:        make(map[string][]websocketResponseCreateEvent),
		knownStreams: make(map[string]struct{}),
	}
}

func (q *websocketResponseTurnQueue) push(
	data []byte,
	started time.Time,
	configure ...func(*websocketResponseCreateEvent),
) (string, error) {
	if q == nil {
		return "", errWebsocketResponseTurnQueueClosed
	}
	model := websocketResponseCreateModel(data)
	if model == "" {
		return "", errors.New("response.create model is required")
	}
	req, err := dto.ParseChatRequest(data)
	if err != nil {
		// The WebSocket envelope is not a normal Chat Completions request, but
		// its top-level service_tier/reasoning fields still have the same JSON
		// shape. Keep a minimal request for billing if an extension field makes
		// the lightweight parser reject the frame.
		req = &dto.ChatRequest{Model: model, Stream: true}
	} else {
		req.Model = model
		req.Stream = true
	}
	streamID, err := websocketResponseCreateStreamID(data)
	if err != nil {
		return "", err
	}
	turn := websocketResponseCreateEvent{
		streamID: streamID, model: model, req: req, start: started,
		warmup: websocketResponseCreateWarmup(data),
	}
	if len(configure) > 0 && configure[0] != nil {
		configure[0](&turn)
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return "", errWebsocketResponseTurnQueueClosed
	}
	if q.lanes == nil {
		q.lanes = make(map[string][]websocketResponseCreateEvent)
	}
	if q.knownStreams == nil {
		q.knownStreams = make(map[string]struct{})
	}
	if streamID != "" {
		if _, known := q.knownStreams[streamID]; !known {
			if len(q.knownStreams) >= codexWebSocketMaxNamedStreams {
				q.mu.Unlock()
				return "", errWebsocketNamedStreamLimit
			}
			q.knownStreams[streamID] = struct{}{}
		}
	}
	q.lanes[streamID] = append(q.lanes[streamID], turn)
	q.mu.Unlock()
	return model, nil
}

// close prevents late response.create frames from being accepted while the
// WebSocket session is tearing down. It deliberately leaves queued turns
// intact so the owner can drain and release them with popAll.
func (q *websocketResponseTurnQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}

func websocketResponseCreateWarmup(data []byte) bool {
	var envelope struct {
		Generate *bool `json:"generate"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Generate == nil {
		return false
	}
	return !*envelope.Generate
}

// pop removes the oldest turn from one lane.  The variadic form preserves the
// old unit-test/helper call q.pop() for the implicit default lane while making
// it impossible for a named lane completion to consume another lane's turn.
func (q *websocketResponseTurnQueue) pop(streamIDs ...string) (websocketResponseCreateEvent, bool) {
	if q == nil {
		return websocketResponseCreateEvent{}, false
	}
	streamID := ""
	if len(streamIDs) > 0 {
		streamID = strings.TrimSpace(streamIDs[0])
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	turns := q.lanes[streamID]
	if len(turns) == 0 {
		return websocketResponseCreateEvent{}, false
	}
	turn := turns[0]
	copy(turns, turns[1:])
	turns[len(turns)-1] = websocketResponseCreateEvent{}
	turns = turns[:len(turns)-1]
	if len(turns) == 0 {
		delete(q.lanes, streamID)
	} else {
		q.lanes[streamID] = turns
	}
	return turn, true
}

// discardLast removes a turn that was enqueued locally but could not be sent
// to the executor.  It is lane-scoped for the same reason as pop.
func (q *websocketResponseTurnQueue) discardLast(streamIDs ...string) {
	if q == nil {
		return
	}
	streamID := ""
	if len(streamIDs) > 0 {
		streamID = strings.TrimSpace(streamIDs[0])
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	turns := q.lanes[streamID]
	if len(turns) == 0 {
		return
	}
	turns[len(turns)-1] = websocketResponseCreateEvent{}
	turns = turns[:len(turns)-1]
	if len(turns) == 0 {
		delete(q.lanes, streamID)
	} else {
		q.lanes[streamID] = turns
	}
}

// markProviderStarted records that a queued response.create has been handed
// to the executor input channel.  It is deliberately matched by the account
// request id as well as stream_id: multiple creates may share one lane, and a
// provider terminal event can pop an earlier turn before the reader finishes
// bookkeeping for the next one.
func (q *websocketResponseTurnQueue) markProviderStarted(streamID, accountRequest string) bool {
	if q == nil {
		return false
	}
	streamID = strings.TrimSpace(streamID)
	q.mu.Lock()
	defer q.mu.Unlock()
	turns := q.lanes[streamID]
	for i := len(turns) - 1; i >= 0; i-- {
		if accountRequest != "" && turns[i].accountRequest != accountRequest {
			continue
		}
		turns[i].accountProviderStarted = true
		return true
	}
	return false
}

// popAll drains every lane and is used for connection-scoped errors/teardown,
// where the upstream cannot associate the failure with one stream_id.  Map
// iteration order is intentionally unspecified; callers must only use this
// for releasing resources, never for billing correlation.
func (q *websocketResponseTurnQueue) popAll() []websocketResponseCreateEvent {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []websocketResponseCreateEvent
	for lane, turns := range q.lanes {
		out = append(out, turns...)
		delete(q.lanes, lane)
	}
	return out
}

func (q *websocketResponseTurnQueue) pending() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, turns := range q.lanes {
		n += len(turns)
	}
	return n
}

// releaseWebSocketResponseTurn releases resources owned by one Responses
// turn.  RPM is deliberately not rolled back by default: once the request has
// been accepted into the executor it may already have been consumed upstream.
// Callers may request rollback only for a local rejection that happened before
// the frame was handed to the executor.
func (p *Pipeline) releaseWebSocketResponseTurn(turn websocketResponseCreateEvent, rollbackRPM bool) {
	if p == nil {
		return
	}
	if turn.clientRelease != nil {
		turn.clientRelease()
	}
	if turn.accountID > 0 && turn.accountRequest != "" && p.concurrency != nil {
		p.concurrency.ReleaseAccountSlot(context.Background(), turn.accountID, turn.accountRequest)
	}
	if rollbackRPM && turn.accountID > 0 && turn.accountRPMMinute > 0 && p.rpm != nil {
		p.rpm.DecrementAccountRPM(context.Background(), turn.accountID, turn.accountRPMMinute)
	}
}

// websocketResponseStreamID returns the lane identifier.  The empty string is
// the implicit default lane; malformed JSON also returns the default so the
// caller can still forward opaque frames without inventing a cross-lane key.
func websocketResponseStreamID(data []byte) string {
	streamID, _ := websocketResponseEventStreamID(data)
	return streamID
}

func websocketResponseCreateStreamID(data []byte) (string, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return "", errors.New("invalid response.create frame")
	}
	raw, present := envelope["stream_id"]
	if !present {
		return "", nil
	}
	var streamID string
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &streamID) != nil {
		return "", errors.New("response.create stream_id must be a string")
	}
	if err := validateWebsocketStreamID(streamID); err != nil {
		return "", err
	}
	return streamID, nil
}

func websocketResponseEventStreamID(data []byte) (string, bool) {
	streamID, present, _ := websocketResponseEventStreamIDChecked(data)
	return streamID, present
}

// websocketResponseEventStreamIDChecked distinguishes an omitted default lane
// from a malformed explicit stream_id. A malformed value must never silently
// map to the implicit default lane, otherwise an out-of-order terminal event
// can bill/release the wrong response.create turn.
func websocketResponseEventStreamIDChecked(data []byte) (streamID string, present, malformed bool) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return "", false, false
	}
	raw, present := envelope["stream_id"]
	if !present {
		return "", false, false
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", true, true
	}
	if json.Unmarshal(raw, &streamID) != nil {
		return "", true, true
	}
	if err := validateWebsocketStreamID(streamID); err != nil {
		return "", true, true
	}
	return streamID, true, false
}

func validateWebsocketStreamID(streamID string) error {
	if len(streamID) == 0 || len(streamID) > 256 {
		return errors.New("response.create stream_id must contain 1 to 256 characters")
	}
	for i := 0; i < len(streamID); i++ {
		ch := streamID[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.' {
			continue
		}
		return errors.New("response.create stream_id contains an invalid character")
	}
	return nil
}

func websocketNamedStreamLimitError(streamID string) []byte {
	payload, err := json.Marshal(map[string]any{
		"type":      "error",
		"status":    http.StatusBadRequest,
		"stream_id": streamID,
		"error": map[string]any{
			"type":    "invalid_request_error",
			"code":    "websocket_stream_limit_reached",
			"message": "This WebSocket connection has reached its maximum number of distinct stream IDs (32). Reuse an existing stream_id or open a new WebSocket connection.",
			"param":   "stream_id",
		},
	})
	if err != nil {
		return []byte(`{"type":"error","status":400,"stream_id":"` + strings.ReplaceAll(streamID, `"`, "") + `"}`)
	}
	return payload
}

type websocketResponseTerminalEvent struct {
	Type              string
	StreamID          string
	streamIDPresent   bool
	streamIDMalformed bool
	connectionError   bool
	Model             string
	Usage             dto.Usage
	usageFound        bool
}

func websocketResponseTerminal(data []byte) (websocketResponseTerminalEvent, bool) {
	typ := websocketResponseEventType(data)
	switch typ {
	case "response.completed", "response.incomplete", "response.failed", "error":
	default:
		return websocketResponseTerminalEvent{}, false
	}
	var envelope struct {
		Model string `json:"model"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
		Response *struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return websocketResponseTerminalEvent{}, false
	}
	model := strings.TrimSpace(envelope.Model)
	if model == "" && envelope.Response != nil {
		model = strings.TrimSpace(envelope.Response.Model)
	}
	streamID, streamIDPresent, streamIDMalformed := websocketResponseEventStreamIDChecked(data)
	usage, usageFound := dto.ExtractResponsesUsage(data)
	return websocketResponseTerminalEvent{
		Type: typ, StreamID: streamID, streamIDPresent: streamIDPresent,
		streamIDMalformed: streamIDMalformed,
		connectionError:   typ == "error" && (!streamIDPresent || streamID == ""),
		Model:             model, Usage: usage, usageFound: usageFound,
	}, true
}

func (p *Pipeline) responsesWebSocketPrice(model string) (pricing.Price, bool) {
	if p == nil || p.pricing == nil {
		return pricing.Price{}, false
	}
	return p.pricing.Get(strings.TrimSpace(model))
}

// preflightResponsesWebSocket mirrors the non-streaming relay gates that can
// be evaluated before a WebSocket 101: client allow-list/fallback, user
// balance, billing-rate ceiling, and the user/API-key concurrency slot.  The
// returned slot is owned by the first response.create turn once its model is
// accepted; callers must release it when that turn terminates or is rejected.
func (p *Pipeline) preflightResponsesWebSocket(c *gin.Context, keyInfo *auth.APIKeyInfo, clientType string) (func(), bool) {
	if c == nil || keyInfo == nil {
		return nil, false
	}
	if len(keyInfo.GroupAllowedClients) > 0 && !clientid.Matches(clientType, keyInfo.GroupAllowedClients) {
		if keyInfo.GroupFallbackID != nil {
			keyInfo.GroupID = *keyInfo.GroupFallbackID
		} else {
			writeError(c, http.StatusForbidden, "permission_error", "client_restricted", "当前客户端类型不允许访问此分组")
			return nil, false
		}
	}
	if keyInfo.UserBalance <= 0 {
		writeError(c, http.StatusPaymentRequired, "insufficient_quota", "insufficient_balance", "账户余额不足")
		return nil, false
	}
	if rate := billing.ResolveBillingRate(keyInfo); billing.ExceedsKeyMaxRate(keyInfo, rate) {
		msg := fmt.Sprintf("当前计费倍率 %.2f 超过密钥最高倍率 %.2f", rate, keyInfo.MaxRate)
		writeError(c, http.StatusForbidden, "permission_error", "billing_rate_exceeded", msg)
		return nil, false
	}
	release, limitCode := p.acquireResponsesWebSocketClientSlot(c, keyInfo, true)
	if limitCode != "" {
		return nil, false
	}
	return release, true
}

// acquireResponsesWebSocketClientSlot is intentionally synchronous on
// release.  A client may send the next response.create immediately after a
// terminal event; deferring the Redis ZREM in a goroutine would transiently
// make a just-freed slot look occupied and incorrectly reject that turn.
func (p *Pipeline) acquireResponsesWebSocketClientSlot(c *gin.Context, keyInfo *auth.APIKeyInfo, writeHTTPError bool) (func(), string) {
	if p == nil || p.concurrency == nil || keyInfo == nil {
		return func() {}, ""
	}
	slotID := uuid.New().String()
	err := p.concurrency.AcquireClientCapacity(
		c.Request.Context(), keyInfo.UserID, keyInfo.KeyID, keyInfo.GroupID, slotID,
		keyInfo.UserMaxConcurrency, keyInfo.KeyMaxConcurrency, channelSlotTTL(true),
	)
	if errors.Is(err, scheduler.ErrUserConcurrencyLimit) {
		if writeHTTPError {
			writeRateLimitError(c, "user_concurrency_limit", "用户并发数已达上限", time.Second)
		}
		return nil, "user_concurrency_limit"
	}
	if errors.Is(err, scheduler.ErrAPIKeyConcurrencyLimit) {
		if writeHTTPError {
			writeRateLimitError(c, "apikey_concurrency_limit", "API Key 并发数已达上限", time.Second)
		}
		return nil, "apikey_concurrency_limit"
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			p.concurrency.ReleaseClientCapacity(
				context.Background(), keyInfo.UserID, keyInfo.KeyID, keyInfo.GroupID, slotID,
				keyInfo.UserMaxConcurrency > 0, keyInfo.KeyMaxConcurrency > 0,
			)
		})
	}, ""
}

// acquireResponsesWebSocketAccountTurn keeps generate=false prewarm turns in
// the account concurrency budget without charging the account RPM budget:
// prewarm establishes connection-local state but is not an inference request.
func (p *Pipeline) acquireResponsesWebSocketAccountTurn(
	ctx context.Context,
	acc *accountreg.Snapshot,
	warmup bool,
) (requestID string, rpmMinute int64, ok bool) {
	if p == nil || p.concurrency == nil || acc == nil {
		return "", 0, false
	}
	if !warmup {
		requestID, rpmMinute, _, ok := p.acquireAccountSlots(ctx, acc, true)
		return requestID, rpmMinute, ok
	}
	requestID = uuid.New().String()
	if err := p.concurrency.AcquireAccountSlot(ctx, acc.ID, requestID, acc.MaxConcurrency, channelSlotTTL(true)); err != nil {
		return "", 0, false
	}
	return requestID, 0, true
}

func websocketResponseCompleted(data []byte) (dto.Usage, bool, string) {
	var envelope struct {
		Type     string `json:"type"`
		Model    string `json:"model"`
		Response *struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &envelope) != nil ||
		!strings.EqualFold(strings.TrimSpace(envelope.Type), "response.completed") {
		return dto.Usage{}, false, ""
	}
	model := strings.TrimSpace(envelope.Model)
	if model == "" && envelope.Response != nil {
		model = strings.TrimSpace(envelope.Response.Model)
	}
	usage, found := dto.ExtractResponsesUsage(data)
	return usage, found, model
}

func websocketResponseEventType(data []byte) string {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(envelope.Type))
}

func nativeCodexAccountSupportsWebSocketModel(acc *accountreg.Snapshot, model string) bool {
	if acc == nil {
		return false
	}
	model = strings.TrimSpace(model)
	if model == "" || len(acc.Models) == 0 {
		// Empty model catalogs are the registry's explicit native wildcard.
		return true
	}
	_, ok := acc.Models[model]
	return ok
}

func isCodexGuardianWebSocketEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case adaptor.EndpointGuardian, adaptor.EndpointGuardianClassifier:
		return true
	default:
		return false
	}
}

// normalizeCodexGuardianResponseCreate removes fields that the official
// Guardian clients intentionally leave unset.  In particular, Guardian and
// GuardianClassifier requests never carry service_tier, even when the parent
// turn selected one.  Keep non-response.create frames and malformed payloads
// byte-for-byte intact; the WebSocket data plane is otherwise transparent.
func normalizeCodexGuardianResponseCreate(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil || envelope == nil {
		return data
	}
	rawType, ok := envelope["type"]
	if !ok {
		return data
	}
	var eventType string
	if json.Unmarshal(rawType, &eventType) != nil || !strings.EqualFold(strings.TrimSpace(eventType), "response.create") {
		return data
	}
	if _, exists := envelope["service_tier"]; !exists {
		return data
	}
	delete(envelope, "service_tier")
	normalized, err := json.Marshal(envelope)
	if err != nil {
		return data
	}
	return normalized
}

// realtimeSidebandProviderPath collapses the public compatibility aliases to
// the two official upstream shapes. Query parameters are carried separately
// in CodexExecuteRequest.Query and therefore must not be folded into Path.
func realtimeSidebandProviderPath(c *gin.Context) string {
	path, _ := realtimeSidebandProviderPathChecked(c)
	if path == "" {
		return "/realtime"
	}
	return path
}

func realtimeSidebandProviderPathChecked(c *gin.Context) (string, error) {
	if c == nil {
		return "/realtime", nil
	}
	callID, err := realtimeSidebandCallIDChecked(c)
	if err != nil {
		return "", err
	}
	if callID == "" || c.Param("call_id") == "" {
		return "/realtime", nil
	}
	return "/live/" + url.PathEscape(callID), nil
}

// validateRealtimeCallID validates the decoded value of an opaque call ID.
// The official Frameless client deliberately permits IDs containing path
// separators and serializes the whole value as one escaped URL segment. Keep
// those values compatible while rejecting the only spellings that could
// change URL structure when used without escaping. Callers constructing a
// path must always pass the validated value through url.PathEscape.
func validateRealtimeCallID(raw string) error {
	if raw == "" {
		return errors.New("realtime call id is required")
	}
	if len(raw) > 512 || !utf8.ValidString(raw) || strings.TrimSpace(raw) != raw {
		return errors.New("invalid realtime call id")
	}
	if raw == "." || raw == ".." {
		return errors.New("invalid realtime call id")
	}
	// A backslash is not a URL path separator according to RFC 3986, but some
	// reverse proxies normalize it as one. Do not allow that ambiguity even
	// though the value is escaped again before the provider request.
	if strings.ContainsRune(raw, '\\') {
		return errors.New("invalid realtime call id")
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("invalid realtime call id")
		}
	}
	return nil
}

// realtimeSidebandCallIDChecked extracts the single call identifier accepted
// by the public sideband aliases. Frameless routes use a path parameter,
// whereas V1/V2 routes use `?call_id=`. Reject duplicate or mixed forms so
// affinity lookup and the provider URL can never disagree about the call.
func realtimeSidebandCallIDChecked(c *gin.Context) (string, error) {
	if c == nil {
		return "", errors.New("realtime call id is required")
	}
	pathID := c.Param("call_id")
	if pathID != "" {
		if c.Request != nil && c.Request.URL != nil {
			if _, queryPresent := c.Request.URL.Query()["call_id"]; queryPresent {
				return "", errors.New("realtime call id must be provided in only one location")
			}
		}
		return realtimeSidebandPathCallID(c, pathID)
	}
	if c.Request == nil || c.Request.URL == nil {
		return "", errors.New("realtime call id is required")
	}
	queryValues, queryPresent := c.Request.URL.Query()["call_id"]
	if !queryPresent {
		return "", errors.New("realtime call id is required")
	}
	if len(queryValues) != 1 {
		return "", errors.New("realtime call id must be provided in only one location")
	}
	if err := validateRealtimeCallID(queryValues[0]); err != nil {
		return "", err
	}
	return queryValues[0], nil
}

// realtimeSidebandPathCallID proves that a decoded Gin parameter originated
// from exactly one escaped path segment. This is the important distinction
// between the official `/live/..%2F..%2Fopaque` representation (safe and
// round-trippable) and a literal multi-segment path. Gin routes using RawPath
// in production, then exposes the once-decoded value through c.Param.
func realtimeSidebandPathCallID(c *gin.Context, pathID string) (string, error) {
	if err := validateRealtimeCallID(pathID); err != nil {
		return "", err
	}
	if c == nil || c.Request == nil || c.Request.URL == nil {
		// Unit-test/embedding contexts can provide only Gin params. The provider
		// path still goes through url.PathEscape, so retaining this fallback does
		// not allow a decoded separator to alter the upstream path.
		return pathID, nil
	}
	u := c.Request.URL
	if u.RawPath != "" {
		decodedPath, err := url.PathUnescape(u.RawPath)
		if err != nil || decodedPath != u.Path {
			return "", errors.New("invalid realtime call id path encoding")
		}
	}
	escapedPath := u.EscapedPath()
	separator := strings.LastIndexByte(escapedPath, '/')
	if separator < 0 || separator == len(escapedPath)-1 {
		return "", errors.New("invalid realtime call id path encoding")
	}
	rawSegment := escapedPath[separator+1:]
	decodedSegment, err := url.PathUnescape(rawSegment)
	if err != nil || decodedSegment != pathID {
		return "", errors.New("invalid realtime call id path encoding")
	}
	return decodedSegment, nil
}

func realtimeSidebandCallID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if callID := c.Param("call_id"); callID != "" {
		return callID
	}
	return c.Query("call_id")
}

func websocketAccountRoutingModel(acc *accountreg.Snapshot, hinted string) string {
	if acc == nil {
		return ""
	}
	hinted = strings.TrimSpace(hinted)
	if hinted != "" {
		if _, ok := acc.Models[hinted]; ok {
			return hinted
		}
	}
	models := make([]string, 0, len(acc.Models))
	for model := range acc.Models {
		if model = strings.TrimSpace(model); model != "" {
			models = append(models, model)
		}
	}
	slices.Sort(models)
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

// codexRemoteControlWebSocketNameFallback normalizes the official inbound
// handshake representation when the dedicated Remote Control middleware did
// not populate context. Codex CLI sends X-Codex-Name as standard Base64;
// Core's internal metadata and the plugin envelope intentionally use the raw
// display name, so decode exactly once here. A deployment-specific legacy
// alias is kept as a raw value, and malformed values fall back unchanged so an
// embedding caller can still diagnose the bad header.
func codexRemoteControlWebSocketNameFallback(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if raw := strings.TrimSpace(c.GetHeader("X-Codex-Name")); raw != "" {
		if decoded, ok := decodeCodexRemoteControlNameHeader(raw); ok {
			return decoded
		}
		return raw
	}
	return firstNonEmptyHeader(c, "X-Remote-Control-Name")
}

func decodeCodexRemoteControlNameHeader(raw string) (string, bool) {
	// The official header uses the standard alphabet. RawStdEncoding is also
	// accepted because some HTTP clients omit padding when constructing the
	// handshake. Do not try URL-safe encodings here: an embedding may use a
	// plain X-Codex-Name value containing URL-safe punctuation, and decoding it
	// would silently alter the server name.
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded, err := encoding.DecodeString(raw)
		if err == nil && len(decoded) > 0 && utf8.Valid(decoded) {
			return string(decoded), true
		}
	}
	return "", false
}

// accountSidebandWebSocketHeaders retains end-to-end Codex/Realtime metadata
// that is not known to Core yet, while enforcing the account credential
// boundary. The normal HTTP allow-list intentionally stays narrow; a
// sideband handshake has several rolling protocol headers, so dropping an
// otherwise harmless X-* field can make an established call unusable.
func accountSidebandWebSocketHeaders(c *gin.Context) http.Header {
	out := make(http.Header)
	if c == nil || c.Request == nil {
		return out
	}
	for name, values := range c.Request.Header {
		lower := strings.ToLower(strings.TrimSpace(name))
		if sidebandBlockedHeader(lower) {
			continue
		}
		out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	if out.Get("Content-Type") == "" {
		out.Set("Content-Type", "application/json")
	}
	return out
}

// remoteControlWebSocketHeaders projects only non-credential protocol
// metadata.  The enrolled bearer and installation/server identity are carried
// in the explicit providertransport.Request fields and are applied by the
// native executor.  In particular, Authorization from the downstream request
// must never be forwarded as an OAuth bearer, and Responses beta/Guardian
// headers do not belong to the Remote Control protocol.
func remoteControlWebSocketHeaders(c *gin.Context) http.Header {
	out := make(http.Header)
	if c == nil || c.Request == nil {
		return out
	}
	for name, values := range c.Request.Header {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "authorization" || lower == "cookie" || lower == "proxy-authorization" ||
			lower == "openai-beta" || lower == "x-openai-subagent" ||
			lower == "x-openai-internal-codex-responses-lite" ||
			lower == "x-codex-remote-control-token" || lower == "x-remote-control-token" ||
			lower == "x-codex-server-id" || lower == "x-codex-name" ||
			lower == "x-codex-protocol-version" || lower == "x-codex-installation-id" ||
			lower == "x-codex-host-device-kind" || lower == "x-codex-subscribe-cursor" ||
			sidebandBlockedHeader(lower) {
			continue
		}
		// Keep only the protocol metadata namespaces used by the official
		// app-server.  This avoids forwarding arbitrary caller headers to a
		// long-lived privileged control connection.
		if !strings.HasPrefix(lower, "x-codex-") && !strings.HasPrefix(lower, "x-openai-") && lower != "user-agent" && lower != "origin" {
			continue
		}
		out[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	return out
}

func sidebandBlockedHeader(lower string) bool {
	if lower == "" {
		return true
	}
	for _, name := range []string{
		"authorization", "proxy-authorization", "cookie", "set-cookie", "host", "content-length",
		"upgrade", "connection", "keep-alive", "proxy-authenticate", "transfer-encoding", "te", "trailer",
		"sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions", "sec-websocket-accept", "sec-websocket-protocol",
	} {
		if lower == name {
			return true
		}
	}
	for _, marker := range []string{"authorization", "api-key", "apikey", "access-token", "refresh-token", "id-token", "session-token", "credential", "secret", "password"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

type codexWebSocketWriter interface {
	SetWriteDeadline(time.Time) error
	WriteMessage(int, []byte) error
	WriteControl(int, []byte, time.Time) error
}

func writeCodexWebSocketFrame(ws codexWebSocketWriter, frame protocol.CodexWebSocketFrame) error {
	switch frame.Type {
	case protocol.CodexWebSocketFrameClose:
		code := frame.Code
		if code == 0 {
			code = websocket.CloseNormalClosure
		}
		return ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, frame.Reason), time.Now().Add(time.Second))
	case protocol.CodexWebSocketFramePing, protocol.CodexWebSocketFramePong:
		kind := websocket.PingMessage
		if frame.Type == protocol.CodexWebSocketFramePong {
			kind = websocket.PongMessage
		}
		return ws.WriteControl(kind, frame.Data, time.Now().Add(time.Second))
	case protocol.CodexWebSocketFrameText:
		if err := ws.SetWriteDeadline(time.Now().Add(codexWebSocketWriteTimeout)); err != nil {
			return err
		}
		return ws.WriteMessage(websocket.TextMessage, frame.Data)
	case protocol.CodexWebSocketFrameBinary:
		if err := ws.SetWriteDeadline(time.Now().Add(codexWebSocketWriteTimeout)); err != nil {
			return err
		}
		return ws.WriteMessage(websocket.BinaryMessage, frame.Data)
	default:
		return nil
	}
}
