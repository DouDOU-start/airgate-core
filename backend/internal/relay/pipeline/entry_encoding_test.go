package pipeline

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

func TestDecodeRequestBodySupportedCodings(t *testing.T) {
	payload := []byte(`{"model":"gpt-5","input":"hello"}`)
	gzipBody := gzipEncode(t, payload)
	zstdBody := zstdEncode(t, payload)
	stackedBody := zstdEncode(t, gzipBody)

	tests := []struct {
		name     string
		body     []byte
		encoding string
		decoded  bool
	}{
		{name: "identity header omitted", body: payload},
		{name: "identity explicit", body: payload, encoding: " identity "},
		{name: "gzip", body: gzipBody, encoding: "gzip", decoded: true},
		{name: "x-gzip mixed case", body: gzipBody, encoding: " X-GZip ", decoded: true},
		{name: "zstd", body: zstdBody, encoding: "zstd", decoded: true},
		{name: "stacked", body: stackedBody, encoding: "gzip, zstd", decoded: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, decoded, err := decodeRequestBody(test.body, test.encoding, 1<<20)
			if err != nil {
				t.Fatalf("decodeRequestBody: %v", err)
			}
			if decoded != test.decoded {
				t.Fatalf("decoded = %v, want %v", decoded, test.decoded)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("body = %q, want %q", got, payload)
			}
		})
	}
}

func TestDecodeRequestBodyRejectsInvalidAndOversized(t *testing.T) {
	if _, _, err := decodeRequestBody([]byte("body"), "br", 1024); !errors.Is(err, errUnsupportedContentEncoding) {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, _, err := decodeRequestBody([]byte("not gzip"), "gzip", 1024); !errors.Is(err, errInvalidContentEncoding) {
		t.Fatalf("invalid gzip error = %v", err)
	}
	if _, _, err := decodeRequestBody([]byte("body"), "identity,identity,identity,identity,identity,identity,identity,identity,identity", 1024); !errors.Is(err, errInvalidContentEncoding) {
		t.Fatalf("too many layers error = %v", err)
	}
	compressed := zstdEncode(t, bytes.Repeat([]byte("a"), 4096))
	if _, _, err := decodeRequestBody(compressed, "zstd", 128); !errors.Is(err, errDecodedRequestBodyTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestReadRelayRequestDecodesZstdAndNormalizesOutboundHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := []byte(`{"model":"gpt-5","input":"hello"}`)
	compressed := zstdEncode(t, payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	setEntryProtocol(c, registry.ProtocolOpenAI)

	parsed, ok := readRelayRequest(c)
	if !ok {
		t.Fatalf("readRelayRequest failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if parsed.Model != "gpt-5" {
		t.Fatalf("model = %q", parsed.Model)
	}
	if !requestBodyWasDecoded(c) {
		t.Fatal("request was not marked decoded")
	}
	// Audit retains the exact client bytes, while routing/moderation and the
	// restored request reader consume the normalized JSON.
	if !bytes.Equal(relayInboundRequestBody(c), compressed) {
		t.Fatal("inbound audit body did not retain compressed client bytes")
	}
	restored, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if !bytes.Equal(restored, payload) {
		t.Fatalf("restored body = %q", restored)
	}
	if c.Request.ContentLength != int64(len(compressed)) {
		t.Fatalf("ContentLength = %d, want compressed length %d", c.Request.ContentLength, len(compressed))
	}
	if got := accountProviderHeaders(c).Get("Content-Encoding"); got != "" {
		t.Fatalf("native Content-Encoding = %q, want empty", got)
	}
	if got := channelCPAHeaders(c).Get("Content-Encoding"); got != "" {
		t.Fatalf("CPA Content-Encoding = %q, want empty", got)
	}
}

func TestReadRawBodyRejectsUnsupportedEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-5"}`))
	req.Header.Set("Content-Encoding", "br")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	setEntryProtocol(c, registry.ProtocolOpenAI)

	if _, ok := readRawBody(c); ok {
		t.Fatal("readRawBody unexpectedly accepted unsupported encoding")
	}
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
}

func gzipEncode(t *testing.T, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return out.Bytes()
}

func zstdEncode(t *testing.T, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := zw.Write(payload); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return out.Bytes()
}
