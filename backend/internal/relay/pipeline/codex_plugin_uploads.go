package pipeline

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// Workspace plugin bundles are capped by the official Codex client at 50 MiB.
// The opaque relay deliberately uses a smaller, independent lease store than
// Files so a file upload cannot be confused with a plugin bundle upload.
const (
	codexPluginUploadLimitBytes int64 = 50 << 20
	codexPluginUploadLeaseTTL         = 15 * time.Minute
	codexPluginUploadStoreLimit       = 1024
	codexPluginUploadPathPrefix       = "/_airgate/codex/plugins/upload/"
)

type codexPluginUploadState uint8

const (
	codexPluginUploadIssued codexPluginUploadState = iota
	codexPluginUploadInFlight
	codexPluginUploadComplete
)

type codexPluginUploadKey struct {
	apiKeyID int
	groupID  int
	fileID   string
}

type codexPluginUploadLease struct {
	token        string
	fileID       string
	etag         string
	pluginID     string
	uploadURL    string
	expectedSize int64
	accountID    int
	apiKeyID     int
	userID       int
	groupID      int
	proxyURL     string
	expiresAt    time.Time
	state        codexPluginUploadState
}

type codexPluginUploadStore struct {
	mu      sync.Mutex
	byToken map[string]*codexPluginUploadLease
	byFile  map[codexPluginUploadKey]string
}

func newCodexPluginUploadStore() *codexPluginUploadStore {
	return &codexPluginUploadStore{
		byToken: make(map[string]*codexPluginUploadLease),
		byFile:  make(map[codexPluginUploadKey]string),
	}
}

func (p *Pipeline) pluginUploadStore() *codexPluginUploadStore {
	if p == nil {
		return nil
	}
	p.accountTransportMu.Lock()
	if p.codexPluginUploads == nil {
		p.codexPluginUploads = newCodexPluginUploadStore()
	}
	store := p.codexPluginUploads
	p.accountTransportMu.Unlock()
	return store
}

func newCodexPluginUploadToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *codexPluginUploadStore) issue(lease codexPluginUploadLease) (string, error) {
	if s == nil {
		return "", errors.New("codex plugin upload store is unavailable")
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if len(s.byToken) >= codexPluginUploadStoreLimit {
		return "", errors.New("codex plugin upload store is full")
	}
	for attempt := 0; attempt < 4; attempt++ {
		token, err := newCodexPluginUploadToken()
		if err != nil {
			return "", err
		}
		if _, exists := s.byToken[token]; exists {
			continue
		}
		lease.token = token
		lease.expiresAt = now.Add(codexPluginUploadLeaseTTL)
		lease.state = codexPluginUploadIssued
		key := codexPluginUploadKey{apiKeyID: lease.apiKeyID, groupID: lease.groupID, fileID: lease.fileID}
		if previous := s.byFile[key]; previous != "" {
			s.removeLocked(previous)
		}
		copyLease := lease
		s.byToken[token] = &copyLease
		s.byFile[key] = token
		return token, nil
	}
	return "", errors.New("failed to allocate a unique Codex plugin upload token")
}

func (s *codexPluginUploadStore) beginUpload(token string) (codexPluginUploadLease, bool) {
	if s == nil {
		return codexPluginUploadLease{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	lease := s.byToken[token]
	if lease == nil || lease.state != codexPluginUploadIssued || !lease.expiresAt.After(now) {
		return codexPluginUploadLease{}, false
	}
	lease.state = codexPluginUploadInFlight
	return *lease, true
}

func (s *codexPluginUploadStore) finishUpload(token string, success bool) {
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
		lease.state = codexPluginUploadComplete
		return
	}
	lease.state = codexPluginUploadIssued
}

func (s *codexPluginUploadStore) uploadedFile(apiKeyID, groupID int, fileID string) (codexPluginUploadLease, bool) {
	if s == nil {
		return codexPluginUploadLease{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	token := s.byFile[codexPluginUploadKey{apiKeyID: apiKeyID, groupID: groupID, fileID: fileID}]
	lease := s.byToken[token]
	if lease == nil || lease.state != codexPluginUploadComplete || !lease.expiresAt.After(now) {
		return codexPluginUploadLease{}, false
	}
	return *lease, true
}

func (s *codexPluginUploadStore) consume(apiKeyID, groupID int, fileID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if token := s.byFile[codexPluginUploadKey{apiKeyID: apiKeyID, groupID: groupID, fileID: fileID}]; token != "" {
		s.removeLocked(token)
	}
	s.mu.Unlock()
}

func (s *codexPluginUploadStore) removeLocked(token string) {
	lease := s.byToken[token]
	if lease == nil {
		return
	}
	delete(s.byToken, token)
	key := codexPluginUploadKey{apiKeyID: lease.apiKeyID, groupID: lease.groupID, fileID: lease.fileID}
	if s.byFile[key] == token {
		delete(s.byFile, key)
	}
}

func (s *codexPluginUploadStore) sweepLocked(now time.Time) {
	for token, lease := range s.byToken {
		if lease == nil || !lease.expiresAt.After(now) {
			s.removeLocked(token)
		}
	}
}

type codexPluginUploadURLRequest struct {
	filename  string
	mimeType  string
	sizeBytes int64
	pluginID  string
}

func parseCodexPluginUploadURLRequest(body []byte) (codexPluginUploadURLRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return codexPluginUploadURLRequest{}, errors.New("workspace plugin upload request must be a JSON object")
	}
	filename, err := requiredCodexPluginString(fields, "filename")
	if err != nil {
		return codexPluginUploadURLRequest{}, err
	}
	mimeType, err := requiredCodexPluginString(fields, "mime_type")
	if err != nil {
		return codexPluginUploadURLRequest{}, err
	}
	if len(filename) > 4096 || len(mimeType) > 256 || !validCodexPluginText(filename) || !validCodexPluginText(mimeType) {
		return codexPluginUploadURLRequest{}, errors.New("workspace plugin upload metadata is invalid")
	}
	rawSize, ok := fields["size_bytes"]
	if !ok {
		return codexPluginUploadURLRequest{}, errors.New("missing size_bytes field")
	}
	sizeBytes, err := parseCodexPluginUnsignedInt(rawSize)
	if err != nil || sizeBytes > uint64(codexPluginUploadLimitBytes) {
		return codexPluginUploadURLRequest{}, errors.New("size_bytes must be a non-negative integer no larger than 50 MiB")
	}
	pluginID := ""
	if raw, ok := fields["plugin_id"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &pluginID); err != nil || !validCodexPluginPathSegment(pluginID) {
			return codexPluginUploadURLRequest{}, errors.New("plugin_id is invalid")
		}
	}
	return codexPluginUploadURLRequest{filename: filename, mimeType: mimeType, sizeBytes: int64(sizeBytes), pluginID: strings.TrimSpace(pluginID)}, nil
}

func requiredCodexPluginString(fields map[string]json.RawMessage, name string) (string, error) {
	raw, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("missing %s field", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return value, nil
}

func parseCodexPluginUnsignedInt(raw json.RawMessage) (uint64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, ".eE+-") || value == "null" {
		return 0, errors.New("not an unsigned integer")
	}
	return strconv.ParseUint(value, 10, 64)
}

func validCodexPluginText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

type codexPluginUploadURLResponse struct {
	FileID    string `json:"file_id"`
	UploadURL string `json:"upload_url"`
	ETag      string `json:"etag"`
}

// validateCodexPluginFinalizeResponse enforces the response contract used by
// the official workspace-share client.  Both create and update return a JSON
// object containing the resulting remote plugin_id; an HTTP 2xx alone is not
// sufficient to prove that the provider completed the operation.  In
// particular, do not consume the upload lease when a proxy/provider returns an
// empty or malformed success body: the caller must be able to retry (and the
// failure must remain visible as a structured upstream-response error).
func validateCodexPluginFinalizeResponse(body []byte, expectedPluginID string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return errors.New("provider workspace plugin finalize response is not a JSON object")
	}
	raw, ok := fields["plugin_id"]
	if !ok {
		return errors.New("provider workspace plugin finalize response is missing plugin_id")
	}
	var pluginID string
	if err := json.Unmarshal(raw, &pluginID); err != nil {
		return errors.New("provider workspace plugin finalize response has an invalid plugin_id")
	}
	pluginID = strings.TrimSpace(pluginID)
	if !validCodexPluginPathSegment(pluginID) {
		return errors.New("provider workspace plugin finalize response has an invalid plugin_id")
	}
	if expectedPluginID = strings.TrimSpace(expectedPluginID); expectedPluginID != "" && pluginID != expectedPluginID {
		return fmt.Errorf("provider workspace plugin finalize response plugin_id %q does not match requested plugin %q", pluginID, expectedPluginID)
	}
	return nil
}

func (p *Pipeline) rewriteCodexPluginUploadURLResponse(c *gin.Context, keyInfo *auth.APIKeyInfo, account *accountreg.Snapshot, request codexPluginUploadURLRequest, result *attemptResult) error {
	if c == nil || keyInfo == nil || account == nil || result == nil {
		return errors.New("workspace plugin upload response context is incomplete")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.body, &fields); err != nil || fields == nil {
		return errors.New("provider workspace plugin upload response is not a JSON object")
	}
	var response codexPluginUploadURLResponse
	if err := json.Unmarshal(result.body, &response); err != nil {
		return errors.New("provider workspace plugin upload response is invalid")
	}
	response.FileID = strings.TrimSpace(response.FileID)
	if !validCodexFileID(response.FileID) {
		return errors.New("provider workspace plugin upload response has an invalid file_id")
	}
	response.UploadURL = strings.TrimSpace(response.UploadURL)
	uploadURL, err := validateCodexBlobUploadURL(response.UploadURL)
	if err != nil {
		return err
	}
	response.ETag = strings.TrimSpace(response.ETag)
	if response.ETag == "" || len(response.ETag) > 4096 || !validCodexPluginText(response.ETag) {
		return errors.New("provider workspace plugin upload response has an invalid etag")
	}
	token, err := p.pluginUploadStore().issue(codexPluginUploadLease{
		fileID: response.FileID, etag: response.ETag, pluginID: request.pluginID,
		uploadURL: uploadURL, expectedSize: request.sizeBytes,
		accountID: account.ID, apiKeyID: keyInfo.KeyID, userID: keyInfo.UserID,
		groupID: keyInfo.GroupID, proxyURL: account.ProxyURL,
	})
	if err != nil {
		return err
	}
	localURL, err := codexLocalPluginUploadURL(c, token)
	if err != nil {
		p.pluginUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, response.FileID)
		return err
	}
	encoded, err := json.Marshal(localURL)
	if err != nil {
		p.pluginUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, response.FileID)
		return err
	}
	fields["upload_url"] = encoded
	rewritten, err := json.Marshal(fields)
	if err != nil {
		p.pluginUploadStore().consume(keyInfo.KeyID, keyInfo.GroupID, response.FileID)
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

func codexLocalPluginUploadURL(c *gin.Context, token string) (string, error) {
	if c == nil || c.Request == nil || token == "" {
		return "", errors.New("cannot build local workspace plugin upload URL")
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
		return "", errors.New("cannot build local workspace plugin upload URL without a valid host")
	}
	return scheme + "://" + host + codexPluginUploadPathPrefix + url.PathEscape(token), nil
}

// HandleCodexPluginUpload is intentionally unauthenticated. The official
// client omits the AirGate API key when PUT-ing the provider's signed blob;
// the high-entropy single-use token is the only credential accepted here.
func (p *Pipeline) HandleCodexPluginUpload(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	setEntryProtocol(c, registry.ProtocolOpenAI)
	if !strings.EqualFold(c.Request.Method, http.MethodPut) {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_upload_method", "workspace plugin upload only supports PUT")
		return
	}
	token := strings.TrimSpace(c.Param("token"))
	if token == "" || strings.ContainsAny(token, "/\\") {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_upload_token", "workspace plugin upload token is invalid or expired")
		return
	}
	lease, ok := p.pluginUploadStore().beginUpload(token)
	if !ok {
		writeError(c, http.StatusNotFound, "invalid_request_error", "invalid_upload_token", "workspace plugin upload token is invalid, used, or expired")
		return
	}
	success := false
	defer func() { p.pluginUploadStore().finishUpload(token, success) }()
	if lease.expectedSize < 0 || lease.expectedSize > codexPluginUploadLimitBytes {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_file_size", "workspace plugin bundle size is invalid")
		return
	}
	if c.Request.ContentLength < 0 {
		writeError(c, http.StatusLengthRequired, "invalid_request_error", "content_length_required", "workspace plugin upload requires Content-Length")
		return
	}
	if c.Request.ContentLength != lease.expectedSize {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "content_length_mismatch", "Content-Length does not match the upload-url request")
		return
	}
	clientRequestID := strings.TrimSpace(c.GetHeader("X-Ms-Client-Request-Id"))
	if clientRequestID == "" {
		clientRequestID = uuid.NewString()
	} else if _, err := uuid.Parse(clientRequestID); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_client_request_id", "x-ms-client-request-id must be a UUID")
		return
	}
	tempFile, err := os.CreateTemp("", "airgate-codex-plugin-upload-*")
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "upload_staging_failed", "unable to stage workspace plugin upload")
		return
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	readLimit := lease.expectedSize + 1
	if readLimit < lease.expectedSize || readLimit > codexPluginUploadLimitBytes+1 {
		readLimit = codexPluginUploadLimitBytes + 1
	}
	body := c.Request.Body
	if body == nil {
		body = http.NoBody
	}
	actualSize, err := io.Copy(tempFile, io.LimitReader(body, readLimit))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "upload_read_failed", "unable to read workspace plugin upload")
		return
	}
	if actualSize != lease.expectedSize {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "upload_size_mismatch", "actual upload size does not match the upload-url request")
		return
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "upload_staging_failed", "unable to prepare workspace plugin upload")
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPut, lease.uploadURL, tempFile)
	if err != nil {
		writeError(c, http.StatusBadGateway, "upstream_error", "upload_request_failed", "unable to create workspace plugin upload request")
		return
	}
	upstreamRequest.ContentLength = lease.expectedSize
	copyCodexBlobUploadHeaders(upstreamRequest.Header, c.Request.Header)
	if upstreamRequest.Header.Get("Content-Type") == "" {
		upstreamRequest.Header.Set("Content-Type", "application/gzip")
	}
	upstreamRequest.Header.Set("X-Ms-Blob-Type", "BlockBlob")
	upstreamRequest.Header.Set("X-Ms-Client-Request-Id", clientRequestID)
	transport, err := p.accountAuditTransport(lease.proxyURL)
	if err != nil || transport == nil {
		writeError(c, http.StatusBadGateway, "upstream_error", "invalid_proxy", "workspace plugin upload proxy configuration is invalid")
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
		writeError(c, http.StatusBadGateway, "upstream_error", "blob_upload_failed", "workspace plugin upload failed")
		return
	}
	defer func() { _ = response.Body.Close() }()
	copySafeCodexBlobResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	c.Writer.WriteHeaderNow()
	if err := drainCodexBlobResponseBody(response.Body); err != nil {
		return
	}
	// The official Codex workspace-share client accepts only the two statuses
	// documented by the blob PUT contract (200 OK or 201 Created).  Keep a
	// 202/204 response visible to the caller but do not mark the lease complete;
	// otherwise a non-conforming storage response could allow finalization of a
	// bundle that was never durably accepted.
	success = response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated
}

func codexPluginWorkspaceRemoteID(providerPath string) (string, bool) {
	path := strings.TrimRight(strings.TrimSpace(providerPath), "/")
	const prefix = "/public/plugins/workspace/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || !validCodexPluginPathSegment(decoded) {
		return "", false
	}
	return decoded, true
}

func codexPluginUploadFinalizeFields(body []byte) (fileID, etag string, err error) {
	var fields map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(body, &fields); unmarshalErr != nil || fields == nil {
		return "", "", errors.New("workspace plugin finalize request must be a JSON object")
	}
	fileID, err = requiredCodexPluginString(fields, "file_id")
	if err != nil || !validCodexFileID(fileID) {
		return "", "", errors.New("file_id is invalid")
	}
	etag, err = requiredCodexPluginString(fields, "etag")
	if err != nil || len(etag) > 4096 || !validCodexPluginText(etag) {
		return "", "", errors.New("etag is invalid")
	}
	return strings.TrimSpace(fileID), strings.TrimSpace(etag), nil
}

func (p *Pipeline) codexPluginFinalizeOptions(c *gin.Context, keyInfo *auth.APIKeyInfo, route codexBackendClientRoute, providerPath string, body []byte) (forwardOptions, error) {
	fileID, etag, err := codexPluginUploadFinalizeFields(body)
	if err != nil {
		return forwardOptions{}, err
	}
	lease, ok := p.pluginUploadStore().uploadedFile(keyInfo.KeyID, keyInfo.GroupID, fileID)
	if !ok {
		return forwardOptions{}, errors.New("workspace plugin upload is not complete, expired, or already finalized")
	}
	if lease.etag != etag {
		return forwardOptions{}, errors.New("workspace plugin etag does not match the upload-url response")
	}
	remoteID, hasRemoteID := codexPluginWorkspaceRemoteID(providerPath)
	if route.Endpoint == adaptor.EndpointCodexPluginsWorkspaceUpdate {
		if !hasRemoteID || lease.pluginID == "" || lease.pluginID != remoteID {
			return forwardOptions{}, errors.New("workspace plugin id does not match the upload-url request")
		}
	} else if hasRemoteID || lease.pluginID != "" {
		return forwardOptions{}, errors.New("workspace plugin upload was issued for a different finalize path")
	}
	keyID, groupID := keyInfo.KeyID, keyInfo.GroupID
	expectedPluginID := ""
	if route.Endpoint == adaptor.EndpointCodexPluginsWorkspaceUpdate {
		expectedPluginID = remoteID
	}
	return forwardOptions{
		nativeCodexAccountID: lease.accountID,
		responseTransform: func(_ *accountreg.Snapshot, result *attemptResult) error {
			if result == nil {
				return errors.New("provider workspace plugin finalize response is missing")
			}
			if err := validateCodexPluginFinalizeResponse(result.body, expectedPluginID); err != nil {
				return err
			}
			p.pluginUploadStore().consume(keyID, groupID, fileID)
			return nil
		},
	}, nil
}
