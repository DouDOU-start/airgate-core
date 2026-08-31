package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/pluginruntime/protocol"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	providertransport "github.com/DouDOU-start/airgate-core/internal/relay/transport"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

type codexFileRoundTripper func(*http.Request) (*http.Response, error)

func (f codexFileRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type codexFilesAccountLoader struct {
	accounts []accountreg.Snapshot
}

func (l codexFilesAccountLoader) LoadAllForAccountRegistry(context.Context) ([]accountreg.Snapshot, error) {
	return l.accounts, nil
}

// codexFilesIntegrationManager returns distinct native provider responses for
// the two steps of the Files lifecycle and records every wire request emitted
// by the Core-owned plugin transport.
type codexFilesIntegrationManager struct {
	mu       sync.Mutex
	requests []protocol.CodexExecuteRequest
}

func (m *codexFilesIntegrationManager) CodexTransportMode() string {
	return string(providertransport.CodexModeNativeOnly)
}

func (m *codexFilesIntegrationManager) ExecuteCodex(_ context.Context, request protocol.CodexExecuteRequest, emit func(protocol.CodexExecuteEvent) error) error {
	m.mu.Lock()
	m.requests = append(m.requests, request)
	m.mu.Unlock()

	var body string
	switch request.Endpoint {
	case adaptor.EndpointFilesCreate:
		body = `{"file_id":"file-integration","upload_url":"https://storage.example/blob?sig=upload-secret","pdf_c2pa_reservation":true,"metadata":{"keep":true}}`
	case adaptor.EndpointFilesFinalize:
		body = `{"status":"success","download_url":"https://storage.example/download?sig=download-secret","file_id":"file-integration"}`
	default:
		return nil
	}
	if err := emit(protocol.CodexExecuteEvent{
		Type:       protocol.CodexEventResponseHeaders,
		StatusCode: http.StatusOK,
		Header:     map[string][]string{"Content-Type": {"application/json"}},
	}); err != nil {
		return err
	}
	if err := emit(protocol.CodexExecuteEvent{Type: protocol.CodexEventData, Data: []byte(body)}); err != nil {
		return err
	}
	return emit(protocol.CodexExecuteEvent{Type: protocol.CodexEventCompleted})
}

func (m *codexFilesIntegrationManager) snapshotRequests() []protocol.CodexExecuteRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]protocol.CodexExecuteRequest(nil), m.requests...)
}

func TestCodexFilesCreatePutFinalizeUsesNativeAccountLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := accountreg.New(codexFilesAccountLoader{accounts: []accountreg.Snapshot{{
		ID: 91, Name: "codex-oauth", Platform: "codex", Type: "oauth", State: accountreg.StateActive,
		Credentials: map[string]string{"access_token": "account-token", "chatgpt_account_id": "acct-91"},
		GroupIDs:    map[int]struct{}{7: {}},
	}}}, nil)
	if err := accounts.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager := &codexFilesIntegrationManager{}
	pipe := New(Options{
		Accounts:          accounts,
		ProviderTransport: providertransport.NewCodexPluginTransport(manager),
		Concurrency:       scheduler.NewConcurrencyManager(nil),
		RPM:               scheduler.NewRPMCounter(nil),
	})
	var uploadURL string
	var uploadBody string
	var uploadAuth string
	var uploadCookie string
	pipe.accountDirectTransport = codexFileRoundTripper(func(request *http.Request) (*http.Response, error) {
		uploadURL = request.URL.String()
		uploadAuth = request.Header.Get("Authorization")
		uploadCookie = request.Header.Get("Cookie")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		uploadBody = string(body)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})

	engine := gin.New()
	injectKey := func(c *gin.Context) { c.Set(middleware.CtxKeyKeyInfo, testKeyInfo()) }
	engine.POST("/v1/files", injectKey, pipe.HandleCodexFilesCreate)
	engine.PUT(codexFileUploadPathPrefix+":token", pipe.HandleCodexFileUpload)
	engine.POST("/v1/files/:file_id/uploaded", injectKey, pipe.HandleCodexFilesFinalize)

	createBody := `{"file_name":"report.pdf","file_size":3,"use_case":"codex"}`
	createRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/files", strings.NewReader(createBody))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Authorization", "Bearer caller-secret")
	createRequest.Header.Set("Originator", "codex_cli_rs")
	createResponse := httptest.NewRecorder()
	engine.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	if strings.Contains(createResponse.Body.String(), "storage.example") || strings.Contains(createResponse.Body.String(), "upload-secret") {
		t.Fatalf("provider upload URL leaked in create response: %s", createResponse.Body.String())
	}
	var created struct {
		FileID             string `json:"file_id"`
		UploadURL          string `json:"upload_url"`
		PDFC2PAReservation bool   `json:"pdf_c2pa_reservation"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("create response is not JSON: %v", err)
	}
	if created.FileID != "file-integration" || !created.PDFC2PAReservation {
		t.Fatalf("create response = %+v", created)
	}
	parsedUploadURL, err := url.Parse(created.UploadURL)
	if err != nil || parsedUploadURL.Path == "" || !strings.HasPrefix(parsedUploadURL.Path, codexFileUploadPathPrefix) {
		t.Fatalf("upload URL = %q, want local opaque URL", created.UploadURL)
	}
	uploadURL = ""
	uploadRequest := httptest.NewRequest(http.MethodPut, "http://gateway.test"+parsedUploadURL.EscapedPath(), strings.NewReader("abc"))
	uploadRequest.Header.Set("Content-Length", "3")
	uploadRequest.Header.Set("Content-Type", "application/octet-stream")
	uploadRequest.Header.Set("Authorization", "Bearer caller-secret")
	uploadRequest.Header.Set("Cookie", "session=caller-secret")
	uploadRequest.Header.Set("X-Ms-Blob-Type", "BlockBlob")
	uploadResponse := httptest.NewRecorder()
	engine.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if uploadURL != "https://storage.example/blob?sig=upload-secret" || uploadBody != "abc" {
		t.Fatalf("provider upload request = url:%q body:%q", uploadURL, uploadBody)
	}
	if uploadAuth != "" || uploadCookie != "" {
		t.Fatalf("credentials leaked to blob provider: authorization=%q cookie=%q", uploadAuth, uploadCookie)
	}

	finalizeBody := `{"client_note":"keep-this"}`
	finalizeRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/files/file-integration/uploaded", strings.NewReader(finalizeBody))
	finalizeRequest.Header.Set("Content-Type", "application/json")
	finalizeRequest.Header.Set("Authorization", "Bearer caller-secret")
	finalizeRequest.Header.Set("Originator", "codex_cli_rs")
	finalizeResponse := httptest.NewRecorder()
	engine.ServeHTTP(finalizeResponse, finalizeRequest)
	if finalizeResponse.Code != http.StatusOK {
		t.Fatalf("finalize status = %d, body = %s", finalizeResponse.Code, finalizeResponse.Body.String())
	}
	var finalized map[string]any
	if err := json.Unmarshal(finalizeResponse.Body.Bytes(), &finalized); err != nil {
		t.Fatalf("finalize response is not JSON: %v", err)
	}
	if finalized["download_url"] != "https://storage.example/download?sig=download-secret" {
		t.Fatalf("download_url changed: %#v", finalized["download_url"])
	}

	requests := manager.snapshotRequests()
	if len(requests) != 2 {
		t.Fatalf("native provider request count = %d, want 2", len(requests))
	}
	if requests[0].Credential.AccountID != "acct-91" || requests[1].Credential.AccountID != "acct-91" {
		t.Fatalf("native account affinity lost: create=%q finalize=%q", requests[0].Credential.AccountID, requests[1].Credential.AccountID)
	}
	if requests[0].Endpoint != adaptor.EndpointFilesCreate || requests[0].Path != "/files" {
		t.Fatalf("create provider request = endpoint:%q path:%q", requests[0].Endpoint, requests[0].Path)
	}
	if requests[1].Endpoint != adaptor.EndpointFilesFinalize || requests[1].Path != "/files/file-integration/uploaded" {
		t.Fatalf("finalize provider request = endpoint:%q path:%q", requests[1].Endpoint, requests[1].Path)
	}
	var providerFinalize map[string]json.RawMessage
	if err := json.Unmarshal(requests[1].Body, &providerFinalize); err != nil {
		t.Fatalf("provider finalize body is not JSON: %v", err)
	}
	var c2paCreate map[string]any
	if err := json.Unmarshal(providerFinalize["pdf_c2pa_create_request"], &c2paCreate); err != nil {
		t.Fatalf("pdf_c2pa_create_request is not JSON: %v", err)
	}
	if c2paCreate["file_name"] != "report.pdf" || c2paCreate["file_size"] != float64(3) || c2paCreate["use_case"] != "codex" {
		t.Fatalf("PDF C2PA create body = %#v", c2paCreate)
	}
	if _, ok := providerFinalize["client_note"]; !ok {
		t.Fatalf("finalize body lost caller fields: %s", requests[1].Body)
	}

	// A successful finalize consumes the lease and prevents a second account-
	// scoped finalize from replaying the provider operation.
	replayRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/files/file-integration/uploaded", strings.NewReader(finalizeBody))
	replayRequest.Header.Set("Content-Type", "application/json")
	replayRequest.Header.Set("Originator", "codex_cli_rs")
	replayResponse := httptest.NewRecorder()
	engine.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusConflict {
		t.Fatalf("replayed finalize status = %d, want 409", replayResponse.Code)
	}
}

func TestValidCodexFileIDRejectsPathAmbiguity(t *testing.T) {
	for _, fileID := range []string{"", ".", "..", "a/b", `a\\b`, "a%2Fb", "a%2e%2e", "a#b", "a?b", "a\x00b"} {
		if validCodexFileID(fileID) {
			t.Errorf("validCodexFileID(%q) = true, want false", fileID)
		}
	}
	for _, fileID := range []string{"file-123", "file_abc-XYZ", "0"} {
		if !validCodexFileID(fileID) {
			t.Errorf("validCodexFileID(%q) = false, want true", fileID)
		}
	}
}

func TestValidateCodexBlobUploadURLRejectsObviousSSRFHosts(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/blob?sig=x",
		"http://10.0.0.1/blob?sig=x",
		"http://[::1]/blob?sig=x",
		"http://[fe80::1%25eth0]/blob?sig=x",
		"http://localhost/blob?sig=x",
		"http://metadata.internal/blob?sig=x",
		"http://2130706433/blob?sig=x",
		"http://0x7f000001/blob?sig=x",
		"http://0177.0.0.1/blob?sig=x",
		"http://127.1/blob?sig=x",
	} {
		if _, err := validateCodexBlobUploadURL(raw); err == nil {
			t.Errorf("validateCodexBlobUploadURL(%q) accepted an unsafe host", raw)
		}
	}
	if got, err := validateCodexBlobUploadURL("https://storage.example/blob?sig=x"); err != nil || got == "" {
		t.Fatalf("public storage URL rejected: got=%q err=%v", got, err)
	}
}

func TestValidateCodexBlobUploadURLRejectsFragments(t *testing.T) {
	for _, raw := range []string{
		"https://storage.example/blob?sig=x#fragment",
		"https://storage.example/blob?sig=x#",
	} {
		if _, err := validateCodexBlobUploadURL(raw); err == nil {
			t.Errorf("validateCodexBlobUploadURL(%q) accepted a fragment", raw)
		}
	}
	if got, err := validateCodexBlobUploadURL("https://storage.example/blob?sig=x%23y"); err != nil || got == "" {
		t.Fatalf("encoded fragment byte in query was rejected: got=%q err=%v", got, err)
	}
}

func TestValidateCodexBlobUploadURLRejectsDNSPrivateAddress(t *testing.T) {
	originalLookup := lookupCodexBlobHostIPs
	lookupCodexBlobHostIPs = func(host string) ([]net.IP, error) {
		if host == "storage.private.example" {
			return []net.IP{net.ParseIP("192.168.10.20")}, nil
		}
		return nil, errors.New("not found")
	}
	defer func() { lookupCodexBlobHostIPs = originalLookup }()
	if _, err := validateCodexBlobUploadURL("https://storage.private.example/blob?sig=x"); err == nil {
		t.Fatal("DNS name resolving to a private address was accepted")
	}
}

func TestCopySafeCodexBlobResponseHeadersRemovesURLBearingMetadata(t *testing.T) {
	dst := make(http.Header)
	copySafeCodexBlobResponseHeaders(dst, http.Header{
		"ETag":             {`"etag"`},
		"X-Ms-Request-Id":  {"request-id"},
		"Location":         {"https://storage.example/blob?sig=secret"},
		"Content-Location": {"https://storage.example/blob?sig=secret"},
		"Refresh":          {"0;url=https://storage.example/blob?sig=secret"},
		"Link":             {"<https://storage.example/blob?sig=secret>; rel=upload"},
	})
	if got := dst.Get("ETag"); got == "" {
		t.Fatal("ETag was removed from blob response")
	}
	if got := dst.Get("X-Ms-Request-Id"); got == "" {
		t.Fatal("storage request id was removed from blob response")
	}
	for _, name := range []string{"Location", "Content-Location", "Refresh", "Link"} {
		if got := dst.Get(name); got != "" {
			t.Fatalf("URL-bearing header %s leaked as %q", name, got)
		}
	}
}

func TestDrainCodexBlobResponseBodySuppressesSignedURL(t *testing.T) {
	// The helper intentionally drains without returning bytes. This test keeps
	// the contract explicit while also exercising the nil-body path.
	if err := drainCodexBlobResponseBody(strings.NewReader("https://storage.example/blob?sig=secret")); err != nil {
		t.Fatal(err)
	}
	if err := drainCodexBlobResponseBody(nil); err != nil {
		t.Fatal(err)
	}
}

func TestCodexFileUploadRequiresPUT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newCodexFileUploadStore()
	token, err := store.issue(codexFileUploadLease{fileID: "file-1", expectedSize: 0, uploadURL: "https://storage.example/blob", apiKeyID: 1, groupID: 2})
	if err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{codexFileUploads: store}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/_airgate/codex/files/upload/"+token, nil)
	c.Params = gin.Params{{Key: "token", Value: token}}
	p.HandleCodexFileUpload(c)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("GET upload status = %d, want 404", recorder.Code)
	}
	if _, ok := store.beginUpload(token); !ok {
		t.Fatal("method rejection consumed upload token")
	}
}

func TestCodexFileUploadFiltersCredentialHeadersAndUsesBoundURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newCodexFileUploadStore()
	providerURL := "https://storage.example/blob?sig=opaque"
	token, err := store.issue(codexFileUploadLease{
		fileID: "file-1", expectedSize: 3, uploadURL: providerURL, apiKeyID: 1, groupID: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var seenURL string
	var seenAuth, seenCookie, seenCopySource string
	var seenBody string
	p := &Pipeline{
		codexFileUploads: store,
		accountDirectTransport: codexFileRoundTripper(func(req *http.Request) (*http.Response, error) {
			seenURL = req.URL.String()
			seenAuth = req.Header.Get("Authorization")
			seenCookie = req.Header.Get("Cookie")
			seenCopySource = req.Header.Get("X-Ms-Copy-Source")
			body, _ := io.ReadAll(req.Body)
			seenBody = string(body)
			return &http.Response{
				StatusCode: http.StatusCreated,
				Status:     "201 Created",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPut, "/_airgate/codex/files/upload/"+token, strings.NewReader("abc"))
	req.Header.Set("Authorization", "Bearer caller-secret")
	req.Header.Set("Cookie", "session=caller-secret")
	req.Header.Set("X-Ms-Copy-Source", "https://attacker.example/source")
	req.Header.Set("X-Ms-Blob-Type", "BlockBlob")
	req.Header.Set("X-Ms-Client-Request-Id", "550e8400-e29b-41d4-a716-446655440000")
	req.Header.Set("Content-Type", "application/octet-stream")
	c.Request = req
	c.Params = gin.Params{{Key: "token", Value: token}}
	p.HandleCodexFileUpload(c)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", recorder.Code)
	}
	if seenURL != providerURL {
		t.Fatalf("upstream URL = %q, want bound URL %q", seenURL, providerURL)
	}
	if seenAuth != "" || seenCookie != "" {
		t.Fatalf("credential header leaked: authorization=%q cookie=%q", seenAuth, seenCookie)
	}
	if seenCopySource != "" {
		t.Fatalf("dangerous x-ms-copy-source leaked: %q", seenCopySource)
	}
	if seenBody != "abc" {
		t.Fatalf("upstream body = %q, want abc", seenBody)
	}
	if _, ok := store.beginUpload(token); ok {
		t.Fatal("successful upload token can be replayed")
	}
}

func TestCodexFileUploadContentLengthMismatchCanRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newCodexFileUploadStore()
	token, err := store.issue(codexFileUploadLease{fileID: "file-1", expectedSize: 3, uploadURL: "https://storage.example/blob", apiKeyID: 1, groupID: 2})
	if err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{codexFileUploads: store, accountDirectTransport: codexFileRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPut, "/_airgate/codex/files/upload/"+token, strings.NewReader("ab"))
	req.ContentLength = 2
	c.Request = req
	c.Params = gin.Params{{Key: "token", Value: token}}
	p.HandleCodexFileUpload(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("mismatched upload status = %d, want 400", recorder.Code)
	}
	if _, ok := store.beginUpload(token); !ok {
		t.Fatal("failed upload did not return token to retryable state")
	}
}

func TestCodexFileUploadRejectsOverlongBodyBeforeProviderCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newCodexFileUploadStore()
	token, err := store.issue(codexFileUploadLease{fileID: "file-1", expectedSize: 3, uploadURL: "https://storage.example/blob", apiKeyID: 1, groupID: 2})
	if err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	p := &Pipeline{
		codexFileUploads: store,
		accountDirectTransport: codexFileRoundTripper(func(req *http.Request) (*http.Response, error) {
			providerCalls++
			return &http.Response{StatusCode: http.StatusCreated, Status: "201 Created", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPut, "/_airgate/codex/files/upload/"+token, strings.NewReader("abcd"))
	// Deliberately lie in the request metadata.  httptest keeps the complete
	// reader behind Body, which exercises the overlong-body check independently
	// of net/http's server-side Content-Length framing.
	req.ContentLength = 3
	c.Request = req
	c.Params = gin.Params{{Key: "token", Value: token}}
	p.HandleCodexFileUpload(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("overlong upload status = %d, want 400", recorder.Code)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls = %d, want 0 for rejected overlong body", providerCalls)
	}
	if _, ok := store.beginUpload(token); !ok {
		t.Fatal("failed upload did not return token to retryable state")
	}
}

func TestValidCodexLocalUploadHostRejectsForwardedHostInjection(t *testing.T) {
	valid := []string{"gateway.example", "gateway.example:8443", "[::1]", "[2001:db8::1]:9443"}
	for _, host := range valid {
		if !validCodexLocalUploadHost(host) {
			t.Errorf("valid host %q rejected", host)
		}
	}
	invalid := []string{
		"", "attacker.example/path", "attacker.example?x=1", "attacker.example#frag",
		"user:pass@attacker.example", "attacker.example\r\nX-Leak: yes", "[::1",
	}
	for _, host := range invalid {
		if validCodexLocalUploadHost(host) {
			t.Errorf("injected host %q accepted", host)
		}
	}
}
