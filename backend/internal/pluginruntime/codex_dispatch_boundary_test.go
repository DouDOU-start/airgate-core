package pluginruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
)

type codexDispatchFixture struct {
	emit  func(func(protocol.CodexExecuteEvent) error) error
	err   error
	calls atomic.Int32
}

type codexWebSocketDispatchFixture struct {
	calls atomic.Int32
}

type blockingCodexWebSocketDispatchFixture struct {
	started  chan struct{}
	canceled chan struct{}
}

type blockingCodexStreamDispatchFixture struct {
	started  chan struct{}
	canceled chan struct{}
}

func (f *codexDispatchFixture) Info() protocol.PluginInfo                   { return protocol.PluginInfo{ID: "fixture"} }
func (*codexDispatchFixture) Init(context.Context, map[string]string) error { return nil }
func (*codexDispatchFixture) Start(context.Context) error                   { return nil }
func (*codexDispatchFixture) Stop(context.Context) error                    { return nil }
func (*codexDispatchFixture) Handle(context.Context, protocol.Request) (protocol.Response, error) {
	return protocol.Response{}, nil
}
func (f *codexDispatchFixture) ExecuteStream(_ context.Context, _ protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	f.calls.Add(1)
	if f.emit != nil {
		if err := f.emit(emit); err != nil {
			return err
		}
	}
	return f.err
}

func (f *codexWebSocketDispatchFixture) Info() protocol.PluginInfo {
	return protocol.PluginInfo{ID: "fixture-ws"}
}
func (*codexWebSocketDispatchFixture) Init(context.Context, map[string]string) error { return nil }
func (*codexWebSocketDispatchFixture) Start(context.Context) error                   { return nil }
func (*codexWebSocketDispatchFixture) Stop(context.Context) error                    { return nil }
func (*codexWebSocketDispatchFixture) Handle(context.Context, protocol.Request) (protocol.Response, error) {
	return protocol.Response{}, nil
}
func (f *codexWebSocketDispatchFixture) ExecuteWebSocket(_ context.Context, _ protocol.CodexExecuteRequest, _ <-chan protocol.CodexWebSocketFrame, emit func(protocol.CodexWebSocketFrame) error) error {
	f.calls.Add(1)
	return emit(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameText, Data: []byte("frame")})
}

func (p *blockingCodexWebSocketDispatchFixture) Info() protocol.PluginInfo {
	return protocol.PluginInfo{ID: "blocking-ws-fixture"}
}
func (*blockingCodexWebSocketDispatchFixture) Init(context.Context, map[string]string) error {
	return nil
}
func (*blockingCodexWebSocketDispatchFixture) Start(context.Context) error { return nil }
func (*blockingCodexWebSocketDispatchFixture) Stop(context.Context) error  { return nil }
func (*blockingCodexWebSocketDispatchFixture) Handle(context.Context, protocol.Request) (protocol.Response, error) {
	return protocol.Response{}, nil
}
func (p *blockingCodexWebSocketDispatchFixture) ExecuteWebSocket(ctx context.Context, _ protocol.CodexExecuteRequest, _ <-chan protocol.CodexWebSocketFrame, emit func(protocol.CodexWebSocketFrame) error) error {
	close(p.started)
	// Deliberately ignore the consumer error. A real third-party executor may
	// do the same while waiting for its own cleanup; Manager must cancel the
	// child call context so this wait cannot retain the WebSocket forever.
	_ = emit(protocol.CodexWebSocketFrame{Version: protocol.CodexExecutorVersion, Type: protocol.CodexWebSocketFrameText, Data: []byte("frame")})
	<-ctx.Done()
	close(p.canceled)
	return ctx.Err()
}

func (p *blockingCodexStreamDispatchFixture) Info() protocol.PluginInfo {
	return protocol.PluginInfo{ID: "blocking-stream-fixture"}
}
func (*blockingCodexStreamDispatchFixture) Init(context.Context, map[string]string) error { return nil }
func (*blockingCodexStreamDispatchFixture) Start(context.Context) error                   { return nil }
func (*blockingCodexStreamDispatchFixture) Stop(context.Context) error                    { return nil }
func (*blockingCodexStreamDispatchFixture) Handle(context.Context, protocol.Request) (protocol.Response, error) {
	return protocol.Response{}, nil
}
func (p *blockingCodexStreamDispatchFixture) ExecuteStream(ctx context.Context, _ protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	close(p.started)
	// Deliberately ignore the callback error. This models an executor that has
	// already handed the event to its own pipeline and is waiting for upstream
	// cleanup; Manager must cancel the child context on consumer failure.
	_ = emit(protocol.CodexExecuteEvent{Version: protocol.CodexExecutorVersion, Type: protocol.CodexEventData, Data: []byte("data")})
	<-ctx.Done()
	close(p.canceled)
	return ctx.Err()
}

func newCodexDispatchInstance(id string, priority int32, plugin *codexDispatchFixture) *instance {
	info := protocol.PluginInfo{
		ID: id, Name: id, ProtocolVersion: protocol.ProtocolVersion,
		Priority: priority, Capabilities: []string{protocol.CapabilityCodexExecutorV1},
	}
	return &instance{id: id, name: id, info: info, plugin: plugin, started: true}
}

func TestExecuteCodexDoesNotReplayAfterResponseHeaders(t *testing.T) {
	firstErr := errors.New("executor disconnected after headers")
	first := &codexDispatchFixture{
		err: firstErr,
		emit: func(emit func(protocol.CodexExecuteEvent) error) error {
			return emit(protocol.CodexExecuteEvent{
				Version:    protocol.CodexExecutorVersion,
				Type:       protocol.CodexEventResponseHeaders,
				StatusCode: 200,
			})
		},
	}
	second := &codexDispatchFixture{}
	m := &Manager{
		instances: map[string]*instance{
			"first":  newCodexDispatchInstance("first", 1, first),
			"second": newCodexDispatchInstance("second", 2, second),
		},
		lastErrors: make(map[string]string),
	}

	err := m.ExecuteCodex(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, func(protocol.CodexExecuteEvent) error {
		return nil
	})
	if !errors.Is(err, firstErr) {
		t.Fatalf("ExecuteCodex error = %v, want first executor error", err)
	}
	if got := second.calls.Load(); got != 0 {
		t.Fatalf("second executor calls = %d, want no replay after headers", got)
	}
}

func TestExecuteCodexDoesNotReplayAfterAuditResponseStarted(t *testing.T) {
	firstErr := errors.New("executor disconnected after provider response")
	first := &codexDispatchFixture{
		err: firstErr,
		emit: func(emit func(protocol.CodexExecuteEvent) error) error {
			return emit(protocol.CodexExecuteEvent{
				Version: protocol.CodexExecutorVersion,
				Type:    protocol.CodexEventAuditResult,
				Audit:   &protocol.CodexAuditEvent{ResponseStarted: true},
			})
		},
	}
	second := &codexDispatchFixture{}
	m := &Manager{
		instances: map[string]*instance{
			"first":  newCodexDispatchInstance("first", 1, first),
			"second": newCodexDispatchInstance("second", 2, second),
		},
		lastErrors: make(map[string]string),
	}

	err := m.ExecuteCodex(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, nil)
	if !errors.Is(err, firstErr) {
		t.Fatalf("ExecuteCodex error = %v, want first executor error", err)
	}
	if got := second.calls.Load(); got != 0 {
		t.Fatalf("second executor calls = %d, want no replay after audit response_started", got)
	}
}

func TestExecuteCodexStillFallsBackBeforeResponseStarts(t *testing.T) {
	first := &codexDispatchFixture{err: errors.New("executor unavailable before headers")}
	second := &codexDispatchFixture{}
	m := &Manager{
		instances: map[string]*instance{
			"first":  newCodexDispatchInstance("first", 1, first),
			"second": newCodexDispatchInstance("second", 2, second),
		},
		lastErrors: make(map[string]string),
	}

	if err := m.ExecuteCodex(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, nil); err != nil {
		t.Fatalf("ExecuteCodex error = %v, want fallback success", err)
	}
	if got := first.calls.Load(); got != 1 {
		t.Fatalf("first executor calls = %d, want 1", got)
	}
	if got := second.calls.Load(); got != 1 {
		t.Fatalf("second executor calls = %d, want 1", got)
	}
}

func TestExecuteCodexDoesNotCircuitBreakOnConsumerEmitFailure(t *testing.T) {
	consumerErr := errors.New("downstream writer failed")
	plugin := &codexDispatchFixture{
		emit: func(emit func(protocol.CodexExecuteEvent) error) error {
			return emit(protocol.CodexExecuteEvent{Version: protocol.CodexExecutorVersion, Type: protocol.CodexEventData, Data: []byte("data")})
		},
	}
	inst := newCodexDispatchInstance("consumer-error", 1, plugin)
	m := &Manager{instances: map[string]*instance{"consumer-error": inst}, lastErrors: make(map[string]string)}
	for i := 0; i < circuitFailureLimit+1; i++ {
		err := m.ExecuteCodex(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, func(protocol.CodexExecuteEvent) error {
			return consumerErr
		})
		if !errors.Is(err, consumerErr) {
			t.Fatalf("attempt %d error = %v, want consumer error", i+1, err)
		}
	}
	if got := plugin.calls.Load(); got != int32(circuitFailureLimit+1) {
		t.Fatalf("consumer failures circuit-broke plugin: calls=%d, want %d", got, circuitFailureLimit+1)
	}
	if !inst.circuitUntil.IsZero() || inst.consecutiveFailures != 0 {
		t.Fatalf("consumer failure changed plugin health: circuit_until=%v consecutive=%d", inst.circuitUntil, inst.consecutiveFailures)
	}
}

func TestExecuteCodexCancelsExecutorWhenConsumerEmitFails(t *testing.T) {
	consumerErr := errors.New("downstream writer failed")
	fixture := &blockingCodexStreamDispatchFixture{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	info := protocol.PluginInfo{
		ID:              "blocking-consumer-error-stream",
		Name:            "blocking-consumer-error-stream",
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        1,
		Capabilities:    []string{protocol.CapabilityCodexExecutorV1},
	}
	inst := &instance{id: info.ID, name: info.Name, info: info, plugin: fixture, started: true}
	m := &Manager{instances: map[string]*instance{info.ID: inst}, lastErrors: make(map[string]string)}
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.ExecuteCodex(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, func(protocol.CodexExecuteEvent) error {
			return consumerErr
		})
	}()
	select {
	case <-fixture.started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream executor did not start")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, consumerErr) {
			t.Fatalf("ExecuteCodex error = %v, want consumer error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer emit failure did not cancel blocking executor")
	}
	select {
	case <-fixture.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("executor context was not canceled after consumer emit failure")
	}
}

func TestExecuteCodexWebSocketDoesNotCircuitBreakOnConsumerEmitFailure(t *testing.T) {
	consumerErr := errors.New("downstream websocket closed")
	plugin := &codexWebSocketDispatchFixture{}
	info := protocol.PluginInfo{
		ID:              "consumer-error-ws",
		Name:            "consumer-error-ws",
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        1,
		Capabilities:    []string{protocol.CapabilityCodexExecutorV1},
	}
	inst := &instance{id: info.ID, name: info.Name, info: info, plugin: plugin, started: true}
	m := &Manager{instances: map[string]*instance{info.ID: inst}, lastErrors: make(map[string]string)}
	for i := 0; i < circuitFailureLimit+1; i++ {
		err := m.ExecuteCodexWebSocket(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, nil, func(protocol.CodexWebSocketFrame) error {
			return consumerErr
		})
		if !errors.Is(err, consumerErr) {
			t.Fatalf("attempt %d error = %v, want consumer error", i+1, err)
		}
	}
	if got := plugin.calls.Load(); got != int32(circuitFailureLimit+1) {
		t.Fatalf("consumer failures circuit-broke websocket plugin: calls=%d, want %d", got, circuitFailureLimit+1)
	}
	if !inst.circuitUntil.IsZero() || inst.consecutiveFailures != 0 {
		t.Fatalf("consumer websocket failure changed plugin health: circuit_until=%v consecutive=%d", inst.circuitUntil, inst.consecutiveFailures)
	}
}

func TestExecuteCodexWebSocketCancelsExecutorWhenConsumerEmitFails(t *testing.T) {
	consumerErr := errors.New("downstream websocket closed")
	fixture := &blockingCodexWebSocketDispatchFixture{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	info := protocol.PluginInfo{
		ID:              "blocking-consumer-error-ws",
		Name:            "blocking-consumer-error-ws",
		ProtocolVersion: protocol.ProtocolVersion,
		Priority:        1,
		Capabilities:    []string{protocol.CapabilityCodexExecutorV1},
	}
	inst := &instance{id: info.ID, name: info.Name, info: info, plugin: fixture, started: true}
	m := &Manager{instances: map[string]*instance{info.ID: inst}, lastErrors: make(map[string]string)}
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.ExecuteCodexWebSocket(context.Background(), protocol.CodexExecuteRequest{Version: protocol.CodexExecutorVersion}, nil, func(protocol.CodexWebSocketFrame) error {
			return consumerErr
		})
	}()
	select {
	case <-fixture.started:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket executor did not start")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, consumerErr) {
			t.Fatalf("ExecuteCodexWebSocket error = %v, want consumer error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer emit failure did not cancel blocking executor")
	}
	select {
	case <-fixture.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("executor context was not canceled after consumer emit failure")
	}
}
