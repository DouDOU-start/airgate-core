// Package cursor 把 Cursor 官方 Agent 服务（agent.v1.AgentService，Connect-RPC
// over HTTP/2）桥接为 airgate 账号路径的一个 provider executor。
//
// 本文件实现最底层的传输：Connect-RPC 的帧编解码，以及承载双向流的
// HTTP/2 客户端。上层的协议翻译（入口 OpenAI/Anthropic JSON ↔ Cursor
// protobuf）与 exec 交互状态机在其它文件。
//
// 依赖约束：本包只依赖 CPA 公开 SDK 类型、protobuf 运行时与标准库/x-net，
// 禁止 import ent 与 internal/app（与其它 relay 子系统一致）。
package cursor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultBaseURL 是 Cursor Agent 服务地址。Run RPC 仅支持 HTTP/2
	// （ALB 对 HTTP/1.1 返回 464），因此传输强制 h2。
	DefaultBaseURL = "https://api2.cursor.sh"

	// DefaultClientVersion 是伪装的 Cursor CLI 客户端版本号。这是逆向协议的
	// 指纹之一，Cursor 升级或封禁旧版本时需要同步更新（来源 oh-my-pi）。
	DefaultClientVersion = "cli-2026.07.23-e383d2b"

	clientType = "cli"
	runPath    = "/agent.v1.AgentService/Run"

	// endStreamFlag 是 Connect 帧头 flags 的 end-of-stream 位；置位时该帧
	// 载荷是 JSON 的 end-stream 消息（可能含 error 与 metadata）。
	endStreamFlag byte = 0b0000_0010

	frameHeaderLen = 5
	// maxFrameLen 是单帧载荷上限，纯防御，避免异常长度导致无界分配。
	maxFrameLen = 64 << 20
)

// FrameMessage 按 Connect-RPC 分帧封装一个载荷：1 字节 flags + 4 字节大端长度 +
// 载荷。end 为 true 时置 end-stream 位。
func FrameMessage(payload []byte, end bool) []byte {
	frame := make([]byte, frameHeaderLen+len(payload))
	if end {
		frame[0] = endStreamFlag
	}
	binary.BigEndian.PutUint32(frame[1:frameHeaderLen], uint32(len(payload)))
	copy(frame[frameHeaderLen:], payload)
	return frame
}

// ReadFrame 从 r 读取一个 Connect 帧，返回该帧是否为 end-stream 帧及其载荷。
// 流正常结束时返回 io.EOF。
func ReadFrame(r *bufio.Reader) (end bool, payload []byte, err error) {
	var header [frameHeaderLen]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return false, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxFrameLen {
		return false, nil, fmt.Errorf("cursor connect 帧长度超限: %d", length)
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return false, nil, err
	}
	return header[0]&endStreamFlag != 0, payload, nil
}

// EndStreamError 解析 end-stream 帧载荷；若其中携带 Connect 错误则返回该错误，
// 否则返回 nil（正常结束）。
func EndStreamError(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	var env struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("cursor connect end-stream 解析失败: %w", err)
	}
	if env.Error == nil {
		return nil
	}
	code := env.Error.Code
	if code == "" {
		code = "unknown"
	}
	return &ConnectError{Code: code, Message: env.Error.Message}
}

// ConnectError 表示 Connect-RPC 语义错误（end-stream 帧或 HTTP 层）。
type ConnectError struct {
	// Code 是 Connect/gRPC 错误码字符串（如 resource_exhausted、unauthenticated）。
	Code string
	// Message 是上游给出的错误描述。
	Message string
	// HTTPStatus 是承载该错误的 HTTP 状态码（0 表示来自 end-stream 帧而非 HTTP 层）。
	HTTPStatus int
}

func (e *ConnectError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("cursor connect HTTP %d (%s): %s", e.HTTPStatus, e.Code, e.Message)
	}
	return fmt.Sprintf("cursor connect error %s: %s", e.Code, e.Message)
}

// StatusCode 把 Connect 错误码映射为 HTTP 状态码，供上层做认证刷新 / 限流分类。
func (e *ConnectError) StatusCode() int {
	if e.HTTPStatus != 0 {
		return e.HTTPStatus
	}
	switch e.Code {
	case "unauthenticated":
		return http.StatusUnauthorized
	case "permission_denied":
		return http.StatusForbidden
	case "resource_exhausted":
		return http.StatusTooManyRequests
	case "invalid_argument", "failed_precondition", "out_of_range":
		return http.StatusBadRequest
	case "not_found":
		return http.StatusNotFound
	case "unavailable":
		return http.StatusServiceUnavailable
	case "deadline_exceeded":
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

// Client 是可复用的 Cursor Agent 传输客户端，按出站代理缓存 HTTP/2 连接池。
type Client struct {
	baseURL       string
	clientVersion string

	mu      sync.Mutex
	clients map[string]*http.Client
}

// NewClient 创建客户端。baseURL / clientVersion 为空时用默认值。
func NewClient(baseURL, clientVersion string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = DefaultClientVersion
	}
	return &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		clientVersion: clientVersion,
		clients:       make(map[string]*http.Client),
	}
}

// sharedH2Transports 按代理缓存 HTTP/2 传输层，供本包客户端与外部审计
// RoundTripper（作为 base）共用，保证连接复用。
var (
	sharedH2Mu         sync.Mutex
	sharedH2Transports = map[string]*http.Transport{}
)

// SharedH2Transport 返回绑定指定出站代理的 HTTP/2 传输层（proxyURL 为空即
// 直连）。Cursor Agent 的 Run RPC 仅支持 HTTP/2（ALB 对 HTTP/1.1 返回 464），
// 通用账号传输层不能用，审计链路需以此为 base。
func SharedH2Transport(proxyURL string) (http.RoundTripper, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	sharedH2Mu.Lock()
	defer sharedH2Mu.Unlock()
	if t, ok := sharedH2Transports[proxyURL]; ok {
		return t, nil
	}
	transport := &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{NextProtos: []string{"h2"}},
		// 长连接空闲上限；Agent 流可能长时间保持。
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("解析代理 URL 失败: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	sharedH2Transports[proxyURL] = transport
	return transport, nil
}

// clientFor 返回绑定指定出站代理的 HTTP/2 客户端（proxyURL 为空即直连）。
func (c *Client) clientFor(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	if hc, ok := c.clients[proxyURL]; ok {
		return hc, nil
	}
	transport, err := SharedH2Transport(proxyURL)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Transport: transport}
	c.clients[proxyURL] = hc
	return hc, nil
}

// RunOptions 描述一次 Run 流的建立参数。
type RunOptions struct {
	// AccessToken 是账号的 Cursor access token（JWT），用于 Authorization。
	AccessToken string
	// ProxyURL 出站代理（可空）。
	ProxyURL string
	// RequestID 用于 x-request-id；为空时自动生成。
	RequestID string
	// Transport 可选的外部传输层（审计 RoundTripper 包装）。非空时替代
	// 按代理缓存的默认客户端；其内部 base 必须支持 HTTP/2 双向流。
	Transport http.RoundTripper
	// AuditBody 是提供给审计层（req.GetBody）的请求体替身。真实 body 是
	// 全双工管道，审计层同步读取会死锁，必须给出可重复读取的副本。
	AuditBody []byte
}

type respResult struct {
	resp *http.Response
	err  error
}

// Stream 是一条建立中的 Run 双向流：先 Send 初始 runRequest 帧，随后循环
// Recv 读取服务端帧并按需 Send exec 结果帧，直到收到 end-stream。
type Stream struct {
	pw     *io.PipeWriter
	respCh chan respResult

	resp   *http.Response
	reader *bufio.Reader
	setup  bool

	closeOnce sync.Once
}

// OpenRun 发起一次 Run 请求并返回可读写的双向流。此调用不写任何帧；调用方
// 必须先 Send 初始 runRequest 帧，Recv 才会拿到服务端响应。
func (c *Client) OpenRun(ctx context.Context, opts RunOptions) (*Stream, error) {
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, fmt.Errorf("缺少 access token")
	}
	var hc *http.Client
	if opts.Transport != nil {
		hc = &http.Client{Transport: opts.Transport}
	} else {
		var err error
		hc, err = c.clientFor(opts.ProxyURL)
		if err != nil {
			return nil, err
		}
	}
	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+runPath, pr)
	if err != nil {
		return nil, err
	}
	if opts.Transport != nil {
		// 审计层通过 GetBody 异步读取请求体副本；不设置会退回同步读取
		// 真身（io.Pipe），直接死锁。
		auditBody := opts.AuditBody
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(auditBody)), nil
		}
	}
	req.Header.Set("content-type", "application/connect+proto")
	req.Header.Set("connect-protocol-version", "1")
	req.Header.Set("authorization", "Bearer "+opts.AccessToken)
	req.Header.Set("x-ghost-mode", "true")
	req.Header.Set("x-cursor-client-version", c.clientVersion)
	req.Header.Set("x-cursor-client-type", clientType)
	req.Header.Set("x-request-id", requestID)

	s := &Stream{pw: pw, respCh: make(chan respResult, 1)}
	go func() {
		resp, doErr := hc.Do(req)
		s.respCh <- respResult{resp: resp, err: doErr}
	}()
	return s, nil
}

// Send 写入一帧数据。end 为 true 时置 end-stream 位（关闭发送方向）。
func (s *Stream) Send(payload []byte, end bool) error {
	_, err := s.pw.Write(FrameMessage(payload, end))
	return err
}

// CloseSend 结束发送方向。Cursor Run 在多数情况下并不要求客户端主动结束发送，
// 但提供该能力以支持无 exec 交互的纯请求。
func (s *Stream) CloseSend() error {
	return s.pw.Close()
}

// Recv 读取服务端的下一帧。首次调用会阻塞直到响应头到达；HTTP 层非 200 时
// 返回携带状态码的 ConnectError。流正常读尽返回 io.EOF。
func (s *Stream) Recv() (end bool, payload []byte, err error) {
	if !s.setup {
		r := <-s.respCh
		if r.err != nil {
			return false, nil, r.err
		}
		s.resp = r.resp
		if s.resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(s.resp.Body, 64<<10))
			_ = s.resp.Body.Close()
			return false, nil, &ConnectError{
				Code:       strings.ToLower(strings.ReplaceAll(s.resp.Status, " ", "_")),
				Message:    string(body),
				HTTPStatus: s.resp.StatusCode,
			}
		}
		s.reader = bufio.NewReaderSize(s.resp.Body, 64<<10)
		s.setup = true
	}
	return ReadFrame(s.reader)
}

// StatusCode 返回上游响应的 HTTP 状态码（Recv 首次成功后可用，否则 0）。
func (s *Stream) StatusCode() int {
	if s.resp == nil {
		return 0
	}
	return s.resp.StatusCode
}

// Close 释放流资源：关闭发送管道与响应体。可安全多次调用。
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		_ = s.pw.Close()
		if s.resp != nil && s.resp.Body != nil {
			_ = s.resp.Body.Close()
		}
	})
	return nil
}
