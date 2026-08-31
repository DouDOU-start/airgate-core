package pipeline

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

const (
	codexFileUploadLimitBytes      int64 = 512 << 20
	codexFileControlBodyLimitBytes       = 64 << 10
	codexFileUploadLeaseTTL              = 15 * time.Minute
	codexFileUploadStoreLimit            = 4096
	codexFileUploadPathPrefix            = "/_airgate/codex/files/upload/"
)

type codexFileUploadState uint8

const (
	codexFileUploadIssued codexFileUploadState = iota
	codexFileUploadInFlight
	codexFileUploadComplete
)

type codexFileUploadKey struct {
	apiKeyID int
	groupID  int
	fileID   string
}

type codexFileUploadLease struct {
	token          string
	fileID         string
	uploadURL      string
	expectedSize   int64
	accountID      int
	apiKeyID       int
	userID         int
	groupID        int
	proxyURL       string
	createBody     []byte
	pdfReservation bool
	expiresAt      time.Time
	state          codexFileUploadState
}

type codexFileUploadStore struct {
	mu      sync.Mutex
	byToken map[string]*codexFileUploadLease
	byFile  map[codexFileUploadKey]string
}

func newCodexFileUploadStore() *codexFileUploadStore {
	return &codexFileUploadStore{
		byToken: make(map[string]*codexFileUploadLease),
		byFile:  make(map[codexFileUploadKey]string),
	}
}

func (p *Pipeline) fileUploadStore() *codexFileUploadStore {
	if p.codexFileUploads != nil {
		return p.codexFileUploads
	}
	// A few focused tests construct Pipeline directly instead of using New.
	// Reuse accountTransportMu as the lazy-init guard so zero-value pipelines
	// remain safe without adding another mutex to the hot request path.
	p.accountTransportMu.Lock()
	if p.codexFileUploads == nil {
		p.codexFileUploads = newCodexFileUploadStore()
	}
	store := p.codexFileUploads
	p.accountTransportMu.Unlock()
	return store
}

func newCodexFileUploadToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *codexFileUploadStore) issue(lease codexFileUploadLease) (string, error) {
	if s == nil {
		return "", errors.New("Codex file upload store is unavailable")
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if len(s.byToken) >= codexFileUploadStoreLimit {
		return "", errors.New("Codex file upload store is full")
	}
	for attempt := 0; attempt < 4; attempt++ {
		token, err := newCodexFileUploadToken()
		if err != nil {
			return "", err
		}
		if _, exists := s.byToken[token]; exists {
			continue
		}
		lease.token = token
		lease.createBody = append([]byte(nil), lease.createBody...)
		lease.expiresAt = now.Add(codexFileUploadLeaseTTL)
		lease.state = codexFileUploadIssued
		key := codexFileUploadKey{apiKeyID: lease.apiKeyID, groupID: lease.groupID, fileID: lease.fileID}
		if previous := s.byFile[key]; previous != "" {
			s.removeLocked(previous)
		}
		copyLease := lease
		s.byToken[token] = &copyLease
		s.byFile[key] = token
		return token, nil
	}
	return "", errors.New("failed to allocate a unique Codex file upload token")
}

func (s *codexFileUploadStore) beginUpload(token string) (codexFileUploadLease, bool) {
	if s == nil {
		return codexFileUploadLease{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	lease := s.byToken[token]
	if lease == nil || lease.state != codexFileUploadIssued || !lease.expiresAt.After(now) {
		return codexFileUploadLease{}, false
	}
	lease.state = codexFileUploadInFlight
	return cloneCodexFileLease(lease), true
}

func (s *codexFileUploadStore) finishUpload(token string, success bool) {
	if s == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	lease := s.byToken[token]
	if lease == nil {
		return
	}
	if !lease.expiresAt.After(now) {
		s.removeLocked(token)
		return
	}
	if success {
		lease.state = codexFileUploadComplete
		return
	}
	// A failed transport attempt may be retried with the same opaque token
	// while the lease is alive. Concurrent attempts remain excluded by the
	// in-flight state, and a successful upload can never be replayed.
	lease.state = codexFileUploadIssued
}

func (s *codexFileUploadStore) uploadedFile(apiKeyID, groupID int, fileID string) (codexFileUploadLease, bool) {
	if s == nil {
		return codexFileUploadLease{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	token := s.byFile[codexFileUploadKey{apiKeyID: apiKeyID, groupID: groupID, fileID: fileID}]
	lease := s.byToken[token]
	if lease == nil || lease.state != codexFileUploadComplete || !lease.expiresAt.After(now) {
		return codexFileUploadLease{}, false
	}
	return cloneCodexFileLease(lease), true
}

func (s *codexFileUploadStore) consume(apiKeyID, groupID int, fileID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	token := s.byFile[codexFileUploadKey{apiKeyID: apiKeyID, groupID: groupID, fileID: fileID}]
	if token != "" {
		s.removeLocked(token)
	}
	s.mu.Unlock()
}

func (s *codexFileUploadStore) removeLocked(token string) {
	lease := s.byToken[token]
	if lease == nil {
		return
	}
	delete(s.byToken, token)
	key := codexFileUploadKey{apiKeyID: lease.apiKeyID, groupID: lease.groupID, fileID: lease.fileID}
	if s.byFile[key] == token {
		delete(s.byFile, key)
	}
}

func (s *codexFileUploadStore) sweepLocked(now time.Time) {
	for token, lease := range s.byToken {
		if lease == nil || !lease.expiresAt.After(now) {
			s.removeLocked(token)
		}
	}
}

func cloneCodexFileLease(lease *codexFileUploadLease) codexFileUploadLease {
	if lease == nil {
		return codexFileUploadLease{}
	}
	copyLease := *lease
	copyLease.createBody = append([]byte(nil), lease.createBody...)
	return copyLease
}

type codexFileCreateRequest struct {
	FileName string          `json:"file_name"`
	FileSize json.RawMessage `json:"file_size"`
	UseCase  string          `json:"use_case"`
}

type codexFileCreateResponse struct {
	FileID             string `json:"file_id"`
	UploadURL          string `json:"upload_url"`
	PDFC2PAReservation bool   `json:"pdf_c2pa_reservation"`
}

// HandleCodexFilesCreate forwards the small authenticated file registration
// request through the selected native Codex executor, then replaces the
// provider-signed blob URL with a local opaque upload URL. The signed URL is
// never exposed to the caller and cannot be supplied by a client.
func (p *Pipeline) HandleCodexFilesCreate(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, adaptor.EndpointFilesCreate) {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	if len(body) > codexFileControlBodyLimitBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "Codex 文件控制请求体过大")
		return
	}
	request, err := parseCodexFileCreateRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_file_request", err.Error())
		return
	}
	if request.fileSize > uint64(codexFileUploadLimitBytes) {
		writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "file_too_large", "文件超过 512 MiB 上限")
		return
	}
	relayReq, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "请求体必须是 JSON 对象")
		return
	}
	relayReq.Model = ""
	relayReq.Stream = false
	p.forwardOpt(c, keyInfo, relayReq, adaptor.EndpointFilesCreate, forwardOptions{
		rawBody: body, rawContentType: c.GetHeader("Content-Type"),
		nativeCodexAccountsOnly: true, allowUnpriced: true,
		zeroBilling: true, passthroughResponse: true,
		// The response transform must see JSON, while the native executor keeps
		// upstream bytes untouched for ordinary Responses calls. Control-plane
		// Files responses therefore explicitly disable content compression.
		providerHeaders: http.Header{"Accept-Encoding": {"identity"}},
		providerPath:    "/files",
		responseTransform: func(account *accountreg.Snapshot, result *attemptResult) error {
			return p.rewriteCodexFileCreateResponse(c, keyInfo, account, request, body, result)
		},
	})
}

type parsedCodexFileCreateRequest struct {
	fileName string
	fileSize uint64
}

func parseCodexFileCreateRequest(body []byte) (parsedCodexFileCreateRequest, error) {
	var value codexFileCreateRequest
	if err := json.Unmarshal(body, &value); err != nil {
		return parsedCodexFileCreateRequest{}, errors.New("请求体必须是 JSON 对象")
	}
	if strings.TrimSpace(value.FileName) == "" {
		return parsedCodexFileCreateRequest{}, errors.New("缺少 file_name 字段")
	}
	if len(value.FileName) > 4096 {
		return parsedCodexFileCreateRequest{}, errors.New("file_name 过长")
	}
	if value.UseCase != "codex" {
		return parsedCodexFileCreateRequest{}, errors.New("use_case 必须为 codex")
	}
	fileSize, err := parseCodexFileSize(value.FileSize)
	if err != nil {
		return parsedCodexFileCreateRequest{}, err
	}
	return parsedCodexFileCreateRequest{fileName: value.FileName, fileSize: fileSize}, nil
}

func parseCodexFileSize(raw json.RawMessage) (uint64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errors.New("缺少 file_size 字段")
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, errors.New("file_size 必须是非负整数")
	}
	value, err := strconv.ParseUint(string(number), 10, 64)
	if err != nil {
		return 0, errors.New("file_size 必须是非负整数")
	}
	return value, nil
}

func (p *Pipeline) rewriteCodexFileCreateResponse(
	c *gin.Context,
	keyInfo *auth.APIKeyInfo,
	account *accountreg.Snapshot,
	request parsedCodexFileCreateRequest,
	createBody []byte,
	result *attemptResult,
) error {
	if c == nil || keyInfo == nil || account == nil || result == nil {
		return errors.New("Codex file response context is incomplete")
	}
	var response codexFileCreateResponse
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.body, &fields); err != nil || fields == nil {
		return errors.New("provider file create response is not a JSON object")
	}
	if err := json.Unmarshal(result.body, &response); err != nil {
		return errors.New("provider file create response is invalid")
	}
	response.FileID = strings.TrimSpace(response.FileID)
	if !validCodexFileID(response.FileID) {
		return errors.New("provider file create response has an invalid file_id")
	}
	uploadURL, err := validateCodexBlobUploadURL(response.UploadURL)
	if err != nil {
		return err
	}
	token, err := p.fileUploadStore().issue(codexFileUploadLease{
		fileID: response.FileID, uploadURL: uploadURL,
		expectedSize: int64(request.fileSize), accountID: account.ID,
		apiKeyID: keyInfo.KeyID, userID: keyInfo.UserID, groupID: keyInfo.GroupID,
		proxyURL: account.ProxyURL, createBody: createBody,
		pdfReservation: response.PDFC2PAReservation,
	})
	if err != nil {
		return err
	}
	localUploadURL, err := codexLocalUploadURL(c, token)
	if err != nil {
		p.fileUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, response.FileID)
		return err
	}
	encodedURL, err := json.Marshal(localUploadURL)
	if err != nil {
		p.fileUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, response.FileID)
		return err
	}
	fields["upload_url"] = encodedURL
	rewritten, err := json.Marshal(fields)
	if err != nil {
		p.fileUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, response.FileID)
		return err
	}
	result.body = rewritten
	result.contentType = "application/json"
	if result.headers == nil {
		result.headers = make(http.Header)
	}
	result.headers.Set("Content-Type", "application/json")
	return nil
}

func validateCodexBlobUploadURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	// Fragments never reach an HTTP server. Check the raw delimiter too because
	// net/url represents a trailing bare `#` as an empty Fragment; accepting it
	// would store a different signed upload URL from the one returned upstream.
	if err != nil || strings.Contains(raw, "#") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("provider file create response has an invalid upload_url")
	}
	// The URL originates from the provider response, but it is still an
	// untrusted value from the gateway's point of view. Reject obvious SSRF
	// targets before storing it as a bearer lease. DNS names are intentionally
	// left available for sovereign/private storage domains; the selected account
	// transport remains responsible for proxy policy and ordinary DNS routing.
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(parsed.Hostname()), "."))
	if host == "" || isUnsafeCodexBlobHost(host) {
		return "", errors.New("provider file create response has an unsafe upload_url host")
	}
	return parsed.String(), nil
}

// Kept as a variable so security tests can exercise DNS-to-private-address
// handling deterministically without depending on the host resolver. In
// production this is the standard resolver; a lookup failure is intentionally
// non-fatal because the selected account transport may resolve the name inside
// an authenticated/private proxy network.
var lookupCodexBlobHostIPs = net.LookupIP

func isUnsafeCodexBlobHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	// Zone identifiers are meaningful only for link-local IPv6 addresses. Do
	// not let a percent-encoded zone bypass the IP parser (for example
	// `[fe80::1%25eth0]`).  Such destinations are never valid public storage
	// endpoints and are unsafe even before DNS resolution is considered.
	if strings.Contains(host, "%") {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return isUnsafeCodexBlobIP(ip)
	}
	// net.ParseIP deliberately accepts only canonical dotted-decimal IPv4.
	// URL clients and operating-system resolvers commonly also accept legacy
	// integer/hex/octal spellings (2130706433, 0x7f000001, 0177.0.0.1,
	// 127.1), which can disguise loopback/private destinations.  Parse those
	// spellings before treating the value as an ordinary DNS name.
	if ip := parseCodexObfuscatedIPv4(host); ip != nil {
		return isUnsafeCodexBlobIP(ip)
	}
	if lookup := lookupCodexBlobHostIPs; lookup != nil {
		if ips, err := lookup(host); err == nil {
			for _, resolved := range ips {
				if isUnsafeCodexBlobIP(resolved) {
					return true
				}
			}
		}
	}
	return false
}

func isUnsafeCodexBlobIP(ip net.IP) bool {
	return ip != nil && (ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
}

// parseCodexObfuscatedIPv4 recognizes the historical URL/inet_aton forms
// accepted by several HTTP stacks. It intentionally returns nil for ordinary
// DNS names; callers can then apply their normal DNS/proxy policy.
func parseCodexObfuscatedIPv4(host string) net.IP {
	if host == "" {
		return nil
	}
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return nil
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil
		}
		base := 10
		digits := part
		if strings.HasPrefix(part, "0x") {
			base = 16
			digits = part[2:]
			if digits == "" {
				return nil
			}
		} else if len(part) > 1 && strings.HasPrefix(part, "0") {
			// inet_aton-style octal label (for example 0177.0.0.1).
			base = 8
			digits = part[1:]
			if digits == "" {
				digits = "0"
			}
		}
		// A label containing anything beyond the selected numeric alphabet is a
		// hostname, not an obfuscated IPv4 spelling.
		for _, r := range digits {
			valid := false
			switch base {
			case 8:
				valid = r >= '0' && r <= '7'
			case 10:
				valid = r >= '0' && r <= '9'
			case 16:
				valid = (r >= '0' && r <= '9') ||
					(r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			}
			if !valid {
				return nil
			}
		}
		value, err := strconv.ParseUint(digits, base, 32)
		if err != nil {
			return nil
		}
		values[i] = value
	}
	var packed uint64
	switch len(values) {
	case 1:
		packed = values[0]
	case 2:
		if values[0] > 0xff || values[1] > 0xffffff {
			return nil
		}
		packed = (values[0] << 24) | values[1]
	case 3:
		if values[0] > 0xff || values[1] > 0xff || values[2] > 0xffff {
			return nil
		}
		packed = (values[0] << 24) | (values[1] << 16) | values[2]
	case 4:
		for _, value := range values {
			if value > 0xff {
				return nil
			}
		}
		packed = (values[0] << 24) | (values[1] << 16) | (values[2] << 8) | values[3]
	default:
		return nil
	}
	return net.IPv4(byte(packed>>24), byte(packed>>16), byte(packed>>8), byte(packed))
}

func codexLocalUploadURL(c *gin.Context, token string) (string, error) {
	if c == nil || c.Request == nil || c.Request.URL == nil || token == "" {
		return "", errors.New("cannot build local Codex upload URL")
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if forwarded := firstForwardedValue(c.GetHeader("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := strings.TrimSpace(c.Request.Host)
	if forwarded := firstForwardedValue(c.GetHeader("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	if !validCodexLocalUploadHost(host) {
		return "", errors.New("cannot build local Codex upload URL without a host")
	}
	return scheme + "://" + host + codexFileUploadPathPrefix + url.PathEscape(token), nil
}

// validCodexLocalUploadHost accepts the authority forms emitted by trusted
// reverse proxies (DNS names, IPv4, and bracketed IPv6 with an optional port)
// while rejecting path/userinfo/control injection through an untrusted
// X-Forwarded-Host header. The generated URL contains a bearer upload token,
// so pointing it at an attacker-controlled host would leak that token.
func validCodexLocalUploadHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\?#\r\n") || strings.IndexFunc(host, func(r rune) bool {
		return r < 0x20 || r == 0x7f || unicode.IsSpace(r)
	}) >= 0 {
		return false
	}
	u, err := url.Parse("//" + host)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" {
		return false
	}
	return true
}

func firstForwardedValue(value string) string {
	if index := strings.IndexByte(value, ','); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// HandleCodexFileUpload is intentionally unauthenticated: the official client
// does not copy the AirGate API key onto the signed blob PUT. The 256-bit
// opaque token is the sole bearer credential and maps to exactly one provider
// URL chosen from a trusted create response. Arbitrary URL forwarding is not
// supported.
func (p *Pipeline) HandleCodexFileUpload(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !strings.EqualFold(c.Request.Method, http.MethodPut) {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_upload_method", "Codex 文件上传仅支持 PUT")
		return
	}
	token := strings.TrimSpace(c.Param("token"))
	if token == "" || strings.ContainsAny(token, "/\\") {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_upload_token", "Codex 文件上传令牌无效或已过期")
		return
	}
	lease, ok := p.fileUploadStore().beginUpload(token)
	if !ok {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_upload_token", "Codex 文件上传令牌无效、已使用或已过期")
		return
	}
	success := false
	defer func() { p.fileUploadStore().finishUpload(token, success) }()

	if lease.expectedSize < 0 || lease.expectedSize > codexFileUploadLimitBytes {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_file_size", "Codex 文件大小无效")
		return
	}
	if c.Request.ContentLength < 0 {
		writeError(c, http.StatusLengthRequired, "invalid_request_error", "content_length_required", "Codex 文件上传必须提供 Content-Length")
		return
	}
	if c.Request.ContentLength != lease.expectedSize {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "content_length_mismatch", "Content-Length 与文件创建请求不一致")
		return
	}
	blobType := strings.TrimSpace(c.GetHeader("X-Ms-Blob-Type"))
	if blobType != "" && !strings.EqualFold(blobType, "BlockBlob") {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_blob_type", "x-ms-blob-type 必须为 BlockBlob")
		return
	}
	clientRequestID := strings.TrimSpace(c.GetHeader("X-Ms-Client-Request-Id"))
	if clientRequestID == "" {
		clientRequestID = uuid.NewString()
	} else if _, err := uuid.Parse(clientRequestID); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_client_request_id", "x-ms-client-request-id 必须为 UUID")
		return
	}

	// Stage the body before contacting the provider.  The incoming
	// Content-Length is useful as a fast rejection above, but it is not a
	// sufficient integrity check when a test/client supplies a reader whose
	// actual contents are longer than the declared length.  Reading one byte
	// beyond the expected size lets us reject both short and overlong bodies
	// before any bytes are sent to the signed blob URL.  A temporary file keeps
	// memory bounded for the 512 MiB upload limit and gives the outbound
	// transport an exact, rewindable Content-Length body.
	tempFile, err := os.CreateTemp("", "airgate-codex-upload-*")
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "upload_staging_failed", "无法准备 Codex 文件上传")
		return
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	readLimit := lease.expectedSize + 1
	if readLimit < lease.expectedSize || readLimit > codexFileUploadLimitBytes+1 {
		readLimit = codexFileUploadLimitBytes + 1
	}
	limitedBody := io.LimitReader(c.Request.Body, readLimit)
	actualSize, err := io.Copy(tempFile, limitedBody)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "upload_read_failed", "读取 Codex 文件上传内容失败")
		return
	}
	if actualSize != lease.expectedSize {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "upload_size_mismatch", "实际上传字节数与文件创建请求不一致")
		return
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "upload_staging_failed", "无法准备 Codex 文件上传")
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPut, lease.uploadURL, tempFile)
	if err != nil {
		writeError(c, http.StatusBadGateway, "upstream_error", "upload_request_failed", "无法构造 Codex 文件上传请求")
		return
	}
	upstreamRequest.ContentLength = lease.expectedSize
	copyCodexBlobUploadHeaders(upstreamRequest.Header, c.Request.Header)
	upstreamRequest.Header.Set("X-Ms-Blob-Type", "BlockBlob")
	upstreamRequest.Header.Set("X-Ms-Client-Request-Id", clientRequestID)

	transport, err := p.accountAuditTransport(lease.proxyURL)
	if err != nil || transport == nil {
		writeError(c, http.StatusBadGateway, "upstream_error", "invalid_proxy", "Codex 文件上传代理配置无效")
		return
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(upstreamRequest)
	if err != nil {
		writeError(c, http.StatusBadGateway, "upstream_error", "blob_upload_failed", "Codex 文件上传到存储服务失败")
		return
	}
	defer response.Body.Close()
	copySafeCodexBlobResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	// Azure Blob PUTs normally return an empty body. Commit the upstream status
	// before copying so Gin's recorder/net/http does not silently default an
	// empty response to 200 OK.
	c.Writer.WriteHeaderNow()
	// Do not reflect an arbitrary storage body. In particular, a storage
	// service/proxy must not be able to echo the signed upload URL back to the
	// caller. The official PUT contract does not require response bytes.
	if copyErr := drainCodexBlobResponseBody(response.Body); copyErr != nil {
		return
	}
	success = response.StatusCode >= 200 && response.StatusCode < 300
}

func copyCodexBlobUploadHeaders(dst, src http.Header) {
	for name, values := range src {
		lower := strings.ToLower(strings.TrimSpace(name))
		if len(values) == 0 || lower == "content-length" || lower == "host" ||
			isHopByHopProviderHeader(lower) || isCredentialProviderHeader(lower) ||
			strings.HasPrefix(lower, "x-forwarded-") {
			continue
		}
		// Keep the blob PUT contract deliberately narrow. In particular, do not
		// forward arbitrary x-ms-* fields such as x-ms-copy-source or encryption
		// headers: those can change a simple upload into a server-side copy or
		// otherwise alter the signed storage operation. The official Codex client
		// only needs the blob type, request id, and ordinary entity/condition
		// metadata below.
		allowed := lower == "content-type" || lower == "content-md5" ||
			lower == "content-encoding" || lower == "cache-control" ||
			strings.HasPrefix(lower, "if-") || lower == "x-ms-blob-type" ||
			lower == "x-ms-client-request-id" || lower == "x-ms-version"
		if !allowed {
			continue
		}
		dst[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
}

// HandleCodexFilesFinalize forwards the final authenticated JSON call through
// the same account used by Files create. The provider download_url is returned
// unchanged; only the blob upload URL is hidden behind AirGate.
func (p *Pipeline) HandleCodexFilesFinalize(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !requireCodexClientBoundary(c, adaptor.EndpointFilesFinalize) {
		return
	}
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	fileID := strings.TrimSpace(c.Param("file_id"))
	if !validCodexFileID(fileID) {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_file_id", "Codex file_id 无效")
		return
	}
	lease, ok := p.fileUploadStore().uploadedFile(keyInfo.KeyID, keyInfo.GroupID, fileID)
	if !ok {
		writeError(c, http.StatusConflict, "invalid_request_error", "file_upload_not_ready", "Codex 文件尚未上传、已完成或已过期")
		return
	}
	body, ok := readRawBody(c)
	if !ok {
		return
	}
	if len(body) > codexFileControlBodyLimitBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "Codex 文件控制请求体过大")
		return
	}
	finalizeBody, err := codexFileFinalizeBody(body, lease)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_finalize_request", err.Error())
		return
	}
	relayReq, err := dto.ParseChatRequest(finalizeBody)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "请求体必须是 JSON 对象")
		return
	}
	relayReq.Model = ""
	relayReq.Stream = false
	p.forwardOpt(c, keyInfo, relayReq, adaptor.EndpointFilesFinalize, forwardOptions{
		rawBody: finalizeBody, rawContentType: c.GetHeader("Content-Type"),
		providerPath:            "/files/" + url.PathEscape(fileID) + "/uploaded",
		nativeCodexAccountsOnly: true, nativeCodexAccountID: lease.accountID,
		allowUnpriced: true, zeroBilling: true, passthroughResponse: true,
		providerHeaders: http.Header{"Accept-Encoding": {"identity"}},
		responseTransform: func(_ *accountreg.Snapshot, result *attemptResult) error {
			var response struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(result.body, &response); err != nil {
				return errors.New("provider file finalize response is invalid")
			}
			if strings.EqualFold(strings.TrimSpace(response.Status), "success") {
				p.fileUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, fileID)
			}
			return nil
		},
	})
}

func codexFileFinalizeBody(body []byte, lease codexFileUploadLease) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil || request == nil {
		return nil, errors.New("请求体必须是 JSON 对象")
	}
	if !lease.pdfReservation {
		return body, nil
	}
	var createRequest json.RawMessage
	if err := json.Unmarshal(lease.createBody, &createRequest); err != nil || len(createRequest) == 0 {
		return nil, errors.New("PDF C2PA 创建请求不可用")
	}
	request["pdf_c2pa_create_request"] = createRequest
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("编码 PDF C2PA finalize 请求失败: %w", err)
	}
	return encoded, nil
}

func validCodexFileID(fileID string) bool {
	fileID = strings.TrimSpace(fileID)
	// File IDs are a single opaque provider segment. Reject dot segments and
	// percent escapes as well as path separators: encoded `/`, `%2e`, and
	// `%25` must not be allowed to take a different route after another URL
	// parser decodes the value.
	if fileID == "" || fileID == "." || fileID == ".." || len(fileID) > 256 || strings.ContainsAny(fileID, "/\\?#%") {
		return false
	}
	for _, r := range fileID {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}
