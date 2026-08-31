package protocol

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

const serviceName = "airgate.plugin.v2.Plugin"

type wireCodec struct{}

func (wireCodec) Name() string { return "airgate-v2" }
func (wireCodec) Marshal(value any) ([]byte, error) {
	switch typed := value.(type) {
	case Request:
		return marshalRequest(typed)
	case *Request:
		if typed == nil {
			return nil, fmt.Errorf("插件协议请求不能为空")
		}
		return marshalRequest(*typed)
	case Response:
		return marshalResponse(typed)
	case *Response:
		if typed == nil {
			return nil, fmt.Errorf("插件协议响应不能为空")
		}
		return marshalResponse(*typed)
	case CodexExecuteRequest:
		return marshalCodexExecuteRequest(typed)
	case *CodexExecuteRequest:
		if typed == nil {
			return nil, fmt.Errorf("Codex executor request cannot be nil")
		}
		return marshalCodexExecuteRequest(*typed)
	case CodexExecuteEvent:
		return marshalCodexExecuteEvent(typed)
	case *CodexExecuteEvent:
		if typed == nil {
			return nil, fmt.Errorf("Codex executor event cannot be nil")
		}
		return marshalCodexExecuteEvent(*typed)
	case CodexWebSocketFrame:
		return marshalCodexWebSocketFrame(typed)
	case *CodexWebSocketFrame:
		if typed == nil {
			return nil, fmt.Errorf("Codex websocket frame cannot be nil")
		}
		return marshalCodexWebSocketFrame(*typed)
	default:
		return json.Marshal(value)
	}
}
func (wireCodec) Unmarshal(data []byte, value any) error {
	switch typed := value.(type) {
	case *Request:
		return unmarshalRequest(data, typed)
	case *Response:
		return unmarshalResponse(data, typed)
	case *CodexExecuteRequest:
		return unmarshalCodexExecuteRequest(data, typed)
	case *CodexExecuteEvent:
		return unmarshalCodexExecuteEvent(data, typed)
	case *CodexWebSocketFrame:
		return unmarshalCodexWebSocketFrame(data, typed)
	default:
		return json.Unmarshal(data, value)
	}
}

type codexExecuteRequestMetadata CodexExecuteRequest
type codexExecuteEventMetadata CodexExecuteEvent
type codexWebSocketFrameMetadata CodexWebSocketFrame

func marshalCodexExecuteRequest(request CodexExecuteRequest) ([]byte, error) {
	metadata := codexExecuteRequestMetadata(request)
	metadata.Body = nil
	return marshalEnvelope(metadata, request.Body)
}

func unmarshalCodexExecuteRequest(data []byte, request *CodexExecuteRequest) error {
	var metadata codexExecuteRequestMetadata
	body, err := unmarshalEnvelope(data, &metadata)
	if err != nil {
		return err
	}
	*request = CodexExecuteRequest(metadata)
	request.Body = body
	return nil
}

func marshalCodexExecuteEvent(event CodexExecuteEvent) ([]byte, error) {
	metadata := codexExecuteEventMetadata(event)
	metadata.Data = nil
	return marshalEnvelope(metadata, event.Data)
}

func unmarshalCodexExecuteEvent(data []byte, event *CodexExecuteEvent) error {
	var metadata codexExecuteEventMetadata
	body, err := unmarshalEnvelope(data, &metadata)
	if err != nil {
		return err
	}
	*event = CodexExecuteEvent(metadata)
	event.Data = body
	return nil
}

func marshalCodexWebSocketFrame(frame CodexWebSocketFrame) ([]byte, error) {
	metadata := codexWebSocketFrameMetadata(frame)
	metadata.Data = nil
	return marshalEnvelope(metadata, frame.Data)
}

func unmarshalCodexWebSocketFrame(data []byte, frame *CodexWebSocketFrame) error {
	var metadata codexWebSocketFrameMetadata
	body, err := unmarshalEnvelope(data, &metadata)
	if err != nil {
		return err
	}
	*frame = CodexWebSocketFrame(metadata)
	frame.Data = body
	return nil
}

func init() {
	encoding.RegisterCodec(wireCodec{})
}

type requestMetadata struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Header map[string][]string `json:"header,omitempty"`
}

type responseMetadata struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header,omitempty"`
}

func marshalRequest(request Request) ([]byte, error) {
	return marshalEnvelope(requestMetadata{
		Method: request.Method,
		Path:   request.Path,
		Query:  request.Query,
		Header: request.Header,
	}, request.Body)
}

func unmarshalRequest(data []byte, request *Request) error {
	var metadata requestMetadata
	body, err := unmarshalEnvelope(data, &metadata)
	if err != nil {
		return err
	}
	*request = Request{
		Method: metadata.Method,
		Path:   metadata.Path,
		Query:  metadata.Query,
		Header: metadata.Header,
		Body:   body,
	}
	return nil
}

func marshalResponse(response Response) ([]byte, error) {
	return marshalEnvelope(responseMetadata{
		StatusCode: response.StatusCode,
		Header:     response.Header,
	}, response.Body)
}

func unmarshalResponse(data []byte, response *Response) error {
	var metadata responseMetadata
	body, err := unmarshalEnvelope(data, &metadata)
	if err != nil {
		return err
	}
	*response = Response{
		StatusCode: metadata.StatusCode,
		Header:     metadata.Header,
		Body:       body,
	}
	return nil
}

// marshalEnvelope 只对小型元数据使用 JSON，正文按原始字节追加，避免 Base64
// 膨胀和大正文的重复 JSON 编解码。前四字节是大端元数据长度。
func marshalEnvelope(metadata any, body []byte) ([]byte, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if uint64(len(encoded)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("插件协议元数据过大")
	}
	result := make([]byte, 4+len(encoded)+len(body))
	binary.BigEndian.PutUint32(result[:4], uint32(len(encoded)))
	copy(result[4:], encoded)
	copy(result[4+len(encoded):], body)
	return result, nil
}

func unmarshalEnvelope(data []byte, metadata any) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("插件协议消息缺少元数据长度")
	}
	metadataBytes := int(binary.BigEndian.Uint32(data[:4]))
	if metadataBytes > len(data)-4 {
		return nil, fmt.Errorf("插件协议元数据长度无效")
	}
	if err := json.Unmarshal(data[4:4+metadataBytes], metadata); err != nil {
		return nil, fmt.Errorf("解析插件协议元数据失败: %w", err)
	}
	return append([]byte(nil), data[4+metadataBytes:]...), nil
}

type empty struct{}

type initRequest struct {
	Config map[string]string `json:"config"`
}

// Client 是 Plugin 的 gRPC 客户端实现。
type Client struct {
	ctx  context.Context
	conn *grpc.ClientConn
	info PluginInfo
}

const grpcWebSocketShutdownTimeout = time.Second

// A receive pump may finish on the same scheduler turn in which an executor
// returns (for example, after the peer sends a malformed frame and the
// plugin exits because its input channel closes).  Give that pump a short
// bounded drain window before treating a nil executor result as success; this
// avoids hiding a real client-stream receive error without adding an
// unbounded shutdown wait to the RPC.
const grpcWebSocketRecvDrainTimeout = 25 * time.Millisecond

var _ Plugin = (*Client)(nil)

func (c *Client) invoke(ctx context.Context, method string, input, output any) error {
	if c == nil || c.conn == nil {
		return errors.New("plugin client connection is unavailable")
	}
	if ctx == nil {
		ctx = c.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.conn.Invoke(
		ctx,
		"/"+serviceName+"/"+method,
		input,
		output,
		grpc.ForceCodec(wireCodec{}),
		grpc.MaxCallRecvMsgSize(MaxMessageBytes),
		grpc.MaxCallSendMsgSize(MaxMessageBytes),
	)
}

func (c *Client) Info() PluginInfo { return c.info }

func (c *Client) loadInfo(ctx context.Context) error {
	var info PluginInfo
	if err := c.invoke(ctx, "Info", &empty{}, &info); err != nil {
		return err
	}
	c.info = info
	return nil
}

func (c *Client) Init(ctx context.Context, config map[string]string) error {
	return c.invoke(ctx, "Init", &initRequest{Config: config}, &empty{})
}

func (c *Client) Start(ctx context.Context) error {
	return c.invoke(ctx, "Start", &empty{}, &empty{})
}

func (c *Client) Stop(ctx context.Context) error {
	return c.invoke(ctx, "Stop", &empty{}, &empty{})
}

func (c *Client) Handle(ctx context.Context, request Request) (Response, error) {
	var response Response
	err := c.invoke(ctx, "Handle", &request, &response)
	return response, err
}

// ExecuteStream opens the server-streaming native Codex executor RPC.
func (c *Client) ExecuteStream(ctx context.Context, request CodexExecuteRequest, emit func(CodexExecuteEvent) error) error {
	if c == nil || c.conn == nil {
		return errors.New("plugin client connection is unavailable")
	}
	if ctx == nil {
		ctx = c.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Own a call-scoped cancellation context.  Returning early from the receive
	// loop (most commonly because Core's downstream writer failed) must cancel
	// the RPC and therefore the plugin's upstream HTTP request; otherwise a
	// long-lived SSE response can continue running after the caller has gone
	// away.  Do not rely on the parent request context being cancelled promptly
	// in that case.
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	desc := &grpc.StreamDesc{ServerStreams: true}
	stream, err := c.conn.NewStream(
		callCtx,
		desc,
		"/"+serviceName+"/ExecuteStream",
		grpc.ForceCodec(wireCodec{}),
		grpc.MaxCallRecvMsgSize(MaxMessageBytes),
		grpc.MaxCallSendMsgSize(MaxMessageBytes),
	)
	if err != nil {
		return err
	}
	if err := stream.SendMsg(&request); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	for {
		var event CodexExecuteEvent
		if err := stream.RecvMsg(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if emit != nil {
			if err := emit(event); err != nil {
				return err
			}
		}
	}
}

// ExecuteWebSocket opens the bidirectional native Codex executor RPC. The
// first message is the immutable account/request lease; subsequent messages
// are WebSocket frames supplied by the Core gateway. The receive loop runs in
// parallel with the send loop so named stream lanes can remain fully duplex.
func (c *Client) ExecuteWebSocket(ctx context.Context, request CodexExecuteRequest, frames <-chan CodexWebSocketFrame, emit func(CodexWebSocketFrame) error) error {
	if c == nil || c.conn == nil {
		return errors.New("plugin client connection is unavailable")
	}
	if ctx == nil {
		ctx = c.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.conn.NewStream(
		callCtx,
		&grpc.StreamDesc{ServerStreams: true, ClientStreams: true},
		"/"+serviceName+"/ExecuteWebSocket",
		grpc.ForceCodec(wireCodec{}),
		grpc.MaxCallRecvMsgSize(MaxMessageBytes),
		grpc.MaxCallSendMsgSize(MaxMessageBytes),
	)
	if err != nil {
		return err
	}
	if err := stream.SendMsg(&request); err != nil {
		return err
	}
	// A nil input channel means that the caller has no client frames. Treat it
	// as an already-closed send side instead of starting a goroutine that can
	// wait forever on a nil channel while the peer expects a half-close.
	if frames == nil {
		closedFrames := make(chan CodexWebSocketFrame)
		close(closedFrames)
		frames = closedFrames
	}

	// Keep half-close failures separate from frame-send failures.  A peer may
	// send a structured capability/error frame while CloseSend is racing with
	// the receive pump; treating that transport error as terminal would hide
	// the useful structured response from the caller.
	type sendResult struct {
		err       error
		halfClose bool
	}
	sendErr := make(chan sendResult, 1)
	recvDone := make(chan struct{})
	go func() {
		defer close(sendErr)
		for {
			select {
			case <-callCtx.Done():
				return
			case frame, ok := <-frames:
				if !ok {
					sendErr <- sendResult{err: stream.CloseSend(), halfClose: true}
					return
				}
				if err := stream.SendMsg(&frame); err != nil {
					sendErr <- sendResult{err: err}
					return
				}
			}
		}
	}()

	type recvResult struct {
		frame CodexWebSocketFrame
		err   error
	}
	recvCh := make(chan recvResult, 1)
	go func() {
		defer close(recvDone)
		for {
			var frame CodexWebSocketFrame
			err := stream.RecvMsg(&frame)
			// The consumer can return early when the downstream WebSocket closes
			// or its emit callback fails. Do not leave this receive pump blocked on
			// a full local channel after the RPC context has been cancelled.
			select {
			case recvCh <- recvResult{frame: frame, err: err}:
			case <-callCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	// Cancellation normally unblocks both grpc stream operations. Keep a
	// bounded join on return as a lifecycle backstop: it prevents a caller that
	// failed its downstream write from retaining the send/receive pumps forever,
	// without making the request path hang behind a broken transport forever.
	defer func() {
		cancel()
		timer := time.NewTimer(grpcWebSocketShutdownTimeout)
		defer timer.Stop()
		select {
		case <-sendErr:
		case <-timer.C:
			return
		}
		select {
		case <-recvDone:
		case <-timer.C:
		}
	}()
	var sendDone <-chan sendResult = sendErr
	var pendingSendErr error
	// A local SendMsg failure is not guaranteed to terminate the peer's bidi
	// handler (for example, a message can exceed the client-side send limit
	// before it ever reaches the wire). Keep a short window for a structured
	// executor response that is already in flight, then fail closed instead of
	// waiting on RecvMsg forever.
	var pendingSendDrain <-chan time.Time
	consumeRecv := func(result recvResult) (bool, error) {
		if errors.Is(result.err, io.EOF) {
			// A CloseSend error is often a consequence of the peer ending the
			// bidi stream after its final structured frame. Once the receive side
			// reaches EOF, prefer that protocol result and only surface a real
			// frame-send failure.
			if pendingSendErr != nil {
				return true, pendingSendErr
			}
			return true, nil
		}
		if result.err != nil {
			return true, result.err
		}
		if emit != nil {
			if err := emit(result.frame); err != nil {
				return true, err
			}
		}
		return false, nil
	}
	for {
		select {
		case result := <-recvCh:
			if done, err := consumeRecv(result); done {
				return err
			}
		case result, ok := <-sendDone:
			if !ok {
				sendDone = nil
				continue
			}
			sendDone = nil
			if result.err != nil && !result.halfClose {
				// Do not preempt a structured frame already queued by the peer.
				// RecvMsg will normally terminate promptly after a SendMsg error;
				// retaining the error here also lets an already-delivered protocol
				// error win the race. The bounded drain below covers transports that
				// leave the peer handler open after a local serialization failure.
				pendingSendErr = result.err
				if pendingSendDrain == nil {
					pendingSendDrain = time.After(grpcWebSocketRecvDrainTimeout)
				}
			}
		case <-pendingSendDrain:
			// Select is fair when both the timer and recvCh are ready. Drain any
			// already-buffered receive results once more before returning the local
			// send error, so a structured executor error that won the race is not
			// discarded merely because the grace timer fired on the same turn.
			for {
				select {
				case result := <-recvCh:
					if done, err := consumeRecv(result); done {
						return err
					}
				default:
					cancel()
					return pendingSendErr
				}
			}
		case <-callCtx.Done():
			if pendingSendErr != nil {
				return pendingSendErr
			}
			return callCtx.Err()
		}
	}
}

// GRPCPlugin 把最小 Plugin 接口接入 hashicorp/go-plugin。
type GRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl Plugin
}

func (p *GRPCPlugin) GRPCClient(ctx context.Context, _ *goplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	client := &Client{ctx: ctx, conn: conn}
	if err := client.loadInfo(ctx); err != nil {
		return nil, fmt.Errorf("读取插件信息失败: %w", err)
	}
	return client, nil
}

func (p *GRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, server *grpc.Server) error {
	if p.Impl == nil {
		return fmt.Errorf("插件实现不能为空")
	}
	registerPluginServer(server, &grpcServer{impl: p.Impl})
	return nil
}

type pluginServer interface {
	Info(context.Context, *empty) (*PluginInfo, error)
	Init(context.Context, *initRequest) (*empty, error)
	Start(context.Context, *empty) (*empty, error)
	Stop(context.Context, *empty) (*empty, error)
	Handle(context.Context, *Request) (*Response, error)
	ExecuteStream(*CodexExecuteRequest, grpc.ServerStream) error
	ExecuteWebSocket(*CodexExecuteRequest, grpc.ServerStream) error
}

type grpcServer struct {
	impl Plugin
}

func (s *grpcServer) Info(context.Context, *empty) (*PluginInfo, error) {
	info := s.impl.Info()
	return &info, nil
}

func (s *grpcServer) Init(ctx context.Context, request *initRequest) (*empty, error) {
	if err := s.impl.Init(ctx, request.Config); err != nil {
		return nil, err
	}
	return &empty{}, nil
}

func (s *grpcServer) Start(ctx context.Context, _ *empty) (*empty, error) {
	if err := s.impl.Start(ctx); err != nil {
		return nil, err
	}
	return &empty{}, nil
}

func (s *grpcServer) Stop(ctx context.Context, _ *empty) (*empty, error) {
	if err := s.impl.Stop(ctx); err != nil {
		return nil, err
	}
	return &empty{}, nil
}

func (s *grpcServer) Handle(ctx context.Context, request *Request) (*Response, error) {
	response, err := s.impl.Handle(ctx, *request)
	return &response, err
}

func (s *grpcServer) ExecuteStream(request *CodexExecuteRequest, stream grpc.ServerStream) error {
	executor, ok := s.impl.(CodexExecutor)
	if !ok {
		return stream.SendMsg(&CodexExecuteEvent{
			Version: CodexExecutorVersion,
			Type:    CodexEventError,
			Error: &CodexExecutorError{
				Code:    "unsupported_capability",
				Message: "plugin does not implement codex_executor.v1",
				Phase:   "before_headers",
			},
		})
	}
	return executor.ExecuteStream(stream.Context(), *request, func(event CodexExecuteEvent) error {
		return stream.SendMsg(&event)
	})
}

func (s *grpcServer) ExecuteWebSocket(request *CodexExecuteRequest, stream grpc.ServerStream) error {
	if request == nil {
		return errors.New("Codex websocket request cannot be nil")
	}
	executor, ok := s.impl.(CodexWebSocketExecutor)
	if !ok {
		// Keep capability failures on the structured executor wire. Returning a
		// raw gRPC error makes Core unable to distinguish an unavailable plugin
		// from a provider failure and prevents its normal CPA fallback policy.
		return stream.SendMsg(&CodexWebSocketFrame{
			Version: CodexExecutorVersion,
			Type:    CodexWebSocketFrameError,
			Error: &CodexExecutorError{
				Code:    "unsupported_capability",
				Message: "plugin does not implement codex websocket executor",
				Phase:   "before_headers",
			},
		})
	}
	// Keep a small bounded burst buffer between the gRPC receive pump and the
	// plugin. The plugin may emit handshake/control frames before it starts
	// reading client input; an unbuffered channel would stall the pump and make
	// cancellation/half-close handling depend on scheduling.
	frames := make(chan CodexWebSocketFrame, 32)
	recvErr := make(chan error, 1)
	pumpStop := make(chan struct{})
	go func() {
		defer close(frames)
		for {
			var frame CodexWebSocketFrame
			err := stream.RecvMsg(&frame)
			if errors.Is(err, io.EOF) {
				select {
				case recvErr <- nil:
				default:
				}
				return
			}
			if err != nil {
				select {
				case recvErr <- err:
				default:
				}
				return
			}
			select {
			case frames <- frame:
			case <-pumpStop:
				return
			case <-stream.Context().Done():
				select {
				case recvErr <- stream.Context().Err():
				default:
				}
				return
			}
		}
	}()
	err := executor.ExecuteWebSocket(stream.Context(), *request, frames, func(frame CodexWebSocketFrame) error {
		return stream.SendMsg(&frame)
	})
	// Stop accepting new input as soon as the plugin has finished. The stream
	// receive itself is owned by gRPC and is interrupted when this handler
	// returns. Do not wait for that receive here: the server stream context is
	// canceled only after the handler returns, so joining it here would add a
	// shutdown timeout to every server-initiated close.
	close(pumpStop)
	if err != nil {
		return err
	}
	select {
	case recvErr := <-recvErr:
		return recvErr
	case <-time.After(grpcWebSocketRecvDrainTimeout):
		return nil
	}
}

func registerPluginServer(server *grpc.Server, impl pluginServer) {
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*pluginServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Info", Handler: unaryHandler(func(ctx context.Context, request *empty) (any, error) { return impl.Info(ctx, request) })},
			{MethodName: "Init", Handler: unaryHandler(func(ctx context.Context, request *initRequest) (any, error) { return impl.Init(ctx, request) })},
			{MethodName: "Start", Handler: unaryHandler(func(ctx context.Context, request *empty) (any, error) { return impl.Start(ctx, request) })},
			{MethodName: "Stop", Handler: unaryHandler(func(ctx context.Context, request *empty) (any, error) { return impl.Stop(ctx, request) })},
			{MethodName: "Handle", Handler: unaryHandler(func(ctx context.Context, request *Request) (any, error) { return impl.Handle(ctx, request) })},
		},
		Streams: []grpc.StreamDesc{{
			StreamName:    "ExecuteStream",
			ServerStreams: true,
			Handler: func(service any, stream grpc.ServerStream) error {
				request := new(CodexExecuteRequest)
				if err := stream.RecvMsg(request); err != nil {
					return err
				}
				return impl.ExecuteStream(request, stream)
			},
		}, {
			StreamName:    "ExecuteWebSocket",
			ServerStreams: true,
			ClientStreams: true,
			Handler: func(service any, stream grpc.ServerStream) error {
				request := new(CodexExecuteRequest)
				if err := stream.RecvMsg(request); err != nil {
					return err
				}
				return impl.ExecuteWebSocket(request, stream)
			},
		}},
	}, impl)
}

func unaryHandler[T any](call func(context.Context, *T) (any, error)) grpc.MethodHandler {
	return func(service any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		request := new(T)
		if err := decode(request); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return call(ctx, request)
		}
		info := &grpc.UnaryServerInfo{Server: service}
		handler := func(nextCtx context.Context, nextRequest any) (any, error) {
			return call(nextCtx, nextRequest.(*T))
		}
		return interceptor(ctx, request, info, handler)
	}
}

// Serve 启动当前插件的独立 gRPC 进程服务。
func Serve(impl Plugin) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins: goplugin.PluginSet{
			PluginKey: &GRPCPlugin{Impl: impl},
		},
		GRPCServer: func(options []grpc.ServerOption) *grpc.Server {
			options = append(options, grpc.MaxRecvMsgSize(MaxMessageBytes), grpc.MaxSendMsgSize(MaxMessageBytes))
			return grpc.NewServer(options...)
		},
	})
}
