package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/pricing"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func TestCodexWebSocketFrameQueueSerializesSendAndClose(t *testing.T) {
	queue := newCodexWebSocketFrameQueue(1)
	ctx := context.Background()
	if err := queue.send(ctx, protocol.CodexWebSocketFrame{Type: protocol.CodexWebSocketFrameText}); err != nil {
		t.Fatalf("initial queue send: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = queue.send(ctx, protocol.CodexWebSocketFrame{Type: protocol.CodexWebSocketFramePing})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		queue.close()
	}()
	wg.Wait()
	if err := queue.send(ctx, protocol.CodexWebSocketFrame{Type: protocol.CodexWebSocketFrameClose}); !errors.Is(err, errCodexWebSocketFrameQueueClosed) {
		t.Fatalf("send after close = %v, want queue-closed", err)
	}
}

func TestWebsocketResponseCreateModel(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"model", `{"type":"response.create","model":"gpt-5-codex"}`, "gpt-5-codex"},
		{"nested", `{"type":"response.create","response":{"model":"gpt-5-codex"}}`, "gpt-5-codex"},
		{"wrong event", `{"type":"response.completed","model":"gpt-5-codex"}`, ""},
		{"missing model", `{"type":"response.create"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := websocketResponseCreateModel([]byte(tc.body)); got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWebsocketResponseTurnQueueTracksModelsAndBillingFields(t *testing.T) {
	q := newWebsocketResponseTurnQueue()
	started := time.Now().Add(-time.Second)
	model, err := q.push([]byte(`{"type":"response.create","model":"gpt-first","service_tier":"priority","reasoning":{"effort":"high"}}`), started)
	if err != nil || model != "gpt-first" {
		t.Fatalf("push first turn = (%q, %v)", model, err)
	}
	turn, ok := q.pop()
	if !ok || turn.model != "gpt-first" || turn.req == nil || turn.req.Model != "gpt-first" || !turn.req.Stream {
		t.Fatalf("turn=%+v ok=%v", turn, ok)
	}
	if got := serviceTierOf(turn.req); got != "priority" {
		t.Fatalf("service tier = %q, want priority", got)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := reasoningEffortOf(c, turn.req); got != "high" {
		t.Fatalf("reasoning effort = %q, want high", got)
	}
	if _, err := q.push([]byte(`{"type":"response.create"}`), time.Now()); err == nil {
		t.Fatal("turn queue accepted response.create without the official required model")
	}
}

func TestWebsocketResponseTurnQueueIsFIFOPerStreamLane(t *testing.T) {
	q := newWebsocketResponseTurnQueue()
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"planner","model":"planner-1"}`), time.Now()); err != nil {
		t.Fatalf("push planner-1: %v", err)
	}
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"research","model":"research-1"}`), time.Now()); err != nil {
		t.Fatalf("push research-1: %v", err)
	}
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"planner","model":"planner-2"}`), time.Now()); err != nil {
		t.Fatalf("push planner-2: %v", err)
	}
	if turn, ok := q.pop("research"); !ok || turn.model != "research-1" {
		t.Fatalf("research lane pop = %+v, ok=%v", turn, ok)
	}
	if turn, ok := q.pop("planner"); !ok || turn.model != "planner-1" {
		t.Fatalf("planner first pop = %+v, ok=%v", turn, ok)
	}
	if turn, ok := q.pop("planner"); !ok || turn.model != "planner-2" {
		t.Fatalf("planner second pop = %+v, ok=%v", turn, ok)
	}
	if q.pending() != 0 {
		t.Fatalf("pending turns = %d, want 0", q.pending())
	}
}

func TestWebsocketResponseTurnQueueCapsDistinctNamedStreams(t *testing.T) {
	q := newWebsocketResponseTurnQueue()
	for i := 0; i < codexWebSocketMaxNamedStreams; i++ {
		streamID := fmt.Sprintf("lane-%d", i)
		frame := []byte(`{"type":"response.create","stream_id":"` + streamID + `","model":"gpt"}`)
		if _, err := q.push(frame, time.Now()); err != nil {
			t.Fatalf("push %s: %v", streamID, err)
		}
	}
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"lane-overflow","model":"gpt"}`), time.Now()); !errors.Is(err, errWebsocketNamedStreamLimit) {
		t.Fatalf("overflow push error = %v, want named stream limit", err)
	}
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"lane-0","model":"gpt"}`), time.Now()); err != nil {
		t.Fatalf("reusing existing stream rejected: %v", err)
	}
}

func TestWebsocketResponseTurnQueueRejectsLatePushAfterClose(t *testing.T) {
	q := newWebsocketResponseTurnQueue()
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"main","model":"gpt"}`), time.Now()); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	q.close()
	if _, err := q.push([]byte(`{"type":"response.create","stream_id":"late","model":"gpt"}`), time.Now()); !errors.Is(err, errWebsocketResponseTurnQueueClosed) {
		t.Fatalf("late push error = %v, want queue-closed", err)
	}
	if got := q.pending(); got != 1 {
		t.Fatalf("pending after rejected late push = %d, want 1", got)
	}
	if drained := q.popAll(); len(drained) != 1 || drained[0].streamID != "main" {
		t.Fatalf("drained turns = %+v, want original turn only", drained)
	}
}

func TestWebsocketResponseTerminalCarriesStreamIDAndRecognizesIncompleteFailedError(t *testing.T) {
	for _, typ := range []string{"response.completed", "response.incomplete", "response.failed", "error"} {
		t.Run(typ, func(t *testing.T) {
			payload := `{"type":"` + typ + `","stream_id":"lane-a"}`
			if typ == "response.completed" || typ == "response.incomplete" {
				payload = `{"type":"` + typ + `","stream_id":"lane-a","response":{"model":"gpt-a","usage":{"input_tokens":2,"output_tokens":1}}}`
			}
			term, ok := websocketResponseTerminal([]byte(payload))
			if !ok || term.StreamID != "lane-a" || !term.streamIDPresent {
				t.Fatalf("terminal = %+v, ok=%v", term, ok)
			}
		})
	}
	term, ok := websocketResponseTerminal([]byte(`{"type":"error","error":{"code":"websocket_connection_limit_reached"}}`))
	if !ok || !term.connectionError {
		t.Fatalf("unscoped error = %+v, ok=%v; want connection error", term, ok)
	}
}

func TestWebsocketResponseTerminalMarksMalformedStreamID(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.completed","stream_id":42}`,
		`{"type":"response.completed","stream_id":null}`,
		`{"type":"response.incomplete","stream_id":""}`,
		`{"type":"response.failed","stream_id":"lane with spaces"}`,
		`{"type":"response.completed","stream_id":" lane-a"}`,
		`{"type":"response.completed","stream_id":"lane-a "}`,
	} {
		term, ok := websocketResponseTerminal([]byte(payload))
		if !ok || !term.streamIDMalformed {
			t.Fatalf("terminal = %+v, ok=%v for %s; want malformed stream_id", term, ok, payload)
		}
		if term.StreamID != "" {
			t.Fatalf("malformed terminal stream id = %q, want empty", term.StreamID)
		}
	}
}

func TestWebsocketResponseCompletedUsageRequiresCompletedEvent(t *testing.T) {
	usagePayload := `{"type":"response.completed","response":{"model":"gpt-later","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`
	usage, found, model := websocketResponseCompleted([]byte(usagePayload))
	if !found || usage.PromptTokens != 4 || usage.CompletionTokens != 2 || model != "gpt-later" {
		t.Fatalf("completed usage=(%+v,%v,%q)", usage, found, model)
	}
	if _, found, _ := websocketResponseCompleted([]byte(`{"type":"response.created","usage":{"input_tokens":4}}`)); found {
		t.Fatal("response.created must not be treated as a billable completion")
	}
}

func TestNativeCodexAccountSupportsWebSocketModel(t *testing.T) {
	if !nativeCodexAccountSupportsWebSocketModel(&accountreg.Snapshot{}, "gpt-any") {
		t.Fatal("empty model catalog should be a wildcard")
	}
	acc := &accountreg.Snapshot{Models: map[string]struct{}{"gpt-first": {}}}
	if !nativeCodexAccountSupportsWebSocketModel(acc, "gpt-first") {
		t.Fatal("listed model was rejected")
	}
	if nativeCodexAccountSupportsWebSocketModel(acc, "gpt-missing") {
		t.Fatal("unlisted model was accepted")
	}
}

type responsesWebSocketCompletion struct {
	input  int
	output int
}

type responsesWebSocketTestTransport struct {
	mu          sync.Mutex
	requests    []providertransport.Request
	turns       [][]byte
	completions map[string]responsesWebSocketCompletion
	closeAfter  int
	done        chan struct{}
	doneOnce    sync.Once
}

func newResponsesWebSocketTestTransport(
	completions map[string]responsesWebSocketCompletion,
	closeAfter int,
) *responsesWebSocketTestTransport {
	return &responsesWebSocketTestTransport{
		completions: completions,
		closeAfter:  closeAfter,
		done:        make(chan struct{}),
	}
}

func (t *responsesWebSocketTestTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (*responsesWebSocketTestTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (*responsesWebSocketTestTransport) CodexTransportMode() string { return "native" }

func (t *responsesWebSocketTestTransport) ExecuteWebSocket(
	ctx context.Context,
	req providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	defer func() {
		t.doneOnce.Do(func() { close(t.done) })
	}()
	t.mu.Lock()
	t.requests = append(t.requests, req)
	t.mu.Unlock()
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
	}); err != nil {
		return nil, err
	}

	completed := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil, nil
			}
			switch frame.Type {
			case protocol.CodexWebSocketFrameClose:
				if err := emit(frame); err != nil {
					return nil, err
				}
				return nil, nil
			case protocol.CodexWebSocketFrameText, protocol.CodexWebSocketFrameBinary:
				if websocketResponseEventType(frame.Data) != "response.create" {
					continue
				}
				model := websocketResponseCreateModel(frame.Data)
				t.mu.Lock()
				t.turns = append(t.turns, append([]byte(nil), frame.Data...))
				t.mu.Unlock()
				usage := t.completions[model]
				payload, err := json.Marshal(map[string]any{
					"type": "response.completed",
					"response": map[string]any{
						"model": model,
						"usage": map[string]int{
							"input_tokens":  usage.input,
							"output_tokens": usage.output,
							"total_tokens":  usage.input + usage.output,
						},
					},
				})
				if err != nil {
					return nil, err
				}
				if err := emit(protocol.CodexWebSocketFrame{
					Version: protocol.CodexExecutorVersion,
					Type:    protocol.CodexWebSocketFrameText,
					Data:    payload,
				}); err != nil {
					return nil, err
				}
				completed++
				if t.closeAfter > 0 && completed >= t.closeAfter {
					if err := emit(protocol.CodexWebSocketFrame{
						Version: protocol.CodexExecutorVersion,
						Type:    protocol.CodexWebSocketFrameClose,
						Code:    websocket.CloseNormalClosure,
					}); err != nil {
						return nil, err
					}
					return nil, nil
				}
			}
		}
	}
}

func (t *responsesWebSocketTestTransport) turnsSnapshot() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.turns))
	for i := range t.turns {
		out[i] = append([]byte(nil), t.turns[i]...)
	}
	return out
}

// responsesWebSocketOutOfOrderTransport waits for one turn on each of two
// named lanes, then emits the second lane's terminal event first.  This models
// the official multiplexed WebSocket contract where different stream_id lanes
// may interleave while each lane remains FIFO.
type responsesWebSocketOutOfOrderTransport struct {
	mu       sync.Mutex
	turns    [][]byte
	done     chan struct{}
	doneOnce sync.Once
}

func newResponsesWebSocketOutOfOrderTransport() *responsesWebSocketOutOfOrderTransport {
	return &responsesWebSocketOutOfOrderTransport{done: make(chan struct{})}
}

func (t *responsesWebSocketOutOfOrderTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (*responsesWebSocketOutOfOrderTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (*responsesWebSocketOutOfOrderTransport) CodexTransportMode() string { return "native" }

func (t *responsesWebSocketOutOfOrderTransport) ExecuteWebSocket(
	ctx context.Context,
	_ providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	defer t.doneOnce.Do(func() { close(t.done) })
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
	}); err != nil {
		return nil, err
	}
	var creates [][]byte
	for len(creates) < 2 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil, nil
			}
			switch frame.Type {
			case protocol.CodexWebSocketFrameClose:
				_ = emit(frame)
				return nil, nil
			case protocol.CodexWebSocketFrameText, protocol.CodexWebSocketFrameBinary:
				if websocketResponseEventType(frame.Data) == "response.create" {
					creates = append(creates, append([]byte(nil), frame.Data...))
					t.mu.Lock()
					t.turns = append(t.turns, append([]byte(nil), frame.Data...))
					t.mu.Unlock()
				}
			}
		}
	}
	for i := len(creates) - 1; i >= 0; i-- {
		model := websocketResponseCreateModel(creates[i])
		streamID := websocketResponseStreamID(creates[i])
		input, output := 17, 5
		if streamID == "planner" {
			input, output = 11, 3
		}
		payload, err := json.Marshal(map[string]any{
			"type": "response.completed", "stream_id": streamID,
			"response": map[string]any{
				"model": model,
				"usage": map[string]int{
					"input_tokens": input, "output_tokens": output,
					"total_tokens": input + output,
				},
			},
		})
		if err != nil {
			return nil, err
		}
		if err := emit(protocol.CodexWebSocketFrame{
			Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameText,
			Data: payload,
		}); err != nil {
			return nil, err
		}
	}
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameClose,
		Code: websocket.CloseNormalClosure,
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (t *responsesWebSocketOutOfOrderTransport) turnsSnapshot() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.turns))
	for i := range t.turns {
		out[i] = append([]byte(nil), t.turns[i]...)
	}
	return out
}

type responsesWebSocketTerminalTransport struct {
	eventType string
	streamID  string
	withUsage bool
	done      chan struct{}
	doneOnce  sync.Once
}

type responsesWebSocketNoTerminalTransport struct {
	done     chan struct{}
	doneOnce sync.Once
}

// responsesWebSocketCloseStallTransport accepts a downstream close frame but
// deliberately waits for context cancellation instead of completing its own
// close handshake. It models a plugin/provider that would otherwise leave the
// upgraded HTTP handler blocked forever after the Codex client disconnects.
type responsesWebSocketCloseStallTransport struct {
	turnSeen  chan struct{}
	closeSeen chan struct{}
	done      chan struct{}

	turnOnce  sync.Once
	closeOnce sync.Once
	doneOnce  sync.Once

	mu         sync.Mutex
	closeCount int
	err        error
}

func newResponsesWebSocketCloseStallTransport() *responsesWebSocketCloseStallTransport {
	return &responsesWebSocketCloseStallTransport{
		turnSeen: make(chan struct{}), closeSeen: make(chan struct{}), done: make(chan struct{}),
	}
}

func (t *responsesWebSocketCloseStallTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (*responsesWebSocketCloseStallTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (*responsesWebSocketCloseStallTransport) CodexTransportMode() string { return "native" }

func (t *responsesWebSocketCloseStallTransport) ExecuteWebSocket(
	ctx context.Context,
	_ providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	defer t.doneOnce.Do(func() { close(t.done) })
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
	}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			t.mu.Lock()
			t.err = ctx.Err()
			t.mu.Unlock()
			return nil, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				// A conforming executor would return here. Deliberately keep
				// waiting to verify that Core's close grace period cancels ctx.
				frames = nil
				continue
			}
			switch frame.Type {
			case protocol.CodexWebSocketFrameText, protocol.CodexWebSocketFrameBinary:
				if websocketResponseEventType(frame.Data) == "response.create" {
					t.turnOnce.Do(func() { close(t.turnSeen) })
				}
			case protocol.CodexWebSocketFrameClose:
				t.mu.Lock()
				t.closeCount++
				t.mu.Unlock()
				t.closeOnce.Do(func() { close(t.closeSeen) })
			}
		}
	}
}

func (t *responsesWebSocketCloseStallTransport) snapshot() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeCount, t.err
}

func newResponsesWebSocketNoTerminalTransport() *responsesWebSocketNoTerminalTransport {
	return &responsesWebSocketNoTerminalTransport{done: make(chan struct{})}
}

func (t *responsesWebSocketNoTerminalTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (*responsesWebSocketNoTerminalTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (*responsesWebSocketNoTerminalTransport) CodexTransportMode() string { return "native" }

func (t *responsesWebSocketNoTerminalTransport) ExecuteWebSocket(
	ctx context.Context,
	_ providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	defer t.doneOnce.Do(func() { close(t.done) })
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
	}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-frames:
		if !ok {
			return nil, nil
		}
		if frame.Type == protocol.CodexWebSocketFrameClose {
			// Deliberately end cleanly without emitting response.completed or
			// another terminal event for the queued response.create.
			return nil, nil
		}
		return nil, nil
	}
}

func newResponsesWebSocketTerminalTransport(eventType, streamID string, withUsage bool) *responsesWebSocketTerminalTransport {
	return &responsesWebSocketTerminalTransport{
		eventType: eventType, streamID: streamID, withUsage: withUsage,
		done: make(chan struct{}),
	}
}

func (t *responsesWebSocketTerminalTransport) Execute(context.Context, providertransport.Request) providertransport.Result {
	return providertransport.Result{}
}

func (*responsesWebSocketTerminalTransport) Capabilities() providertransport.Capabilities {
	return providertransport.Capabilities{HTTP: true, Streaming: true, WebSocket: true}
}

func (*responsesWebSocketTerminalTransport) CodexTransportMode() string { return "native" }

func (t *responsesWebSocketTerminalTransport) ExecuteWebSocket(
	ctx context.Context,
	_ providertransport.Request,
	frames <-chan protocol.CodexWebSocketFrame,
	emit func(protocol.CodexWebSocketFrame) error,
) (map[string]string, error) {
	defer t.doneOnce.Do(func() { close(t.done) })
	if err := emit(protocol.CodexWebSocketFrame{
		Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameHeaders,
		StatusCode: http.StatusSwitchingProtocols,
	}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil, nil
			}
			if frame.Type == protocol.CodexWebSocketFrameClose {
				_ = emit(frame)
				return nil, nil
			}
			if frame.Type != protocol.CodexWebSocketFrameText && frame.Type != protocol.CodexWebSocketFrameBinary {
				continue
			}
			if websocketResponseEventType(frame.Data) != "response.create" {
				continue
			}
			model := websocketResponseCreateModel(frame.Data)
			envelope := map[string]any{"type": t.eventType}
			if t.streamID != "" {
				envelope["stream_id"] = t.streamID
			}
			if t.eventType == "response.completed" || t.eventType == "response.incomplete" {
				response := map[string]any{"model": model}
				if t.withUsage {
					response["usage"] = map[string]int{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}
				}
				envelope["response"] = response
			}
			payload, err := json.Marshal(envelope)
			if err != nil {
				return nil, err
			}
			if err := emit(protocol.CodexWebSocketFrame{
				Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameText,
				Data: payload,
			}); err != nil {
				return nil, err
			}
			if err := emit(protocol.CodexWebSocketFrame{
				Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameClose,
				Code: websocket.CloseNormalClosure,
			}); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
}

type responsesWebSocketUsageSink struct {
	mu      sync.Mutex
	records []billing.UsageRecord
}

func (s *responsesWebSocketUsageSink) Record(record billing.UsageRecord) {
	s.mu.Lock()
	s.records = append(s.records, record)
	s.mu.Unlock()
}

func (s *responsesWebSocketUsageSink) snapshot() []billing.UsageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]billing.UsageRecord(nil), s.records...)
}

type responsesWebSocketPriceLoader struct {
	prices map[string]pricing.Price
}

func (l responsesWebSocketPriceLoader) LoadAllPrices(context.Context) (map[string]pricing.Price, error) {
	return l.prices, nil
}

func newResponsesWebSocketPipeline(
	t *testing.T,
	transport providertransport.ProviderTransport,
	rdb *redis.Client,
	maxRPM int,
	models ...string,
) (*Pipeline, *responsesWebSocketUsageSink) {
	t.Helper()
	modelCatalog := make(map[string]struct{}, len(models))
	prices := make(map[string]pricing.Price, len(models))
	for _, model := range models {
		modelCatalog[model] = struct{}{}
		prices[model] = pricing.Price{Input: 10, Output: 30}
	}
	accounts := accountreg.New(websocketAccountLoader{accounts: []accountreg.Snapshot{{
		ID: 41, Name: "codex-ws", Platform: "codex", Type: "oauth", State: accountreg.StateActive,
		Credentials: map[string]string{"access_token": "provider-token", "base_url": "https://provider.test/v1"},
		Models:      modelCatalog,
		Priority:    10,
		Weight:      10,
		MaxRPM:      maxRPM,
		// Keep account capacity above one so this fixture can exercise the
		// official multiplexed multi-lane contract; RPM tests still constrain
		// turns independently.
		MaxConcurrency: 16,
		GroupIDs:       map[int]struct{}{7: {}},
	}}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatalf("load account registry: %v", err)
	}
	priceCache := pricing.NewCache(responsesWebSocketPriceLoader{prices: prices})
	if err := priceCache.Reload(context.Background()); err != nil {
		t.Fatalf("load pricing: %v", err)
	}
	sink := &responsesWebSocketUsageSink{}
	return New(Options{
		Accounts:          accounts,
		Pricing:           priceCache,
		Concurrency:       scheduler.NewConcurrencyManager(rdb),
		RPM:               scheduler.NewRPMCounter(rdb),
		Sink:              sink,
		ProviderTransport: transport,
	}), sink
}

func responsesWebSocketServer(p *Pipeline) *httptest.Server {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/v1/responses", func(c *gin.Context) {
		// This fixture represents the official Codex websocket client. Shared
		// /v1 aliases require an explicit client identity before entering the
		// native Codex websocket path.
		c.Request.Header.Set("Originator", "codex_cli_rs")
		c.Set(middleware.CtxKeyKeyInfo, &auth.APIKeyInfo{
			KeyID: 11, UserID: 22, UserEmail: "ws@example.com", GroupID: 7, UserBalance: 100,
			UserMaxConcurrency: 8, KeyMaxConcurrency: 8,
		})
	}, p.HandleResponsesWebSocket)
	return httptest.NewServer(engine)
}

func readResponsesWebSocketEvent(t *testing.T, conn *websocket.Conn, eventType string) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %s event: %v", eventType, err)
		}
		if messageType == websocket.TextMessage && websocketResponseEventType(data) == eventType {
			return data
		}
	}
}

func TestResponsesWebSocketBillsEachTurnWithItsOwnModelAndRequestFields(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketTestTransport(map[string]responsesWebSocketCompletion{
		"gpt-first":  {input: 11, output: 3},
		"gpt-second": {input: 17, output: 5},
	}, 2)
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first", "gpt-second")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial Responses websocket: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()

	requests := []string{
		`{"type":"response.create","model":"gpt-first","stream":true,"service_tier":"priority","reasoning":{"effort":"high"}}`,
		`{"type":"response.create","model":"gpt-second","stream":true,"service_tier":"flex","reasoning":{"effort":"low"}}`,
	}
	for _, request := range requests {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
			t.Fatalf("write response.create: %v", err)
		}
		readResponsesWebSocketEvent(t, conn, "response.completed")
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("provider close frame was not forwarded")
	} else if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("websocket close = %v, want 1000", err)
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not finish")
	}

	records := sink.snapshot()
	if len(records) != 2 {
		t.Fatalf("usage records = %d, want 2: %+v", len(records), records)
	}
	for i, want := range []struct {
		model  string
		tier   string
		effort string
		input  int
		output int
	}{{"gpt-first", "priority", "high", 11, 3}, {"gpt-second", "flex", "low", 17, 5}} {
		got := records[i]
		if got.Model != want.model || got.ServiceTier != want.tier || got.ReasoningEffort != want.effort ||
			got.InputTokens != want.input || got.OutputTokens != want.output {
			t.Errorf("usage record %d = model:%q tier:%q effort:%q input:%d output:%d, want %+v",
				i, got.Model, got.ServiceTier, got.ReasoningEffort, got.InputTokens, got.OutputTokens, want)
		}
	}
	if rpm := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; rpm != 2 {
		t.Fatalf("account RPM = %d, want one unit for each of 2 response.create turns", rpm)
	}
	turns := transport.turnsSnapshot()
	if len(turns) != 2 || websocketResponseCreateModel(turns[0]) != "gpt-first" || websocketResponseCreateModel(turns[1]) != "gpt-second" {
		t.Fatalf("provider turns = %q", turns)
	}
}

func TestResponsesWebSocketBillsOutOfOrderLanesByStreamID(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketOutOfOrderTransport()
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-planner", "gpt-research")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial Responses websocket: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	for _, request := range []string{
		`{"type":"response.create","stream_id":"planner","model":"gpt-planner","stream":true}`,
		`{"type":"response.create","stream_id":"research","model":"gpt-research","stream":true}`,
	} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
			t.Fatalf("write response.create: %v", err)
		}
	}
	seen := make(map[string]bool)
	for len(seen) < 2 {
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatalf("set websocket read deadline: %v", err)
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read multiplexed completion: %v", err)
		}
		if messageType != websocket.TextMessage || websocketResponseEventType(data) != "response.completed" {
			continue
		}
		term, ok := websocketResponseTerminal(data)
		if !ok || term.StreamID == "" {
			t.Fatalf("completion missing stream_id: %s", data)
		}
		seen[term.StreamID] = true
	}
	if !seen["planner"] || !seen["research"] {
		t.Fatalf("completion lanes = %+v", seen)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("provider close frame was not forwarded")
	} else if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("websocket close = %v, want 1000", err)
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not finish")
	}

	records := sink.snapshot()
	if len(records) != 2 {
		t.Fatalf("usage records = %d, want 2: %+v", len(records), records)
	}
	byModel := make(map[string]billing.UsageRecord, len(records))
	for _, record := range records {
		byModel[record.Model] = record
	}
	if got := byModel["gpt-planner"]; got.InputTokens != 11 || got.OutputTokens != 3 {
		t.Fatalf("planner usage = %+v, want input=11 output=3", got)
	}
	if got := byModel["gpt-research"]; got.InputTokens != 17 || got.OutputTokens != 5 {
		t.Fatalf("research usage = %+v, want input=17 output=5", got)
	}
	if rpm := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; rpm != 2 {
		t.Fatalf("account RPM = %d, want 2", rpm)
	}
}

func TestResponsesWebSocketCompletedWithoutUsageRecordsMissingAndReleasesTurnSlots(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketTerminalTransport("response.completed", "main", false)
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","stream_id":"main","model":"gpt-first","stream":true}`,
	)); err != nil {
		t.Fatalf("write response.create: %v", err)
	}
	readResponsesWebSocketEvent(t, conn, "response.completed")
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("provider close frame was not forwarded")
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not finish")
	}

	records := sink.snapshot()
	if len(records) != 1 || records[0].UsageStatus != billing.UsageStatusMissing {
		t.Fatalf("usage records = %+v, want one usage_missing record", records)
	}
	if got := p.concurrency.GetUserCurrentCounts(context.Background(), []int{22})[22]; got != 0 {
		t.Fatalf("user in-flight slots = %d, want 0", got)
	}
	if got := p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41]; got != 0 {
		t.Fatalf("account in-flight slots = %d, want 0", got)
	}
	if got := p.concurrency.GetGroupCurrentCounts(context.Background(), []int{7})[7]; got != 0 {
		t.Fatalf("group in-flight slots = %d, want 0", got)
	}
}

func TestResponsesWebSocketWarmupHoldsSlotsButSkipsBillingAndRPM(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketTerminalTransport("response.completed", "main", false)
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","stream_id":"main","model":"gpt-first","generate":false}`,
	)); err != nil {
		t.Fatalf("write warmup response.create: %v", err)
	}
	readResponsesWebSocketEvent(t, conn, "response.completed")
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("provider close frame was not forwarded")
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not finish")
	}
	if records := sink.snapshot(); len(records) != 0 {
		t.Fatalf("warmup produced usage records: %+v", records)
	}
	if rpm := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; rpm != 0 {
		t.Fatalf("warmup account RPM = %d, want 0", rpm)
	}
	if got := p.concurrency.GetUserCurrentCounts(context.Background(), []int{22})[22]; got != 0 {
		t.Fatalf("warmup user in-flight slots = %d, want 0", got)
	}
	if got := p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41]; got != 0 {
		t.Fatalf("warmup account in-flight slots = %d, want 0", got)
	}
}

func TestResponsesWebSocketIncompleteWithUsageBillsAndReleasesTurn(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketTerminalTransport("response.incomplete", "main", true)
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","stream_id":"main","model":"gpt-first","stream":true}`,
	)); err != nil {
		t.Fatalf("write response.create: %v", err)
	}
	readResponsesWebSocketEvent(t, conn, "response.incomplete")
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("provider close frame was not forwarded")
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not finish")
	}

	records := sink.snapshot()
	if len(records) != 1 || records[0].InputTokens != 4 || records[0].OutputTokens != 2 ||
		records[0].UsageStatus != billing.UsageStatusCompleted {
		t.Fatalf("incomplete usage records = %+v", records)
	}
	if got := p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41]; got != 0 {
		t.Fatalf("account in-flight slots = %d, want 0", got)
	}
}

func TestResponsesWebSocketFailedAndScopedErrorReleaseWithoutBilling(t *testing.T) {
	for _, eventType := range []string{"response.failed", "error"} {
		t.Run(eventType, func(t *testing.T) {
			mini := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			transport := newResponsesWebSocketTerminalTransport(eventType, "main", false)
			p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
			server := responsesWebSocketServer(p)
			defer server.Close()

			conn, _, err := websocket.DefaultDialer.Dial(
				"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
			)
			if err != nil {
				t.Fatalf("dial Responses websocket: %v", err)
			}
			defer conn.Close()
			if err := conn.WriteMessage(websocket.TextMessage, []byte(
				`{"type":"response.create","stream_id":"main","model":"gpt-first","stream":true}`,
			)); err != nil {
				t.Fatalf("write response.create: %v", err)
			}
			readResponsesWebSocketEvent(t, conn, eventType)
			if _, _, err := conn.ReadMessage(); err == nil {
				t.Fatal("provider close frame was not forwarded")
			}
			select {
			case <-transport.done:
			case <-time.After(3 * time.Second):
				t.Fatal("websocket transport did not finish")
			}
			if records := sink.snapshot(); len(records) != 0 {
				t.Fatalf("failed/error turn was billed: %+v", records)
			}
			if got := p.concurrency.GetUserCurrentCounts(context.Background(), []int{22})[22]; got != 0 {
				t.Fatalf("user in-flight slots = %d, want 0", got)
			}
			if got := p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41]; got != 0 {
				t.Fatalf("account in-flight slots = %d, want 0", got)
			}
		})
	}
}

func TestResponsesWebSocketCleanEOFWithoutTerminalIsFailureAndReleasesTurn(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketNoTerminalTransport()
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","stream_id":"main","model":"gpt-first","stream":true}`,
	)); err != nil {
		t.Fatalf("write response.create: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("clean EOF without terminal did not close downstream")
	} else if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseInternalServerErr {
		t.Fatalf("websocket close = %v, want 1011", err)
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not finish")
	}
	if records := sink.snapshot(); len(records) != 1 || records[0].UsageStatus != billing.UsageStatusStreamAbortedUsageMissing {
		t.Fatalf("clean EOF usage records = %+v, want one stream_aborted_usage_missing record", records)
	}
	if got := p.concurrency.GetUserCurrentCounts(context.Background(), []int{22})[22]; got != 0 {
		t.Fatalf("user in-flight slots = %d, want 0", got)
	}
	if got := p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41]; got != 0 {
		t.Fatalf("account in-flight slots = %d, want 0", got)
	}
}

func TestResponsesWebSocketDownstreamCloseCancelsStalledExecutor(t *testing.T) {
	oldGrace := codexWebSocketCloseGracePeriod
	codexWebSocketCloseGracePeriod = 25 * time.Millisecond
	t.Cleanup(func() { codexWebSocketCloseGracePeriod = oldGrace })

	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketCloseStallTransport()
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","model":"gpt-first","stream":true}`,
	)); err != nil {
		t.Fatalf("write response.create: %v", err)
	}
	select {
	case <-transport.turnSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not receive response.create")
	}
	if err := conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client done"),
		time.Now().Add(time.Second)); err != nil {
		t.Fatalf("write downstream close: %v", err)
	}
	select {
	case <-transport.closeSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("downstream close was not forwarded to executor")
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stalled executor was not cancelled after close grace period")
	}
	closeCount, execErr := transport.snapshot()
	if closeCount != 1 {
		t.Fatalf("executor close frame count = %d, want 1", closeCount)
	}
	if !errors.Is(execErr, context.Canceled) {
		t.Fatalf("executor error = %v, want context.Canceled", execErr)
	}
	deadline := time.Now().Add(3 * time.Second)
	released := false
	for time.Now().Before(deadline) {
		if p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41] == 0 &&
			p.concurrency.GetUserCurrentCounts(context.Background(), []int{22})[22] == 0 &&
			len(sink.snapshot()) == 1 {
			released = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !released {
		t.Fatalf("stalled executor cleanup incomplete: account=%d user=%d records=%+v",
			p.concurrency.GetAccountCurrentCounts(context.Background(), []int{41})[41],
			p.concurrency.GetUserCurrentCounts(context.Background(), []int{22})[22], sink.snapshot())
	}
	// The turn reached the executor before the downstream closed, so Core must
	// retain its RPM charge while releasing the in-flight capacity slots.
	if got := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; got != 1 {
		t.Fatalf("account RPM after stalled executor cancellation = %d, want 1", got)
	}
	if records := sink.snapshot(); len(records) != 1 || records[0].UsageStatus != billing.UsageStatusStreamAbortedUsageMissing {
		t.Fatalf("stalled executor usage records = %+v, want one stream_aborted_usage_missing record", records)
	}
}

func TestResponsesWebSocketUnsupportedLaterModelCloses1013WithoutForwardOrRPMCharge(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketTestTransport(map[string]responsesWebSocketCompletion{
		"gpt-first": {input: 7, output: 2},
	}, 0)
	p, sink := newResponsesWebSocketPipeline(t, transport, rdb, 10, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial Responses websocket: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","model":"gpt-first","stream":true,"service_tier":"priority","reasoning":{"effort":"high"}}`,
	)); err != nil {
		t.Fatalf("write first response.create: %v", err)
	}
	readResponsesWebSocketEvent(t, conn, "response.completed")
	if err := conn.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"response.create","model":"gpt-unavailable","stream":true}`,
	)); err != nil {
		t.Fatalf("write unsupported response.create: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("unsupported model did not close the websocket")
	} else if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("websocket close = %v, want 1013", err)
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not stop after model rejection")
	}

	turns := transport.turnsSnapshot()
	if len(turns) != 1 || websocketResponseCreateModel(turns[0]) != "gpt-first" {
		t.Fatalf("unsupported turn reached provider: %q", turns)
	}
	if rpm := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; rpm != 1 {
		t.Fatalf("account RPM = %d, want only the forwarded first turn", rpm)
	}
	if records := sink.snapshot(); len(records) != 1 || records[0].Model != "gpt-first" {
		t.Fatalf("usage records after unsupported model = %+v", records)
	}
}

func TestResponsesWebSocketAppliesAccountRPMToEveryTurn(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	transport := newResponsesWebSocketTestTransport(map[string]responsesWebSocketCompletion{
		"gpt-first": {input: 7, output: 2},
	}, 0)
	p, _ := newResponsesWebSocketPipeline(t, transport, rdb, 1, "gpt-first")
	server := responsesWebSocketServer(p)
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial Responses websocket: %v (status=%d)", err, response.StatusCode)
		}
		t.Fatalf("dial Responses websocket: %v", err)
	}
	defer conn.Close()
	create := []byte(`{"type":"response.create","model":"gpt-first","stream":true}`)
	if err := conn.WriteMessage(websocket.TextMessage, create); err != nil {
		t.Fatalf("write first response.create: %v", err)
	}
	readResponsesWebSocketEvent(t, conn, "response.completed")
	if err := conn.WriteMessage(websocket.TextMessage, create); err != nil {
		t.Fatalf("write second response.create: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("second turn above MaxRPM did not close the websocket")
	} else if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("websocket close = %v, want 1013", err)
	}
	select {
	case <-transport.done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket transport did not stop after RPM rejection")
	}
	if turns := transport.turnsSnapshot(); len(turns) != 1 {
		t.Fatalf("RPM-rejected turn reached provider: %q", turns)
	}
	if rpm := p.rpm.GetAccountRPMs(context.Background(), []int{41})[41]; rpm != 1 {
		t.Fatalf("account RPM = %d, want the configured limit 1", rpm)
	}
}

func TestNormalizeCodexGuardianResponseCreateRemovesServiceTier(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "guardian field", in: `{"type":"response.create","model":"codex-auto-review","service_tier":"priority"}`, want: ""},
		{name: "guardian null field", in: `{"type":"response.create","model":"codex-auto-review","service_tier":null}`, want: ""},
		{name: "non-create unchanged", in: `{"type":"response.completed","service_tier":"priority"}`, want: `{"type":"response.completed","service_tier":"priority"}`},
		{name: "malformed unchanged", in: `{"type":"response.create"`, want: `{"type":"response.create"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeCodexGuardianResponseCreate([]byte(tc.in))
			if tc.want != "" {
				if string(got) != tc.want {
					t.Fatalf("normalized = %q, want %q", got, tc.want)
				}
				return
			}
			var envelope map[string]any
			if err := json.Unmarshal(got, &envelope); err != nil {
				t.Fatalf("normalized JSON invalid: %v (%q)", err, got)
			}
			if _, exists := envelope["service_tier"]; exists {
				t.Fatalf("service_tier remained in normalized request: %s", got)
			}
			if envelope["type"] != "response.create" {
				t.Fatalf("type changed: %s", got)
			}
		})
	}
}

func TestCodexGuardianWebSocketEndpointClassification(t *testing.T) {
	if !isCodexGuardianWebSocketEndpoint("guardian") || !isCodexGuardianWebSocketEndpoint("GUARDIAN_CLASSIFIER") {
		t.Fatal("Guardian endpoints were not classified as native control-plane WebSockets")
	}
	if isCodexGuardianWebSocketEndpoint("responses") || isCodexGuardianWebSocketEndpoint("realtime_sideband") {
		t.Fatal("ordinary native WebSocket endpoints were misclassified as Guardian")
	}
}

func TestWebsocketRequestModelHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		path   string
		header string
		want   string
	}{
		{name: "query", path: "/responses?model=gpt-query", want: "gpt-query"},
		{name: "codex model header", path: "/responses", header: "gpt-header", want: "gpt-header"},
		{name: "routing hint", path: "/responses", header: "model=gpt-routing;tier=fast", want: "gpt-routing"},
		{name: "routing hint comma", path: "/responses", header: "tier=fast,model=gpt-comma", want: "gpt-comma"},
		{name: "none", path: "/responses", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.name == "codex model header" {
				req.Header.Set("X-Codex-Model", tc.header)
			} else if strings.Contains(tc.name, "routing") {
				req.Header.Set("X-Codex-Routing-Hint", tc.header)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			if got := websocketRequestModelHint(c); got != tc.want {
				t.Fatalf("model hint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodexRemoteControlWebSocketNameFallbackDecodesOfficialHeaderOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		headerName string
		header     string
		want       string
	}{
		{name: "official standard base64", headerName: "X-Codex-Name", header: base64.StdEncoding.EncodeToString([]byte("Remote Server")), want: "Remote Server"},
		{name: "official raw base64", headerName: "X-Codex-Name", header: base64.RawStdEncoding.EncodeToString([]byte("\u673a")), want: "\u673a"},
		{name: "decoded value preserves whitespace and punctuation", headerName: "X-Codex-Name", header: base64.StdEncoding.EncodeToString([]byte(" Remote\\Server ")), want: " Remote\\Server "},
		{name: "url-safe value is not decoded", headerName: "X-Codex-Name", header: "SGVsbG8_", want: "SGVsbG8_"},
		{name: "malformed preserves value", headerName: "X-Codex-Name", header: "not a base64 value", want: "not a base64 value"},
		{name: "legacy alias remains raw", headerName: "X-Remote-Control-Name", header: "Remote Server", want: "Remote Server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/remote/control/server", nil)
			req.Header.Set(tc.headerName, tc.header)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			if got := codexRemoteControlWebSocketNameFallback(c); got != tc.want {
				t.Fatalf("name fallback = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodexWebSocketCloseForError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   int
		reason string
	}{
		{name: "nil", code: 1000, reason: ""},
		{
			name: "invalid frame",
			err:  &providertransport.CodexPluginError{Info: protocol.CodexExecutorError{Code: "invalid_frame"}},
			code: 1002, reason: "invalid websocket frame",
		},
		{
			name: "unsupported capability",
			err:  &providertransport.CodexPluginError{Info: protocol.CodexExecutorError{Code: "unsupported_capability"}},
			code: 1013, reason: "native Codex transport unavailable",
		},
		{
			name: "upstream rate limit",
			err:  &providertransport.CodexPluginError{Info: protocol.CodexExecutorError{Code: "upstream_http_error", UpstreamStatus: http.StatusTooManyRequests}},
			code: 1013, reason: "native Codex transport unavailable",
		},
		{
			name: "upstream client error",
			err:  &providertransport.CodexPluginError{Info: protocol.CodexExecutorError{Code: "upstream_http_error", UpstreamStatus: http.StatusBadRequest}},
			code: 1011, reason: "native Codex transport failed",
		},
		{
			name: "wrapped unavailable",
			err:  fmt.Errorf("executor stopped: %w", providertransport.ErrCodexPluginUnavailable),
			code: 1013, reason: "native Codex transport unavailable",
		},
		{
			name: "generic",
			err:  errors.New("internal detail must not cross websocket boundary"),
			code: 1011, reason: "native Codex transport failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, reason := codexWebSocketCloseForError(tc.err)
			if code != tc.code || reason != tc.reason {
				t.Fatalf("close = (%d, %q), want (%d, %q)", code, reason, tc.code, tc.reason)
			}
			if len([]byte(reason)) > 123 {
				t.Fatalf("close reason exceeds RFC 6455 limit: %d bytes", len([]byte(reason)))
			}
			if tc.err != nil && strings.Contains(reason, "internal detail") {
				t.Fatal("internal error detail leaked into close reason")
			}
		})
	}
}

type recordingCodexWebSocketWriter struct {
	deadline    time.Time
	deadlineErr error
	messageType int
	messageData []byte
}

func (w *recordingCodexWebSocketWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.deadlineErr
}

func (w *recordingCodexWebSocketWriter) WriteMessage(messageType int, data []byte) error {
	w.messageType = messageType
	w.messageData = append([]byte(nil), data...)
	return nil
}

func (*recordingCodexWebSocketWriter) WriteControl(int, []byte, time.Time) error { return nil }

func TestWriteCodexWebSocketDataFrameSetsDeadline(t *testing.T) {
	for _, test := range []struct {
		name        string
		frameType   string
		messageType int
	}{
		{name: "text", frameType: protocol.CodexWebSocketFrameText, messageType: websocket.TextMessage},
		{name: "binary", frameType: protocol.CodexWebSocketFrameBinary, messageType: websocket.BinaryMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingCodexWebSocketWriter{}
			before := time.Now()
			if err := writeCodexWebSocketFrame(writer, protocol.CodexWebSocketFrame{Type: test.frameType, Data: []byte("payload")}); err != nil {
				t.Fatalf("write frame: %v", err)
			}
			after := time.Now()
			if writer.deadline.Before(before.Add(codexWebSocketWriteTimeout)) || writer.deadline.After(after.Add(codexWebSocketWriteTimeout)) {
				t.Fatalf("write deadline %v is outside expected interval [%v, %v]", writer.deadline, before.Add(codexWebSocketWriteTimeout), after.Add(codexWebSocketWriteTimeout))
			}
			if writer.messageType != test.messageType || string(writer.messageData) != "payload" {
				t.Fatalf("message=(%d, %q), want (%d, payload)", writer.messageType, writer.messageData, test.messageType)
			}
		})
	}
}

func TestWriteCodexWebSocketDataFrameStopsOnDeadlineError(t *testing.T) {
	wantErr := errors.New("deadline unavailable")
	writer := &recordingCodexWebSocketWriter{deadlineErr: wantErr}
	err := writeCodexWebSocketFrame(writer, protocol.CodexWebSocketFrame{Type: protocol.CodexWebSocketFrameText, Data: []byte("payload")})
	if !errors.Is(err, wantErr) || writer.messageData != nil {
		t.Fatalf("err=%v message=%q, want deadline error before write", err, writer.messageData)
	}
}
