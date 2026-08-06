package protocol

import (
	"context"
	"encoding/json"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

const serviceName = "airgate.plugin.v1.Plugin"

type jsonCodec struct{}

func (jsonCodec) Name() string { return "json" }
func (jsonCodec) Marshal(value any) ([]byte, error) {
	return json.Marshal(value)
}
func (jsonCodec) Unmarshal(data []byte, value any) error {
	return json.Unmarshal(data, value)
}

func init() {
	encoding.RegisterCodec(jsonCodec{})
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

var _ Plugin = (*Client)(nil)

func (c *Client) invoke(ctx context.Context, method string, input, output any) error {
	if ctx == nil {
		ctx = c.ctx
	}
	return c.conn.Invoke(
		ctx,
		"/"+serviceName+"/"+method,
		input,
		output,
		grpc.ForceCodec(jsonCodec{}),
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
