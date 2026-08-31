package pipeline

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
)

func TestParseCodexPluginUploadURLRequest(t *testing.T) {
	request, err := parseCodexPluginUploadURLRequest([]byte(`{"filename":"demo.tar.gz","mime_type":"application/gzip","size_bytes":42,"plugin_id":"plugins~demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.filename != "demo.tar.gz" || request.mimeType != "application/gzip" || request.sizeBytes != 42 || request.pluginID != "plugins~demo" {
		t.Fatalf("parsed request = %+v", request)
	}
	for _, body := range []string{
		`{"filename":"demo","mime_type":"application/gzip","size_bytes":-1}`,
		`{"filename":"demo","mime_type":"application/gzip","size_bytes":1.5}`,
		`{"filename":"demo","mime_type":"application/gzip","size_bytes":1,"plugin_id":"a/b"}`,
		`{"filename":"demo","size_bytes":1}`,
	} {
		if _, err := parseCodexPluginUploadURLRequest([]byte(body)); err == nil {
			t.Fatalf("invalid upload-url body accepted: %s", body)
		}
	}
}

func TestCodexPluginUploadStoreIsSingleUseAfterFinalize(t *testing.T) {
	store := newCodexPluginUploadStore()
	token, err := store.issue(codexPluginUploadLease{fileID: "file-1", etag: "\"etag\"", apiKeyID: 1, groupID: 2, expectedSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	lease, ok := store.beginUpload(token)
	if !ok || lease.fileID != "file-1" {
		t.Fatalf("beginUpload = %+v, %v", lease, ok)
	}
	store.finishUpload(token, true)
	if _, ok := store.uploadedFile(1, 2, "file-1"); !ok {
		t.Fatal("completed upload lease not found")
	}
	store.consume(1, 2, "file-1")
	if _, ok := store.uploadedFile(1, 2, "file-1"); ok {
		t.Fatal("consumed upload lease still available")
	}
	if _, ok := store.beginUpload(token); ok {
		t.Fatal("consumed upload token can be replayed")
	}
}

func TestCodexPluginUploadURLResponseIsOpaque(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/public/plugins/workspace/upload-url", nil)
	pipe := &Pipeline{codexPluginUploads: newCodexPluginUploadStore()}
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 8, GroupID: 9}
	account := &accountreg.Snapshot{ID: 11, ProxyURL: ""}
	result := &attemptResult{body: []byte(`{"file_id":"file-1","upload_url":"https://blob.example/upload?sig=secret","etag":"\"etag\"","extra":true}`), headers: make(http.Header)}
	_, err := json.Marshal(result.body)
	if err != nil {
		t.Fatal(err)
	}
	err = pipe.rewriteCodexPluginUploadURLResponse(c, keyInfo, account, codexPluginUploadURLRequest{filename: "x.tar.gz", mimeType: "application/gzip", sizeBytes: 3}, result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result.body, []byte("blob.example")) || bytes.Contains(result.body, []byte("sig=secret")) {
		t.Fatalf("provider signed URL leaked: %s", result.body)
	}
	var response map[string]any
	if err := json.Unmarshal(result.body, &response); err != nil {
		t.Fatal(err)
	}
	localURL, _ := response["upload_url"].(string)
	if localURL == "" || !bytes.Contains([]byte(localURL), []byte(codexPluginUploadPathPrefix)) {
		t.Fatalf("upload URL was not rewritten: %q", localURL)
	}
	if _, ok := pipe.pluginUploadStore().uploadedFile(7, 9, "file-1"); ok {
		t.Fatal("upload lease should not be complete before PUT")
	}
	// Ensure an issued lease can still be expired and swept without leaking.
	pipe.pluginUploadStore().mu.Lock()
	for _, lease := range pipe.pluginUploadStore().byToken {
		lease.expiresAt = time.Now().Add(-time.Second)
	}
	pipe.pluginUploadStore().mu.Unlock()
	if _, ok := pipe.pluginUploadStore().uploadedFile(7, 9, "file-1"); ok {
		t.Fatal("expired upload lease still available")
	}
}

func TestCodexPluginFinalizeResponseIsValidatedBeforeLeaseConsume(t *testing.T) {
	for _, test := range []struct {
		name        string
		body        string
		expectedID  string
		wantErr     bool
		wantConsume bool
	}{
		{name: "missing plugin id", body: `{"ok":true}`, wantErr: true},
		{name: "invalid plugin id", body: `{"plugin_id":"a/b"}`, wantErr: true},
		{name: "mismatched update id", body: `{"plugin_id":"plugins-other"}`, expectedID: "plugins-current", wantErr: true},
		{name: "create", body: `{"plugin_id":"plugins-created"}`, wantConsume: true},
		{name: "matching update", body: `{"plugin_id":"plugins-current"}`, expectedID: "plugins-current", wantConsume: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.wantConsume {
				if err := validateCodexPluginFinalizeResponse([]byte(test.body), test.expectedID); err != nil {
					t.Fatalf("valid finalize response rejected: %v", err)
				}
				return
			}
			if err := validateCodexPluginFinalizeResponse([]byte(test.body), test.expectedID); (err != nil) != test.wantErr {
				t.Fatalf("validation error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestCodexPluginFinalizeTransformKeepsLeaseOnInvalidResponse(t *testing.T) {
	pipe := &Pipeline{codexPluginUploads: newCodexPluginUploadStore()}
	keyInfo := &auth.APIKeyInfo{KeyID: 7, UserID: 8, GroupID: 9}
	_, err := pipe.pluginUploadStore().issue(codexPluginUploadLease{
		fileID: "file-finalize", etag: `"etag"`, pluginID: "plugins-current",
		apiKeyID: keyInfo.KeyID, userID: keyInfo.UserID, groupID: keyInfo.GroupID,
		accountID: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mark the upload complete so the finalize options can be assembled.
	pipe.pluginUploadStore().mu.Lock()
	for _, lease := range pipe.pluginUploadStore().byToken {
		lease.state = codexPluginUploadComplete
	}
	pipe.pluginUploadStore().mu.Unlock()

	options, err := pipe.codexPluginFinalizeOptions(nil, keyInfo, codexBackendClientRoute{
		Endpoint: adaptor.EndpointCodexPluginsWorkspaceUpdate,
		Method:   http.MethodPost,
	}, "/public/plugins/workspace/plugins-current", []byte(`{"file_id":"file-finalize","etag":"\"etag\""}`))
	if err != nil {
		t.Fatal(err)
	}
	if options.responseTransform == nil {
		t.Fatal("finalize response transform is nil")
	}
	if err := options.responseTransform(nil, &attemptResult{body: []byte(`{"ok":true}`)}); err == nil {
		t.Fatal("invalid finalize response unexpectedly accepted")
	}
	if _, ok := pipe.pluginUploadStore().uploadedFile(keyInfo.KeyID, keyInfo.GroupID, "file-finalize"); !ok {
		t.Fatal("invalid finalize response consumed upload lease")
	}
	if err := options.responseTransform(nil, &attemptResult{body: []byte(`{"plugin_id":"plugins-current"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := pipe.pluginUploadStore().uploadedFile(keyInfo.KeyID, keyInfo.GroupID, "file-finalize"); ok {
		t.Fatal("valid finalize response did not consume upload lease")
	}
}
