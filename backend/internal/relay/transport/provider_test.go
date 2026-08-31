package transport

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

type fakeForwarder struct {
	called bool
	req    cpa.ForwardRequest
	result cpa.ForwardResult
}

func (f *fakeForwarder) Forward(_ context.Context, _ *gin.Context, req cpa.ForwardRequest) cpa.ForwardResult {
	f.called = true
	f.req = req
	return f.result
}

func TestNewCPAAdapterNil(t *testing.T) {
	if got := NewCPAAdapter(nil); got != nil {
		t.Fatalf("NewCPAAdapter(nil) = %T, want nil", got)
	}
}

func TestCPAAdapterForwardsResult(t *testing.T) {
	wantErr := errors.New("upstream")
	fake := &fakeForwarder{result: cpa.ForwardResult{StatusCode: 201, DataReceived: true, NetErr: wantErr}}
	got := NewCPAAdapter(fake)
	result := got.Execute(context.Background(), Request{
		Account: Account{ID: 7, Platform: "openai", Credentials: map[string]string{"api_key": "secret"}},
		Model:   "m", UpstreamModel: "upstream-m", Endpoint: "responses", Payload: []byte(`{"model":"m"}`),
	})
	if !fake.called {
		t.Fatal("adapter did not invoke backend")
	}
	if fake.req.Account.AccountID != 7 || fake.req.Model != "m" || fake.req.UpstreamModel != "upstream-m" {
		t.Fatalf("adapter request mapping = %+v", fake.req)
	}
	if result.StatusCode != 201 || !result.DataReceived || !errors.Is(result.NetErr, wantErr) {
		t.Fatalf("adapter result = %+v, want status 201 and original error", result)
	}
}

func TestCPAAdapterPreservesJSONAndRawBodySemantics(t *testing.T) {
	t.Run("JSON remains translation payload", func(t *testing.T) {
		fake := &fakeForwarder{}
		adapter := NewCPAAdapter(fake)
		payload := []byte(`{"model":"m"}`)
		adapter.Execute(context.Background(), Request{Payload: payload, Headers: http.Header{"Content-Type": {"application/json"}}})
		if string(fake.req.Payload) != string(payload) || fake.req.RawBody != nil || fake.req.RawContentType != "" {
			t.Fatalf("legacy JSON mapping = payload:%q raw:%q raw_content_type:%q", fake.req.Payload, fake.req.RawBody, fake.req.RawContentType)
		}
	})

	t.Run("raw contract keeps both bodies", func(t *testing.T) {
		fake := &fakeForwarder{}
		adapter := NewCPAAdapter(fake)
		payload := []byte(`{"model":"m"}`)
		raw := []byte("v=0\r\n")
		adapter.Execute(context.Background(), Request{Payload: payload, RawBody: raw, RawContentType: "application/sdp"})
		if string(fake.req.Payload) != string(payload) || string(fake.req.RawBody) != string(raw) || fake.req.RawContentType != "application/sdp" {
			t.Fatalf("legacy raw mapping = payload:%q raw:%q raw_content_type:%q", fake.req.Payload, fake.req.RawBody, fake.req.RawContentType)
		}
	})
}

func TestCPAAdapterCapabilities(t *testing.T) {
	adapter := NewCPAAdapter(&fakeForwarder{})
	provider, ok := adapter.(CapabilityProvider)
	if !ok {
		t.Fatal("adapter does not expose capabilities")
	}
	cap := provider.Capabilities()
	if !cap.HTTP || !cap.Streaming || !cap.Translation || cap.WebSocket {
		t.Fatalf("unexpected CPA capabilities: %+v", cap)
	}
}

func TestNewCPAAdapterIdempotent(t *testing.T) {
	fake := &fakeForwarder{}
	first := NewCPAAdapter(fake)
	second := NewCPAAdapter(first.(LegacyForwarder))
	if first != second {
		t.Fatalf("wrapping adapter changed instance: %p != %p", first, second)
	}
}
