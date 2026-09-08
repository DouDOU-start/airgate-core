package protocol

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// cancellationCodexPlugin deliberately keeps the executor alive after it has
// emitted data. The client-side emit failure must cancel the RPC context so
// this wait is released; otherwise a real MCP/Responses SSE request would
// remain attached to the plugin after the downstream connection failed.
type cancellationCodexPlugin struct {
	started    chan struct{}
	canceled   chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
}

// halfCloseCodexPlugin verifies that the client-side nil/closed frame stream
// performs a gRPC half-close and that the server does not keep the duplex RPC
// alive after the executor has finished.
type halfCloseCodexPlugin struct {
	halfClosed chan struct{}
	once       sync.Once
}

type immediateWebSocketPlugin struct{}

type blockedWebSocketPlugin struct {
	started  chan struct{}
	canceled chan struct{}
}

func (*immediateWebSocketPlugin) Info() PluginInfo                              { return PluginInfo{ID: "codex-immediate-fixture"} }
func (*immediateWebSocketPlugin) Init(context.Context, map[string]string) error { return nil }
func (*immediateWebSocketPlugin) Start(context.Context) error                   { return nil }
func (*immediateWebSocketPlugin) Stop(context.Context) error                    { return nil }
func (*immediateWebSocketPlugin) Handle(context.Context, Request) (Response, error) {
	return Response{}, nil
}

func (*immediateWebSocketPlugin) ExecuteWebSocket(context.Context, CodexExecuteRequest, <-chan CodexWebSocketFrame, func(CodexWebSocketFrame) error) error {
	return nil
}

func (p *blockedWebSocketPlugin) Info() PluginInfo {
	return PluginInfo{ID: "codex-blocked-websocket-fixture"}
}
func (*blockedWebSocketPlugin) Init(context.Context, map[string]string) error { return nil }
func (*blockedWebSocketPlugin) Start(context.Context) error                   { return nil }
func (*blockedWebSocketPlugin) Stop(context.Context) error                    { return nil }
func (*blockedWebSocketPlugin) Handle(context.Context, Request) (Response, error) {
	return Response{}, nil
}
func (p *blockedWebSocketPlugin) ExecuteWebSocket(ctx context.Context, _ CodexExecuteRequest, _ <-chan CodexWebSocketFrame, _ func(CodexWebSocketFrame) error) error {
	close(p.started)
	<-ctx.Done()
	close(p.canceled)
	return ctx.Err()
}

// immediateRecvErrorStream makes the server-side receive pump report an
// input error immediately. The executor returns nil without consuming the
// channel, so the handler must briefly drain recvErr instead of using a
// racy non-blocking read that can incorrectly report success.
type immediateRecvErrorStream struct {
	err error
}

func (*immediateRecvErrorStream) SetHeader(metadata.MD) error  { return nil }
func (*immediateRecvErrorStream) SendHeader(metadata.MD) error { return nil }
func (*immediateRecvErrorStream) SetTrailer(metadata.MD)       {}
func (*immediateRecvErrorStream) Context() context.Context     { return context.Background() }
func (*immediateRecvErrorStream) SendMsg(any) error            { return nil }
func (s *immediateRecvErrorStream) RecvMsg(any) error          { return s.err }

func (p *halfCloseCodexPlugin) Info() PluginInfo                            { return PluginInfo{ID: "codex-half-close-fixture"} }
func (*halfCloseCodexPlugin) Init(context.Context, map[string]string) error { return nil }
func (*halfCloseCodexPlugin) Start(context.Context) error                   { return nil }
func (*halfCloseCodexPlugin) Stop(context.Context) error                    { return nil }
func (*halfCloseCodexPlugin) Handle(context.Context, Request) (Response, error) {
	return Response{}, nil
}

func (p *halfCloseCodexPlugin) ExecuteWebSocket(ctx context.Context, _ CodexExecuteRequest, frames <-chan CodexWebSocketFrame, emit func(CodexWebSocketFrame) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-frames:
			if ok {
				continue
			}
			p.once.Do(func() { close(p.halfClosed) })
			return emit(CodexWebSocketFrame{
				Version: CodexExecutorVersion,
				Type:    CodexWebSocketFrameText,
				Data:    []byte("server-finished"),
			})
		}
	}
}

func (p *cancellationCodexPlugin) Info() PluginInfo                            { return PluginInfo{ID: "codex-cancel-fixture"} }
func (*cancellationCodexPlugin) Init(context.Context, map[string]string) error { return nil }
func (*cancellationCodexPlugin) Start(context.Context) error                   { return nil }
func (*cancellationCodexPlugin) Stop(context.Context) error                    { return nil }
func (*cancellationCodexPlugin) Handle(context.Context, Request) (Response, error) {
	return Response{}, nil
}

func (p *cancellationCodexPlugin) ExecuteStream(ctx context.Context, _ CodexExecuteRequest, emit func(CodexExecuteEvent) error) error {
	if err := emit(CodexExecuteEvent{Version: CodexExecutorVersion, Type: CodexEventResponseHeaders, StatusCode: 200}); err != nil {
		return err
	}
	if err := emit(CodexExecuteEvent{Version: CodexExecutorVersion, Type: CodexEventData, Data: []byte("event: first\\n\\n")}); err != nil {
		return err
	}
	p.startOnce.Do(func() { close(p.started) })
	<-ctx.Done()
	p.cancelOnce.Do(func() { close(p.canceled) })
	return ctx.Err()
}

func TestExecuteStreamCancelsRPCWhenEmitFails(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	fixture := &cancellationCodexPlugin{started: make(chan struct{}), canceled: make(chan struct{})}
	server := grpc.NewServer(grpc.ForceServerCodec(wireCodec{}))
	registerPluginServer(server, &grpcServer{impl: fixture})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(wireCodec{})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := &Client{ctx: context.Background(), conn: conn}

	emitErr := errors.New("downstream writer failed")
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.ExecuteStream(context.Background(), CodexExecuteRequest{
			Version: CodexExecutorVersion, Method: "POST", BaseURL: "https://example.test", Path: "/ps/mcp",
		}, func(event CodexExecuteEvent) error {
			if event.Type == CodexEventData {
				return emitErr
			}
			return nil
		})
	}()

	select {
	case <-fixture.started:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture did not reach its cancellation wait")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, emitErr) {
			t.Fatalf("ExecuteStream error = %v, want emit error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteStream did not return after emit failure")
	}
	select {
	case <-fixture.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("emit failure did not cancel plugin execution context")
	}
}

func TestExecuteWebSocketReportsUnsupportedCapabilityOnStructuredWire(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	fixture := &cancellationCodexPlugin{started: make(chan struct{}), canceled: make(chan struct{})}
	server := grpc.NewServer(grpc.ForceServerCodec(wireCodec{}))
	registerPluginServer(server, &grpcServer{impl: fixture})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(wireCodec{})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := &Client{ctx: context.Background(), conn: conn}
	frames := make(chan CodexWebSocketFrame)
	close(frames)

	var got []CodexWebSocketFrame
	if err := client.ExecuteWebSocket(context.Background(), CodexExecuteRequest{
		Version: CodexExecutorVersion, Method: "GET", BaseURL: "https://example.test", Path: "/responses",
	}, frames, func(frame CodexWebSocketFrame) error {
		got = append(got, frame)
		return nil
	}); err != nil {
		t.Fatalf("ExecuteWebSocket returned raw gRPC error: %v", err)
	}
	if len(got) != 1 || got[0].Type != CodexWebSocketFrameError || got[0].Error == nil || got[0].Error.Code != "unsupported_capability" {
		t.Fatalf("structured capability frame = %#v", got)
	}
}

func TestExecuteWebSocketNilFramesHalfClosesAndReturns(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	fixture := &halfCloseCodexPlugin{halfClosed: make(chan struct{})}
	server := grpc.NewServer(grpc.ForceServerCodec(wireCodec{}))
	registerPluginServer(server, &grpcServer{impl: fixture})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(wireCodec{})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := &Client{conn: conn}

	var got []CodexWebSocketFrame
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.ExecuteWebSocket(context.TODO(), CodexExecuteRequest{
			Version: CodexExecutorVersion, Method: "GET", Transport: CodexTransportWebSocket,
			BaseURL: "https://example.test", Path: "/responses",
		}, nil, func(frame CodexWebSocketFrame) error {
			got = append(got, frame)
			return nil
		})
	}()

	select {
	case <-fixture.halfClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("nil frame channel did not half-close the gRPC stream")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ExecuteWebSocket returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteWebSocket did not return after executor completion")
	}
	if len(got) != 1 || string(got[0].Data) != "server-finished" {
		t.Fatalf("received frames = %#v, want server-finished", got)
	}
}

func TestExecuteWebSocketReturnsAfterLocalSendFailureWhenPeerStaysOpen(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	fixture := &blockedWebSocketPlugin{started: make(chan struct{}), canceled: make(chan struct{})}
	server := grpc.NewServer(grpc.ForceServerCodec(wireCodec{}))
	registerPluginServer(server, &grpcServer{impl: fixture})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(wireCodec{})),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := &Client{ctx: context.Background(), conn: conn}
	frames := make(chan CodexWebSocketFrame, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- client.ExecuteWebSocket(context.Background(), CodexExecuteRequest{
			Version: CodexExecutorVersion, Method: "GET", Transport: CodexTransportWebSocket,
			BaseURL: "https://example.test", Path: "/responses",
		}, frames, nil)
	}()
	select {
	case <-fixture.started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked websocket fixture did not start")
	}
	// The envelope metadata makes this one byte over the client-side 64 MiB
	// gRPC send limit, so SendMsg fails locally before the server can observe the
	// frame. The peer deliberately remains open to exercise the bounded drain.
	frames <- CodexWebSocketFrame{Version: CodexExecutorVersion, Type: CodexWebSocketFrameBinary, Data: bytes.Repeat([]byte{'x'}, MaxMessageBytes)}
	close(frames)
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("ExecuteWebSocket returned nil after local SendMsg failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteWebSocket remained blocked after local SendMsg failure")
	}
	select {
	case <-fixture.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("local SendMsg failure did not cancel peer execution")
	}
}

func TestGRPCServerExecuteWebSocketDrainsReceiveErrorAfterExecutorReturns(t *testing.T) {
	want := errors.New("client frame decode failed")
	server := &grpcServer{impl: &immediateWebSocketPlugin{}}
	got := server.ExecuteWebSocket(&CodexExecuteRequest{
		Version: CodexExecutorVersion, Method: "GET", Transport: CodexTransportWebSocket,
	}, &immediateRecvErrorStream{err: want})
	if !errors.Is(got, want) {
		t.Fatalf("server ExecuteWebSocket error = %v, want %v", got, want)
	}
}
