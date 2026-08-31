package pipeline

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/gorilla/websocket"
)

type preparedWebSocketTestTransport struct {
	mu         sync.Mutex
	requests   []providertransport.Request
	err        error
	block      <-chan struct{}
	firstSeen  chan struct{}
	firstOnce  sync.Once
	emitBefore bool
}

func (t *preparedWebSocketTestTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (*preparedWebSocketTestTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (*preparedWebSocketTestTransport) CodexTransportMode() string { return "native" }

func (t *preparedWebSocketTestTransport) ExecuteWebSocket(
	ctx context.Context,
	req providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	t.mu.Lock()
	t.requests = append(t.requests, req)
	t.mu.Unlock()
	if t.err != nil {
		return nil, t.err
	}
	if t.block != nil {
		select {
		case <-t.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := emit(protocol.CodexWebSocketFrame{
		Version:    protocol.CodexExecutorVersion,
		Type:       protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
		Header: map[string][]string{
			"X-Codex-Turn-State":   {"turn-state-1"},
			"X-Reasoning-Included": {"true"},
			"OpenAI-Model":         {"gpt-hinted"},
			"X-Models-Etag":        {"etag-1"},
			"Authorization":        {"Bearer should-not-cross"},
			"X-Not-Allowed":        {"drop-me"},
		},
	}); err != nil {
		return nil, err
	}
	if t.emitBefore {
		if err := emit(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameText, Data: []byte(`{"type":"response.created","model":"gpt-hinted"}`)}); err != nil {
			return nil, err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil, nil
			}
			if frame.Type == protocol.CodexWebSocketFrameText || frame.Type == protocol.CodexWebSocketFrameBinary {
				t.firstOnce.Do(func() {
					if t.firstSeen != nil {
						close(t.firstSeen)
					}
				})
				if err := emit(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameText, Data: []byte(`{"type":"response.completed","response":{"model":"gpt-hinted","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)}); err != nil {
					return nil, err
				}
			}
			if frame.Type == protocol.CodexWebSocketFrameClose {
				_ = emit(frame)
				return nil, nil
			}
		}
	}
}

func TestPreparedResponsesWebSocketProjects101HeadersAndBuffersProviderFrame(t *testing.T) {
	transport := &preparedWebSocketTestTransport{emitBefore: true, firstSeen: make(chan struct{})}
	mini := newTestMiniRedis(t)
	p, _ := newResponsesWebSocketPipeline(t, transport, mini, 10, "gpt-hinted")
	server := responsesWebSocketServer(p)
	defer server.Close()

	dialHeader := http.Header{"X-Codex-Routing-Hint": []string{"model=gpt-hinted;tier=fast"}}
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", dialHeader)
	if err != nil {
		t.Fatalf("prepared websocket dial: %v (status=%d)", err, responseStatus(response))
	}
	defer conn.Close()
	if response.Header.Get("X-Codex-Turn-State") != "turn-state-1" ||
		response.Header.Get("X-Reasoning-Included") != "true" ||
		response.Header.Get("OpenAI-Model") != "gpt-hinted" ||
		response.Header.Get("X-Models-Etag") != "etag-1" {
		t.Fatalf("projected handshake headers = %#v", response.Header)
	}
	if response.Header.Get("Authorization") != "" || response.Header.Get("X-Not-Allowed") != "" {
		t.Fatalf("unsafe handshake headers crossed boundary: %#v", response.Header)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"gpt-hinted","stream":true}`)); err != nil {
		t.Fatalf("write response.create: %v", err)
	}
	if _, data, err := conn.ReadMessage(); err != nil || !strings.Contains(string(data), `"response.created"`) {
		t.Fatalf("buffered provider frame = %q, err=%v", data, err)
	}
	if _, data, err := conn.ReadMessage(); err != nil || !strings.Contains(string(data), `"response.completed"`) {
		t.Fatalf("provider completion = %q, err=%v", data, err)
	}
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
}

func TestPreparedResponsesWebSocketUpstreamFailureDoesNotCommit101(t *testing.T) {
	transport := &preparedWebSocketTestTransport{err: &providertransport.CodexPluginError{Info: protocol.CodexExecutorError{
		Code: "upstream_http_error", Message: "upstream failed", Phase: "before_headers", Retryable: true,
	}}}
	mini := newTestMiniRedis(t)
	p, _ := newResponsesWebSocketPipeline(t, transport, mini, 10, "gpt-hinted")
	server := responsesWebSocketServer(p)
	defer server.Close()

	reqHeader := http.Header{"X-Codex-Routing-Hint": []string{"model=gpt-hinted"}}
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", reqHeader)
	if err == nil {
		_ = conn.Close()
		t.Fatal("upstream failure unexpectedly committed websocket 101")
	}
	if response == nil || response.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("failure response = %#v, err=%v", response, err)
	}
}

func TestPreparedResponsesWebSocketTimeoutReleasesAccountRPM(t *testing.T) {
	oldTimeout := codexWebSocketPrepareTimeout
	codexWebSocketPrepareTimeout = 20 * time.Millisecond
	t.Cleanup(func() { codexWebSocketPrepareTimeout = oldTimeout })
	transport := &preparedWebSocketTestTransport{block: make(chan struct{})}
	mini := newTestMiniRedis(t)
	p, _ := newResponsesWebSocketPipeline(t, transport, mini, 1, "gpt-hinted")
	server := responsesWebSocketServer(p)
	defer server.Close()

	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{"X-Codex-Routing-Hint": []string{"model=gpt-hinted"}})
	if err == nil {
		t.Fatal("timeout unexpectedly upgraded websocket")
	}
	if response == nil || response.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("timeout response = %#v, err=%v", response, err)
	}
	if got := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; got != 0 {
		t.Fatalf("account RPM after prepared timeout = %d, want 0", got)
	}
}

func newTestMiniRedis(t *testing.T) *redis.Client {
	t.Helper()
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func responseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}
