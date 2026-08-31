package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// A prepared session is used only while Core is waiting to commit the
// downstream HTTP 101 response. The provider output queue is deliberately
// bounded: a misbehaving executor must not be able to turn a client that has
// not completed its handshake into an unbounded memory consumer.
const (
	codexWebSocketPrepareQueueSize  = 16
	codexWebSocketPrepareDrainLimit = 2 * time.Second
)

var codexWebSocketPrepareTimeout = 20 * time.Second

var (
	errCodexWebSocketPrepareQueueFull = errors.New("codex websocket prepare queue is full")
	errCodexWebSocketPrepareNoHeaders = errors.New("upstream websocket handshake did not return headers")
)

type codexPreparedWebSocketResult struct {
	refreshed map[string]string
	err       error
}

// codexPreparedWebSocket owns an executor invocation that has started before
// the downstream upgrade. The executor emits its upstream 101 metadata into
// events; Core consumes the header, commits the downstream 101, and then
// drains the same event stream through the normal frame handler.
type codexPreparedWebSocket struct {
	cancel context.CancelFunc

	events      chan protocol.CodexWebSocketFrame
	resultReady chan struct{}

	mu      sync.Mutex
	pending []protocol.CodexWebSocketFrame
	header  protocol.CodexWebSocketFrame
	result  codexPreparedWebSocketResult
}

func startCodexPreparedWebSocket(
	parent context.Context,
	transport providertransport.CodexWebSocketTransport,
	request providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
) *codexPreparedWebSocket {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	session := &codexPreparedWebSocket{
		cancel:      cancel,
		events:      make(chan protocol.CodexWebSocketFrame, codexWebSocketPrepareQueueSize),
		resultReady: make(chan struct{}),
	}
	go func() {
		refreshed, err := transport.ExecuteWebSocket(ctx, request, frames, func(frame protocol.CodexWebSocketFrame) error {
			select {
			case session.events <- frame:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
				return errCodexWebSocketPrepareQueueFull
			}
		})
		session.mu.Lock()
		session.result = codexPreparedWebSocketResult{refreshed: refreshed, err: err}
		session.mu.Unlock()
		close(session.resultReady)
		close(session.events)
	}()
	return session
}

func codexWebSocketProviderRequest(
	c *gin.Context,
	route codexWebSocketRoute,
	acc *accountreg.Snapshot,
	keyInfo *auth.APIKeyInfo,
	clientType string,
	model string,
	started time.Time,
	upstreamAudit providertransport.UpstreamAuditSink,
) providertransport.Request {
	providerHeaders := accountProviderHeaders(c)
	if route.remoteControl {
		providerHeaders = remoteControlWebSocketHeaders(c)
	} else if route.endpoint == adaptor.EndpointRealtimeSideband {
		providerHeaders = accountSidebandWebSocketHeaders(c)
	} else if isCodexGuardianWebSocketEndpoint(route.endpoint) {
		// Guardian has a dedicated backend route. Caller routing hints and
		// subagent values must not leak into it.
		providerHeaders.Del("X-Codex-Routing-Hint")
		providerHeaders.Set("X-OpenAI-Subagent", "guardian")
		if providerHeaders.Get("OpenAI-Beta") == "" {
			providerHeaders.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
		}
		if route.endpoint == adaptor.EndpointGuardianClassifier {
			providerHeaders.Set("X-OpenAI-Internal-Codex-Responses-Lite", "true")
		}
	}

	remoteToken := ""
	remoteServerID := ""
	remoteName := ""
	remoteProtocolVersion := ""
	installationID := ""
	remoteHostDeviceKind := ""
	remoteSubscribeCursor := ""
	if route.remoteControl {
		remoteToken = contextString(c, middleware.CtxKeyCodexRemoteControlToken)
		remoteServerID = contextString(c, middleware.CtxKeyCodexRemoteControlServerID)
		remoteName = contextString(c, middleware.CtxKeyCodexRemoteControlName)
		remoteProtocolVersion = contextString(c, middleware.CtxKeyCodexRemoteControlProtocolVersion)
		installationID = contextString(c, middleware.CtxKeyCodexRemoteControlInstallationID)
		if remoteToken == "" {
			remoteToken = firstNonEmptyHeader(c, "X-Codex-Remote-Control-Token", "X-Remote-Control-Token")
		}
		if remoteServerID == "" {
			remoteServerID = firstNonEmptyHeader(c, "X-Codex-Server-Id", "X-Remote-Control-Server-Id")
		}
		if remoteName == "" {
			remoteName = codexRemoteControlWebSocketNameFallback(c)
		}
		if remoteProtocolVersion == "" {
			remoteProtocolVersion = firstNonEmptyHeader(c, "X-Codex-Protocol-Version", "X-Remote-Control-Protocol-Version")
		}
		if installationID == "" {
			installationID = firstNonEmptyHeader(c, "X-Codex-Installation-Id", "X-Remote-Control-Installation-Id")
		}
		// The route handler validates these headers before account selection and
		// handshake preparation.  Keep extraction here explicit as well so a
		// direct caller cannot accidentally smuggle them through Header.
		remoteHostDeviceKind, remoteSubscribeCursor, _ = codexRemoteControlWebSocketOptionalHeaders(c)
	}

	request := providertransport.Request{
		RequestID: requestIDOf(c), Client: clientType, Method: http.MethodGet,
		BaseURL: codexWebSocketBaseURL(acc, route.endpoint), Path: route.providerPath, Query: providerQuery(c),
		Transport: protocol.CodexTransportWebSocket, Model: model, Endpoint: route.endpoint, EntryProtocol: registry.ProtocolOpenAI,
		Stream: true, Headers: providerHeaders, RequestStartedAt: started, CursorSessionKey: requestIDOf(c), UpstreamAudit: upstreamAudit, LegacyContext: c,
		RemoteControlToken: remoteToken, RemoteControlServerID: remoteServerID,
		RemoteControlName: remoteName, RemoteControlProtocolVersion: remoteProtocolVersion,
		InstallationID: installationID, RemoteControlHostDeviceKind: remoteHostDeviceKind,
		RemoteControlSubscribeCursor: remoteSubscribeCursor,
	}
	if keyInfo != nil {
		request.GroupID = keyInfo.GroupID
	}
	if acc != nil {
		request.Account = providertransport.Account{ID: acc.ID, Name: acc.Name, Platform: acc.Platform, Type: acc.Type, Credentials: acc.Credentials, ProxyURL: acc.ProxyURL}
		request.UpstreamModel = acc.ResolveModel(model)
	}
	return request
}

// waitHandshake consumes executor metadata until the upstream 101 headers
// arrive. Non-header frames are retained and replayed after the downstream
// upgrade, preserving executor wire order.
func (s *codexPreparedWebSocket) waitHandshake(ctx context.Context) (protocol.CodexWebSocketFrame, error) {
	if s == nil {
		return protocol.CodexWebSocketFrame{}, errCodexWebSocketPrepareNoHeaders
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(codexWebSocketPrepareTimeout)
	defer timer.Stop()
	for {
		select {
		case frame, ok := <-s.events:
			if !ok {
				result := s.waitResult()
				if result.err != nil {
					return protocol.CodexWebSocketFrame{}, result.err
				}
				return protocol.CodexWebSocketFrame{}, errCodexWebSocketPrepareNoHeaders
			}
			switch frame.Type {
			case protocol.CodexWebSocketFrameHeaders:
				status := frame.StatusCode
				if status != 0 && status != http.StatusSwitchingProtocols {
					return protocol.CodexWebSocketFrame{}, fmt.Errorf("upstream websocket handshake returned status %d", status)
				}
				s.mu.Lock()
				s.header = frame
				s.mu.Unlock()
				return frame, nil
			case protocol.CodexWebSocketFrameError:
				return protocol.CodexWebSocketFrame{}, preparedWebSocketFrameError(frame)
			default:
				s.mu.Lock()
				if len(s.pending) >= codexWebSocketPrepareQueueSize {
					s.mu.Unlock()
					return protocol.CodexWebSocketFrame{}, errCodexWebSocketPrepareQueueFull
				}
				s.pending = append(s.pending, frame)
				s.mu.Unlock()
			}
		case <-ctx.Done():
			return protocol.CodexWebSocketFrame{}, ctx.Err()
		case <-timer.C:
			return protocol.CodexWebSocketFrame{}, context.DeadlineExceeded
		}
	}
}

// run drains the prepared executor after the downstream 101 has been
// committed. The consumed header and any pre-header frames are replayed first.
func (s *codexPreparedWebSocket) run(emit func(protocol.CodexWebSocketFrame) error) (map[string]string, error) {
	if s == nil {
		return nil, errCodexWebSocketPrepareNoHeaders
	}
	if emit == nil {
		return nil, errors.New("codex websocket prepared frame sink is nil")
	}
	s.mu.Lock()
	pending := append([]protocol.CodexWebSocketFrame(nil), s.pending...)
	header := s.header
	s.pending = nil
	s.mu.Unlock()
	if header.Type == protocol.CodexWebSocketFrameHeaders {
		// Any frames retained by waitHandshake preceded the header on the
		// executor stream. Keep that original order when replaying them after
		// the downstream upgrade.
		pending = append(pending, header)
	}
	for _, frame := range pending {
		if err := emit(frame); err != nil {
			s.cancel()
			return s.awaitResult(err)
		}
	}
	for frame := range s.events {
		if err := emit(frame); err != nil {
			s.cancel()
			return s.awaitResult(err)
		}
	}
	result := s.waitResult()
	return result.refreshed, result.err
}

func (s *codexPreparedWebSocket) awaitResult(preferred error) (map[string]string, error) {
	if s == nil {
		return nil, preferred
	}
	if preferred == nil {
		preferred = context.Canceled
	}
	timer := time.NewTimer(codexWebSocketPrepareDrainLimit)
	defer timer.Stop()
	select {
	case <-s.resultReady:
		result := s.resultSnapshot()
		if result.err != nil && !errors.Is(result.err, context.Canceled) {
			return result.refreshed, result.err
		}
		return result.refreshed, preferred
	case <-timer.C:
		return nil, preferred
	}
}

func (s *codexPreparedWebSocket) cancelAndDrain() (map[string]string, error) {
	if s == nil {
		return nil, nil
	}
	s.cancel()
	timer := time.NewTimer(codexWebSocketPrepareDrainLimit)
	defer timer.Stop()
	select {
	case <-s.resultReady:
		result := s.resultSnapshot()
		return result.refreshed, result.err
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
}

func (s *codexPreparedWebSocket) waitResult() codexPreparedWebSocketResult {
	if s == nil {
		return codexPreparedWebSocketResult{err: errCodexWebSocketPrepareNoHeaders}
	}
	<-s.resultReady
	return s.resultSnapshot()
}

func (s *codexPreparedWebSocket) resultSnapshot() codexPreparedWebSocketResult {
	if s == nil {
		return codexPreparedWebSocketResult{err: errCodexWebSocketPrepareNoHeaders}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return codexPreparedWebSocketResult{refreshed: cloneStringMap(s.result.refreshed), err: s.result.err}
}

func preparedWebSocketFrameError(frame protocol.CodexWebSocketFrame) error {
	if frame.Error != nil {
		return &providertransport.CodexPluginError{Info: *frame.Error}
	}
	if strings.TrimSpace(frame.Reason) != "" {
		return errors.New(strings.TrimSpace(frame.Reason))
	}
	return errors.New("upstream websocket executor failed before handshake")
}

// writeCodexWebSocketPrepareFailure is used while the downstream HTTP
// response is still mutable. Capability failures deliberately use 426 so the
// official CLI can retry its HTTP/SSE path; an actual provider/plugin failure
// uses 502 and never emits a misleading WebSocket 101.
func writeCodexWebSocketPrepareFailure(c *gin.Context, displayName string, err error) {
	if c == nil || err == nil {
		return
	}
	if errors.Is(err, providertransport.ErrCodexPluginUnavailable) ||
		errors.Is(err, providertransport.ErrCodexPluginUnsupported) ||
		errors.Is(err, errCodexWebSocketPrepareNoHeaders) {
		writeCodexWebSocketUpgradeRequired(c, displayName)
		return
	}
	var pluginErr *providertransport.CodexPluginError
	if errors.As(err, &pluginErr) {
		code := strings.ToLower(strings.TrimSpace(pluginErr.Info.Code))
		if code == "unsupported_capability" || code == "unsupported_transport" || code == "unsupported_endpoint" {
			writeCodexWebSocketUpgradeRequired(c, displayName)
			return
		}
	}
	writeError(c, http.StatusBadGateway, "server_error", "websocket_upstream_unavailable", "Codex WebSocket upstream is unavailable")
}
