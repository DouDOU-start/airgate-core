package plugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type fakeS3Object struct {
	data        []byte
	contentType string
}

type fakeS3Server struct {
	t       *testing.T
	bucket  string
	mu      sync.Mutex
	objects map[string]fakeS3Object
	server  *httptest.Server
}

func newFakeS3Server(t *testing.T, bucket string) *fakeS3Server {
	f := &fakeS3Server{
		t:       t,
		bucket:  bucket,
		objects: make(map[string]fakeS3Object),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeS3Server) client() (*minio.Client, error) {
	u, err := url.Parse(f.server.URL)
	if err != nil {
		return nil, err
	}
	return minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4("access", "secret", ""),
		Secure:       false,
		BucketLookup: minio.BucketLookupPath,
	})
}

func (f *fakeS3Server) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

func (f *fakeS3Server) handle(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if cleanPath == f.bucket {
		if r.Method == http.MethodGet && r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`))
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 || parts[0] != f.bucket {
		http.NotFound(w, r)
		return
	}
	key := parts[1]

	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ct := r.Header.Get("Content-Type")
		f.mu.Lock()
		f.objects[key] = fakeS3Object{data: data, contentType: ct}
		f.mu.Unlock()
		w.Header().Set("ETag", `"fake-etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		f.mu.Lock()
		obj, ok := f.objects[key]
		f.mu.Unlock()
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "not found")
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.data)))
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		w.Header().Set("ETag", `"fake-etag"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		f.mu.Lock()
		obj, ok := f.objects[key]
		f.mu.Unlock()
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "not found")
			return
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		w.Header().Set("ETag", `"fake-etag"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.data)
	case http.MethodDelete:
		f.mu.Lock()
		delete(f.objects, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeS3Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, message)
}

func newMigratingTestAssetStorage(t *testing.T, bucket string) (*AssetStorage, *fakeS3Server) {
	t.Helper()
	fake := newFakeS3Server(t, bucket)
	client, err := fake.client()
	if err != nil {
		t.Fatalf("create minio client: %v", err)
	}
	storage := &AssetStorage{
		client:        client,
		bucket:        bucket,
		publicBaseURL: "https://cdn.example.com/assets",
		localDir:      t.TempDir(),
		presignTTL:    time.Hour,
		useS3:         true,
	}
	return storage, fake
}

func waitForAsset(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestAssetStorageGetBytesFallsBackAndRepairsS3(t *testing.T) {
	ctx := context.Background()
	storage, fake := newMigratingTestAssetStorage(t, "assets")
	objectKey := path.Join("generated", "42", "202605", "asset.png")
	localPath, err := storage.LocalPath(objectKey)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	mustWriteFile(t, localPath, []byte("local-bytes"))

	url, err := storage.PublicURL(ctx, objectKey)
	if err != nil {
		t.Fatalf("public url before repair: %v", err)
	}
	if !strings.HasPrefix(url, "/assets-runtime/") {
		t.Fatalf("expected local runtime URL before repair, got %s", url)
	}

	data, contentType, err := storage.GetBytes(ctx, objectKey)
	if err != nil {
		t.Fatalf("get bytes: %v", err)
	}
	if string(data) != "local-bytes" {
		t.Fatalf("unexpected bytes: %q", data)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}

	waitForAsset(t, func() bool { return fake.has(objectKey) })

	url, err = storage.PublicURL(ctx, objectKey)
	if err != nil {
		t.Fatalf("public url after repair: %v", err)
	}
	if got, want := url, "https://cdn.example.com/assets/"+objectKey; got != want {
		t.Fatalf("public url = %q, want %q", got, want)
	}
}

func TestAssetStorageSyncLocalToS3(t *testing.T) {
	ctx := context.Background()
	storage, fake := newMigratingTestAssetStorage(t, "assets")
	objectKey := path.Join("generated", "42", "202605", "sync.png")
	localPath, err := storage.LocalPath(objectKey)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	mustWriteFile(t, localPath, []byte("sync-bytes"))
	mustWriteFile(t, localPath+".w256.jpg", []byte("thumb"))

	result, err := storage.SyncLocalToS3(ctx)
	if err != nil {
		t.Fatalf("sync local to s3: %v", err)
	}
	if result.Migrated != 1 {
		t.Fatalf("migrated = %d, want 1", result.Migrated)
	}
	if result.Skipped < 1 {
		t.Fatalf("expected skipped thumbs, got %+v", result)
	}
	if !fake.has(objectKey) {
		t.Fatalf("remote object missing after migration")
	}
}

func TestAssetStorageDeleteRemovesS3AndLocal(t *testing.T) {
	ctx := context.Background()
	storage, fake := newMigratingTestAssetStorage(t, "assets")
	objectKey := path.Join("generated", "42", "202605", "delete.png")
	localPath, err := storage.LocalPath(objectKey)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	mustWriteFile(t, localPath, []byte("delete-bytes"))
	if _, err := storage.SyncLocalToS3(ctx); err != nil {
		t.Fatalf("pre-sync: %v", err)
	}
	if !fake.has(objectKey) {
		t.Fatalf("remote object missing before delete")
	}

	if err := storage.Delete(ctx, objectKey); err != nil {
		t.Fatalf("delete asset: %v", err)
	}
	if fake.has(objectKey) {
		t.Fatalf("remote object still exists after delete")
	}
	assertPathMissing(t, localPath)
}
