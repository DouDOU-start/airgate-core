// Package requestaudit 保存管理员可追溯的完整请求审计记录。
//
// 入站与上游 Header、Body 都先 gzip 压缩，再使用 API_KEY_SECRET 对应的
// AES-GCM 密钥加密。写入使用脱离客户端取消信号的短超时上下文，确保用户中断后
// 审计收尾仍可落库。
package requestaudit

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/DouDOU-start/airgate-core/ent"
	entattempt "github.com/DouDOU-start/airgate-core/ent/requestauditattempt"
	entlog "github.com/DouDOU-start/airgate-core/ent/requestauditlog"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

const (
	payloadPrefix = "audit:gzip:v1:"
	writeTimeout  = 10 * time.Second
)

// ErrWrite 标识在真实发包前审计写入失败。
var ErrWrite = errors.New("请求审计写入失败")

// Service 提供审计写入和管理员查询。
type Service struct {
	db      *ent.Client
	secret  string
	options Options

	asyncOnce         sync.Once
	asyncClose        sync.Once
	asyncSubmitMu     sync.RWMutex
	asyncQueue        chan asyncJob
	asyncStop         chan struct{}
	asyncDone         chan struct{}
	asyncClosed       atomic.Bool
	asyncPending      atomic.Int64
	asyncPendingBytes atomic.Int64
	asyncFallbacks    atomic.Uint64
	asyncCompleted    atomic.Uint64
	asyncFailed       atomic.Uint64
}

// New 创建请求审计服务。
func New(db *ent.Client, secret string, configured ...Options) *Service {
	options := Options{}
	if len(configured) > 0 {
		options = configured[0]
	}
	return &Service{db: db, secret: secret, options: normalizeOptions(options)}
}

// RequestInput 是一次已解析转发请求的入站快照。
type RequestInput struct {
	RequestID    string
	UserID       int
	UserEmail    string
	APIKeyID     int
	GroupID      int
	Client       string
	Protocol     string
	Endpoint     string
	Model        string
	Stream       bool
	Method       string
	Path         string
	RawQuery     string
	Host         string
	RequestProto string
	RemoteAddr   string
	IPAddress    string
	UserAgent    string
	ContentType  string
	ContentLen   int64
	Headers      http.Header
	Body         []byte
}

// Target 保存本次上游触网所使用的账号或渠道快照。
type Target struct {
	RouteKind       string
	ChannelID       int
	ChannelName     string
	ChannelKeyID    int
	ChannelKeyName  string
	AccountID       int
	AccountName     string
	AccountEmail    string
	AccountPlatform string
	AccountType     string
}

// AttemptFinish 是一次真实上游请求的最终结果。
type AttemptFinish struct {
	StatusCode      int
	Verdict         string
	Reason          string
	RetryAfter      time.Duration
	Latency         time.Duration
	FirstTokenMs    int64
	ResponseStarted bool
	StreamCompleted bool
}

// Handle 关联一次入站请求及其全部上游尝试。
type Handle struct {
	service *Service
	id      int
	start   time.Time
	seq     atomic.Int64
	finish  sync.Once
	fast    bool
}

// ID 返回审计主记录 ID。
func (h *Handle) ID() int {
	if h == nil {
		return 0
	}
	return h.id
}

// Start 同步保存入站快照。失败时调用方应停止向上游发包，避免出现已触网但无审计记录。
func (s *Service) Start(ctx context.Context, in RequestInput) (*Handle, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("请求审计服务未配置")
	}
	headers, err := json.Marshal(cloneHeader(in.Headers))
	if err != nil {
		return nil, fmt.Errorf("序列化入站请求头失败: %w", err)
	}
	headersEnc, err := s.seal(headers)
	if err != nil {
		return nil, fmt.Errorf("加密入站请求头失败: %w", err)
	}
	auditBody := redactBase64Images(in.Body)
	bodyEnc, err := s.seal(auditBody)
	if err != nil {
		return nil, fmt.Errorf("加密入站请求体失败: %w", err)
	}

	writeCtx, cancel := detachedTimeout(ctx)
	defer cancel()
	row, err := s.db.RequestAuditLog.Create().
		SetRequestID(in.RequestID).
		SetUserID(in.UserID).
		SetUserEmailSnapshot(in.UserEmail).
		SetAPIKeyID(in.APIKeyID).
		SetGroupID(in.GroupID).
		SetClient(in.Client).
		SetProtocol(in.Protocol).
		SetEndpoint(in.Endpoint).
		SetModel(in.Model).
		SetStream(in.Stream).
		SetMethod(in.Method).
		SetPath(in.Path).
		SetRawQuery(in.RawQuery).
		SetHost(in.Host).
		SetRequestProto(in.RequestProto).
		SetRemoteAddr(in.RemoteAddr).
		SetIPAddress(in.IPAddress).
		SetUserAgent(in.UserAgent).
		SetContentType(in.ContentType).
		SetContentLength(in.ContentLen).
		SetInboundHeadersEnc(headersEnc).
		SetInboundBodyEnc(bodyEnc).
		SetInboundBodyBytes(int64(len(in.Body))).
		Save(writeCtx)
	if err != nil {
		return nil, fmt.Errorf("保存请求审计主记录失败: %w", err)
	}
	return &Handle{service: s, id: row.ID, start: time.Now()}, nil
}

// StartFast 同步创建审计主记录骨架，再异步补写压缩加密后的 Header 与 Body。
// 数据库无法创建骨架时仍阻止向上游发包，保持请求触网前必有审计记录的约束。
func (s *Service) StartFast(ctx context.Context, in RequestInput) (*Handle, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("请求审计服务未配置")
	}
	if !s.options.AsyncEnabled {
		return s.Start(ctx, in)
	}
	writeCtx, cancel := detachedTimeout(ctx)
	defer cancel()
	row, err := s.db.RequestAuditLog.Create().
		SetRequestID(in.RequestID).
		SetUserID(in.UserID).
		SetUserEmailSnapshot(in.UserEmail).
		SetAPIKeyID(in.APIKeyID).
		SetGroupID(in.GroupID).
		SetClient(in.Client).
		SetProtocol(in.Protocol).
		SetEndpoint(in.Endpoint).
		SetModel(in.Model).
		SetStream(in.Stream).
		SetMethod(in.Method).
		SetPath(in.Path).
		SetRawQuery(in.RawQuery).
		SetHost(in.Host).
		SetRequestProto(in.RequestProto).
		SetRemoteAddr(in.RemoteAddr).
		SetIPAddress(in.IPAddress).
		SetUserAgent(in.UserAgent).
		SetContentType(in.ContentType).
		SetContentLength(in.ContentLen).
		SetInboundBodyBytes(int64(len(in.Body))).
		Save(writeCtx)
	if err != nil {
		return nil, fmt.Errorf("保存请求审计主记录失败: %w", err)
	}

	snapshot := cloneRequestInput(in)
	s.submitAsync(asyncJob{
		name:          "request_payload",
		retainedBytes: int64(len(snapshot.Body)),
		run: func(jobCtx context.Context) error {
			return s.enrichRequest(jobCtx, row.ID, snapshot)
		},
	})
	return &Handle{service: s, id: row.ID, start: time.Now(), fast: true}, nil
}

// Finish 保存请求最终状态。该操作幂等，且不受客户端取消影响。
func (h *Handle) Finish(statusCode int, responseBytes int64, completed bool) {
	if h == nil || h.service == nil || h.id <= 0 {
		return
	}
	h.finish.Do(func() {
		if h.fast && h.service.options.AsyncEnabled {
			duration := time.Since(h.start).Milliseconds()
			h.service.submitAsync(asyncJob{
				name: "request_finish",
				run: func(ctx context.Context) error {
					_, err := h.service.db.RequestAuditLog.UpdateOneID(h.id).
						SetStatusCode(statusCode).
						SetDurationMs(duration).
						SetResponseBytes(responseBytes).
						SetCompleted(completed).
						Save(ctx)
					return err
				},
			})
			return
		}
		ctx, cancel := detachedTimeout(context.Background())
		defer cancel()
		_, _ = h.service.db.RequestAuditLog.UpdateOneID(h.id).
			SetStatusCode(statusCode).
			SetDurationMs(time.Since(h.start).Milliseconds()).
			SetResponseBytes(responseBytes).
			SetCompleted(completed).
			Save(ctx)
	})
}

// BeginAttempt 在真实发包前同步保存实际上游 URL、Header、Body。
func (h *Handle) BeginAttempt(ctx context.Context, target Target, req *http.Request) (*AttemptHandle, error) {
	if h == nil || h.service == nil || h.id <= 0 {
		return nil, fmt.Errorf("请求审计句柄无效")
	}
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("上游请求为空")
	}
	body, err := readAndRestoreBody(req)
	if err != nil {
		return nil, fmt.Errorf("读取上游请求体失败: %w", err)
	}
	headers, err := json.Marshal(cloneHeader(req.Header))
	if err != nil {
		return nil, fmt.Errorf("序列化上游请求头失败: %w", err)
	}
	urlEnc, err := h.service.seal([]byte(req.URL.String()))
	if err != nil {
		return nil, fmt.Errorf("加密上游 URL 失败: %w", err)
	}
	headersEnc, err := h.service.seal(headers)
	if err != nil {
		return nil, fmt.Errorf("加密上游请求头失败: %w", err)
	}
	auditBody := redactBase64Images(body)
	bodyEnc, err := h.service.seal(auditBody)
	if err != nil {
		return nil, fmt.Errorf("加密上游请求体失败: %w", err)
	}

	seq := int(h.seq.Add(1))
	writeCtx, cancel := detachedTimeout(ctx)
	defer cancel()
	row, err := h.service.db.RequestAuditAttempt.Create().
		SetRequestAuditID(h.id).
		SetSeq(seq).
		SetRouteKind(entattempt.RouteKind(target.RouteKind)).
		SetChannelID(target.ChannelID).
		SetChannelName(target.ChannelName).
		SetChannelKeyID(target.ChannelKeyID).
		SetChannelKeyName(target.ChannelKeyName).
		SetAccountID(target.AccountID).
		SetAccountName(target.AccountName).
		SetAccountEmail(target.AccountEmail).
		SetAccountPlatform(target.AccountPlatform).
		SetAccountType(target.AccountType).
		SetMethod(req.Method).
		SetUpstreamURLEnc(urlEnc).
		SetForwardHeadersEnc(headersEnc).
		SetForwardBodyEnc(bodyEnc).
		SetForwardBodyBytes(int64(len(body))).
		Save(writeCtx)
	if err != nil {
		return nil, fmt.Errorf("保存上游请求审计失败: %w", err)
	}
	return &AttemptHandle{service: h.service, id: row.ID, start: time.Now()}, nil
}

// BeginAttemptFast 同步保存上游尝试骨架，再异步补写 URL、Header 与 Body 密文。
func (h *Handle) BeginAttemptFast(ctx context.Context, target Target, req *http.Request) (*AttemptHandle, error) {
	if h == nil || h.service == nil || h.id <= 0 {
		return nil, fmt.Errorf("请求审计句柄无效")
	}
	if !h.service.options.AsyncEnabled {
		return h.BeginAttempt(ctx, target, req)
	}
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("上游请求为空")
	}
	body, err := readAndRestoreBody(req)
	if err != nil {
		return nil, fmt.Errorf("读取上游请求体失败: %w", err)
	}
	seq := int(h.seq.Add(1))
	writeCtx, cancel := detachedTimeout(ctx)
	defer cancel()
	row, err := h.service.db.RequestAuditAttempt.Create().
		SetRequestAuditID(h.id).
		SetSeq(seq).
		SetRouteKind(entattempt.RouteKind(target.RouteKind)).
		SetChannelID(target.ChannelID).
		SetChannelName(target.ChannelName).
		SetChannelKeyID(target.ChannelKeyID).
		SetChannelKeyName(target.ChannelKeyName).
		SetAccountID(target.AccountID).
		SetAccountName(target.AccountName).
		SetAccountEmail(target.AccountEmail).
		SetAccountPlatform(target.AccountPlatform).
		SetAccountType(target.AccountType).
		SetMethod(req.Method).
		SetForwardBodyBytes(int64(len(body))).
		Save(writeCtx)
	if err != nil {
		return nil, fmt.Errorf("保存上游请求审计失败: %w", err)
	}

	upstreamURL := req.URL.String()
	headers := cloneHeader(req.Header)
	h.service.submitAsync(asyncJob{
		name:          "attempt_payload",
		retainedBytes: int64(len(body)),
		run: func(jobCtx context.Context) error {
			return h.service.enrichAttempt(jobCtx, row.ID, upstreamURL, headers, body)
		},
	})
	return &AttemptHandle{service: h.service, id: row.ID, start: time.Now(), fast: true}, nil
}

// AttemptHandle 关联一行实际上游尝试。
type AttemptHandle struct {
	service        *Service
	id             int
	start          time.Time
	fast           bool
	mu             sync.Mutex
	finished       bool
	latestFinish   AttemptFinish
	finishRevision uint64
	finishQueued   bool
}

// Finish 保存一次上游尝试结果。允许后续调用覆盖更准确的 verdict/耗时。
func (h *AttemptHandle) Finish(in AttemptFinish) {
	if h == nil || h.service == nil || h.id <= 0 {
		return
	}
	if in.Latency <= 0 {
		in.Latency = time.Since(h.start)
	}
	if h.fast && h.service.options.AsyncEnabled {
		h.mu.Lock()
		h.latestFinish = in
		h.finishRevision++
		if h.finishQueued {
			h.mu.Unlock()
			return
		}
		h.finishQueued = true
		h.mu.Unlock()
		h.service.submitAsync(asyncJob{
			name: "attempt_finish",
			run:  h.flushLatestFinish,
			after: func(err error) {
				if err == nil {
					return
				}
				h.mu.Lock()
				h.finishQueued = false
				h.mu.Unlock()
			},
		})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	ctx, cancel := detachedTimeout(context.Background())
	defer cancel()
	_, err := h.service.db.RequestAuditAttempt.UpdateOneID(h.id).
		SetStatusCode(in.StatusCode).
		SetVerdict(in.Verdict).
		SetReason(in.Reason).
		SetRetryAfterMs(in.RetryAfter.Milliseconds()).
		SetLatencyMs(in.Latency.Milliseconds()).
		SetFirstTokenMs(in.FirstTokenMs).
		SetResponseStarted(in.ResponseStarted).
		SetStreamCompleted(in.StreamCompleted).
		SetFinished(true).
		Save(ctx)
	if err == nil {
		h.finished = true
	}
}

func (h *AttemptHandle) flushLatestFinish(ctx context.Context) error {
	for {
		h.mu.Lock()
		in := h.latestFinish
		revision := h.finishRevision
		h.mu.Unlock()

		_, err := h.service.db.RequestAuditAttempt.UpdateOneID(h.id).
			SetStatusCode(in.StatusCode).
			SetVerdict(in.Verdict).
			SetReason(in.Reason).
			SetRetryAfterMs(in.RetryAfter.Milliseconds()).
			SetLatencyMs(in.Latency.Milliseconds()).
			SetFirstTokenMs(in.FirstTokenMs).
			SetResponseStarted(in.ResponseStarted).
			SetStreamCompleted(in.StreamCompleted).
			SetFinished(true).
			Save(ctx)
		if err != nil {
			return err
		}

		h.mu.Lock()
		if h.finishRevision == revision {
			h.finished = true
			h.finishQueued = false
			h.mu.Unlock()
			return nil
		}
		h.mu.Unlock()
	}
}

func (s *Service) seal(plain []byte) (string, error) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(plain); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	ciphertext, err := auth.EncryptAPIKey(compressed.String(), s.secret)
	if err != nil {
		return "", err
	}
	return payloadPrefix + ciphertext, nil
}

func (s *Service) open(ciphertext string) ([]byte, error) {
	if !strings.HasPrefix(ciphertext, payloadPrefix) {
		return nil, fmt.Errorf("不支持的审计密文格式")
	}
	compressed, err := auth.DecryptAPIKey(strings.TrimPrefix(ciphertext, payloadPrefix), s.secret)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(strings.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

func detachedTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
}

func cloneHeader(src http.Header) http.Header {
	if src == nil {
		return http.Header{}
	}
	return src.Clone()
}

func readAndRestoreBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	return body, nil
}

// redactBase64Images 仅处理审计副本，不修改真实发包内容。它递归扫描 JSON 中的
// data:image/...;base64 数据和常见图片 base64 字段，用长度与 SHA-256 占位，
// 既保留请求结构和可比性，也避免图片原文与超大字符串进入审计库。
func redactBase64Images(body []byte) []byte {
	if len(body) == 0 || !json.Valid(body) {
		return body
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return body
	}
	if !redactJSONValue(value, "") {
		return body
	}
	redacted, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return redacted
}

func redactJSONValue(value any, fieldName string) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		imageContainer := jsonObjectContainsImage(typed)
		for key, child := range typed {
			if text, ok := child.(string); ok && (shouldRedactImageString(key, text) ||
				(imageContainer && isImageDataField(key) && len(strings.TrimSpace(text)) >= 256 && looksLikeBase64(strings.TrimSpace(text)))) {
				typed[key] = imageRedactionPlaceholder(text)
				changed = true
				continue
			}
			if redactJSONValue(child, key) {
				changed = true
			}
		}
	case []any:
		for index, child := range typed {
			if text, ok := child.(string); ok && shouldRedactImageString(fieldName, text) {
				typed[index] = imageRedactionPlaceholder(text)
				changed = true
				continue
			}
			if redactJSONValue(child, fieldName) {
				changed = true
			}
		}
	}
	return changed
}

func jsonObjectContainsImage(value map[string]any) bool {
	for _, key := range []string{"mime_type", "mimeType", "media_type", "mediaType"} {
		if text, ok := value[key].(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "image/") {
			return true
		}
	}
	if text, ok := value["type"].(string); ok {
		typeName := strings.ToLower(strings.TrimSpace(text))
		return strings.Contains(typeName, "image") || typeName == "base64"
	}
	return false
}

func isImageDataField(fieldName string) bool {
	switch strings.ToLower(strings.TrimSpace(fieldName)) {
	case "data", "image", "content":
		return true
	default:
		return false
	}
}

func shouldRedactImageString(fieldName, value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(trimmed), "data:image/") && strings.Contains(trimmed, ";base64,") {
		return true
	}
	key := strings.ToLower(strings.TrimSpace(fieldName))
	switch key {
	case "b64_json", "image_base64", "image_b64", "base64_image", "image_data":
		return len(trimmed) >= 256 && looksLikeBase64(trimmed)
	default:
		return false
	}
}

func looksLikeBase64(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' ||
			r == '-' || r == '_' || r == '\r' || r == '\n' {
			continue
		}
		return false
	}
	return true
}

func imageRedactionPlaceholder(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("[base64 图片已脱敏；原始字符数=%d；sha256=%s]", len(value), hex.EncodeToString(sum[:]))
}

// PayloadView 是 Web 端可展示的解密内容。文本直接返回，二进制使用 base64。
type PayloadView struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Bytes    int64  `json:"bytes"`
	Pending  bool   `json:"pending"`
}

func payloadView(raw []byte) PayloadView {
	if utf8.Valid(raw) {
		return PayloadView{Encoding: "utf8", Content: string(raw), Bytes: int64(len(raw))}
	}
	return PayloadView{Encoding: "base64", Content: base64.StdEncoding.EncodeToString(raw), Bytes: int64(len(raw))}
}

func (s *Service) openPayload(ciphertext string, logicalBytes int64) (PayloadView, error) {
	if ciphertext == "" {
		return PayloadView{Encoding: "pending", Bytes: logicalBytes, Pending: true}, nil
	}
	raw, err := s.open(ciphertext)
	if err != nil {
		return PayloadView{}, err
	}
	view := payloadView(raw)
	if logicalBytes > 0 {
		view.Bytes = logicalBytes
	}
	return view, nil
}

// ListFilter 是管理员审计列表筛选条件。
type ListFilter struct {
	Page       int
	PageSize   int
	Keyword    string
	Model      string
	StatusCode int
	AccountID  int
	ChannelID  int
	Start      *time.Time
	End        *time.Time
}

// ListItem 是审计列表行。
type ListItem struct {
	ID            int       `json:"id"`
	RequestID     string    `json:"request_id"`
	UserID        int       `json:"user_id"`
	UserEmail     string    `json:"user_email"`
	APIKeyID      int       `json:"api_key_id"`
	GroupID       int       `json:"group_id"`
	Client        string    `json:"client"`
	Protocol      string    `json:"protocol"`
	Endpoint      string    `json:"endpoint"`
	Model         string    `json:"model"`
	Stream        bool      `json:"stream"`
	StatusCode    int       `json:"status_code"`
	DurationMs    int64     `json:"duration_ms"`
	ResponseBytes int64     `json:"response_bytes"`
	Completed     bool      `json:"completed"`
	AttemptCount  int       `json:"attempt_count"`
	Routes        []string  `json:"routes"`
	CreatedAt     time.Time `json:"created_at"`
}

// AttemptView 是详情中的实际上游尝试。
type AttemptView struct {
	ID              int         `json:"id"`
	Seq             int         `json:"seq"`
	RouteKind       string      `json:"route_kind"`
	ChannelID       int         `json:"channel_id"`
	ChannelName     string      `json:"channel_name"`
	ChannelKeyID    int         `json:"channel_key_id"`
	ChannelKeyName  string      `json:"channel_key_name"`
	AccountID       int         `json:"account_id"`
	AccountName     string      `json:"account_name"`
	AccountEmail    string      `json:"account_email"`
	AccountPlatform string      `json:"account_platform"`
	AccountType     string      `json:"account_type"`
	Method          string      `json:"method"`
	UpstreamURL     PayloadView `json:"upstream_url"`
	Headers         PayloadView `json:"headers"`
	Body            PayloadView `json:"body"`
	StatusCode      int         `json:"status_code"`
	Verdict         string      `json:"verdict"`
	Reason          string      `json:"reason"`
	RetryAfterMs    int64       `json:"retry_after_ms"`
	LatencyMs       int64       `json:"latency_ms"`
	FirstTokenMs    int64       `json:"first_token_ms"`
	ResponseStarted bool        `json:"response_started"`
	StreamCompleted bool        `json:"stream_completed"`
	Finished        bool        `json:"finished"`
	CreatedAt       time.Time   `json:"created_at"`
}

// Detail 是管理员审计详情。
type Detail struct {
	ListItem
	Method         string        `json:"method"`
	Path           string        `json:"path"`
	RawQuery       string        `json:"raw_query"`
	Host           string        `json:"host"`
	RequestProto   string        `json:"request_proto"`
	RemoteAddr     string        `json:"remote_addr"`
	IPAddress      string        `json:"ip_address"`
	UserAgent      string        `json:"user_agent"`
	ContentType    string        `json:"content_type"`
	ContentLength  int64         `json:"content_length"`
	InboundHeaders PayloadView   `json:"inbound_headers"`
	InboundBody    PayloadView   `json:"inbound_body"`
	Attempts       []AttemptView `json:"attempts"`
}

// List 查询管理员审计列表。
func (s *Service) List(ctx context.Context, f ListFilter) ([]ListItem, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	q := s.db.RequestAuditLog.Query()
	if keyword := strings.TrimSpace(f.Keyword); keyword != "" {
		q.Where(entlog.Or(
			entlog.RequestIDContainsFold(keyword),
			entlog.UserEmailSnapshotContainsFold(keyword),
			entlog.ModelContainsFold(keyword),
			entlog.HasAttemptsWith(entattempt.Or(
				entattempt.AccountEmailContainsFold(keyword),
				entattempt.AccountNameContainsFold(keyword),
				entattempt.ChannelNameContainsFold(keyword),
				entattempt.ChannelKeyNameContainsFold(keyword),
			)),
		))
	}
	if model := strings.TrimSpace(f.Model); model != "" {
		q.Where(entlog.ModelContainsFold(model))
	}
	if f.StatusCode > 0 {
		// 包含最终成功前的 429/5xx 尝试，便于直接筛出“429 后换账号成功”。
		q.Where(entlog.Or(
			entlog.StatusCodeEQ(f.StatusCode),
			entlog.HasAttemptsWith(entattempt.StatusCodeEQ(f.StatusCode)),
		))
	}
	if f.AccountID > 0 {
		q.Where(entlog.HasAttemptsWith(entattempt.AccountIDEQ(f.AccountID)))
	}
	if f.ChannelID > 0 {
		q.Where(entlog.HasAttemptsWith(entattempt.ChannelIDEQ(f.ChannelID)))
	}
	if f.Start != nil {
		q.Where(entlog.CreatedAtGTE(*f.Start))
	}
	if f.End != nil {
		q.Where(entlog.CreatedAtLTE(*f.End))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.
		Select(
			entlog.FieldID, entlog.FieldRequestID, entlog.FieldUserID,
			entlog.FieldUserEmailSnapshot, entlog.FieldAPIKeyID, entlog.FieldGroupID,
			entlog.FieldClient, entlog.FieldProtocol, entlog.FieldEndpoint, entlog.FieldModel,
			entlog.FieldStream, entlog.FieldStatusCode, entlog.FieldDurationMs,
			entlog.FieldResponseBytes, entlog.FieldCompleted, entlog.FieldCreatedAt,
		).
		WithAttempts(func(aq *ent.RequestAuditAttemptQuery) {
			aq.Select(
				entattempt.FieldID, entattempt.FieldRequestAuditID, entattempt.FieldSeq,
				entattempt.FieldRouteKind, entattempt.FieldChannelName,
				entattempt.FieldChannelKeyName, entattempt.FieldAccountName,
				entattempt.FieldAccountEmail,
			).Order(ent.Asc(entattempt.FieldSeq))
		}).
		Order(ent.Desc(entlog.FieldCreatedAt)).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, listItemFromEnt(row))
	}
	return items, total, nil
}

// Clear 清空请求审计。
// before 非 nil 时仅删除 created_at 严格早于 before 的记录；nil 清空全部。
// 先删 attempt 再删主表：SQLite 测试库与部分迁移环境下 bulk 删除不保证 CASCADE 生效。
func (s *Service) Clear(ctx context.Context, before *time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("请求审计服务未配置")
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	attemptDel := tx.RequestAuditAttempt.Delete()
	logDel := tx.RequestAuditLog.Delete()
	if before != nil {
		attemptDel = attemptDel.Where(entattempt.HasRequestWith(entlog.CreatedAtLT(*before)))
		logDel = logDel.Where(entlog.CreatedAtLT(*before))
	}
	if _, err := attemptDel.Exec(ctx); err != nil {
		return 0, err
	}
	n, err := logDel.Exec(ctx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(n), nil
}

// Get 查询并解密单条审计详情。
func (s *Service) Get(ctx context.Context, id int) (Detail, error) {
	row, err := s.db.RequestAuditLog.Query().
		Where(entlog.IDEQ(id)).
		WithAttempts(func(aq *ent.RequestAuditAttemptQuery) { aq.Order(ent.Asc(entattempt.FieldSeq)) }).
		Only(ctx)
	if err != nil {
		return Detail{}, err
	}
	headers, err := s.openPayload(row.InboundHeadersEnc, 0)
	if err != nil {
		return Detail{}, fmt.Errorf("解密入站请求头失败: %w", err)
	}
	body, err := s.openPayload(row.InboundBodyEnc, row.InboundBodyBytes)
	if err != nil {
		return Detail{}, fmt.Errorf("解密入站请求体失败: %w", err)
	}
	detail := Detail{
		ListItem:       listItemFromEnt(row),
		Method:         row.Method,
		Path:           row.Path,
		RawQuery:       row.RawQuery,
		Host:           row.Host,
		RequestProto:   row.RequestProto,
		RemoteAddr:     row.RemoteAddr,
		IPAddress:      row.IPAddress,
		UserAgent:      row.UserAgent,
		ContentType:    row.ContentType,
		ContentLength:  row.ContentLength,
		InboundHeaders: headers,
		InboundBody:    body,
		Attempts:       make([]AttemptView, 0, len(row.Edges.Attempts)),
	}
	for _, attempt := range row.Edges.Attempts {
		urlView, errOpen := s.openPayload(attempt.UpstreamURLEnc, 0)
		if errOpen != nil {
			return Detail{}, fmt.Errorf("解密第 %d 次上游 URL 失败: %w", attempt.Seq, errOpen)
		}
		headersView, errOpen := s.openPayload(attempt.ForwardHeadersEnc, 0)
		if errOpen != nil {
			return Detail{}, fmt.Errorf("解密第 %d 次上游请求头失败: %w", attempt.Seq, errOpen)
		}
		bodyView, errOpen := s.openPayload(attempt.ForwardBodyEnc, attempt.ForwardBodyBytes)
		if errOpen != nil {
			return Detail{}, fmt.Errorf("解密第 %d 次上游请求体失败: %w", attempt.Seq, errOpen)
		}
		detail.Attempts = append(detail.Attempts, AttemptView{
			ID: attempt.ID, Seq: attempt.Seq, RouteKind: string(attempt.RouteKind),
			ChannelID: attempt.ChannelID, ChannelName: attempt.ChannelName,
			ChannelKeyID: attempt.ChannelKeyID, ChannelKeyName: attempt.ChannelKeyName,
			AccountID: attempt.AccountID, AccountName: attempt.AccountName,
			AccountEmail: attempt.AccountEmail, AccountPlatform: attempt.AccountPlatform,
			AccountType: attempt.AccountType, Method: attempt.Method,
			UpstreamURL: urlView, Headers: headersView, Body: bodyView,
			StatusCode: attempt.StatusCode, Verdict: attempt.Verdict, Reason: attempt.Reason,
			RetryAfterMs: attempt.RetryAfterMs, LatencyMs: attempt.LatencyMs,
			FirstTokenMs: attempt.FirstTokenMs, ResponseStarted: attempt.ResponseStarted,
			StreamCompleted: attempt.StreamCompleted, Finished: attempt.Finished,
			CreatedAt: attempt.CreatedAt,
		})
	}
	return detail, nil
}

func listItemFromEnt(row *ent.RequestAuditLog) ListItem {
	routes := make([]string, 0, len(row.Edges.Attempts))
	seen := make(map[string]struct{}, len(row.Edges.Attempts))
	for _, attempt := range row.Edges.Attempts {
		label := attempt.ChannelName
		if attempt.RouteKind == entattempt.RouteKindAccount {
			label = attempt.AccountName
			if attempt.AccountEmail != "" {
				label += " · " + attempt.AccountEmail
			}
		} else if attempt.ChannelKeyName != "" {
			label += " · " + attempt.ChannelKeyName
		}
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		routes = append(routes, label)
	}
	return ListItem{
		ID: row.ID, RequestID: row.RequestID, UserID: row.UserID,
		UserEmail: row.UserEmailSnapshot, APIKeyID: row.APIKeyID, GroupID: row.GroupID,
		Client: row.Client, Protocol: row.Protocol, Endpoint: row.Endpoint, Model: row.Model,
		Stream: row.Stream, StatusCode: row.StatusCode, DurationMs: row.DurationMs,
		ResponseBytes: row.ResponseBytes, Completed: row.Completed,
		AttemptCount: len(row.Edges.Attempts), Routes: routes, CreatedAt: row.CreatedAt,
	}
}
