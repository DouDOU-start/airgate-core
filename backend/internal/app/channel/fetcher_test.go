package channel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchBalanceUsageEndpoint(t *testing.T) {
	// sub2api 口径：/v1/usage 顶层 remaining。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/usage" {
			_, _ = w.Write([]byte(`{"mode":"unrestricted","remaining":42.5,"balance":42.5,"unit":"USD"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	f := DefaultModelFetcher{Client: srv.Client()}

	bal, err := f.FetchBalance(context.Background(), "openai_compatible", srv.URL, "sk-x")
	if err != nil {
		t.Fatalf("FetchBalance err = %v", err)
	}
	if bal != 42.5 {
		t.Errorf("balance = %v, want 42.5 (from /v1/usage remaining)", bal)
	}
}

func TestFetchBalanceFallsBackToDashboardBilling(t *testing.T) {
	// /v1/usage 404 → 回退 /dashboard/billing（new-api 口径）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage":
			w.WriteHeader(http.StatusNotFound)
		case "/v1/dashboard/billing/subscription":
			_, _ = w.Write([]byte(`{"hard_limit_usd":100}`))
		case "/v1/dashboard/billing/usage":
			_, _ = w.Write([]byte(`{"total_usage":2500}`)) // 美分 → $25
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	f := DefaultModelFetcher{Client: srv.Client()}

	bal, err := f.FetchBalance(context.Background(), "openai_compatible", srv.URL, "sk-x")
	if err != nil {
		t.Fatalf("FetchBalance err = %v", err)
	}
	if bal != 75 {
		t.Errorf("balance = %v, want 75 (100 - 25 fallback)", bal)
	}
}

func TestFetchBalanceUsageAuthErrorNotSwallowed(t *testing.T) {
	// /v1/usage 返回 401（非 404）：直接报错，不回退瞎试。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer srv.Close()
	f := DefaultModelFetcher{Client: srv.Client()}

	if _, err := f.FetchBalance(context.Background(), "openai_compatible", srv.URL, "sk-bad"); err == nil {
		t.Error("expected auth error, got nil")
	}
}

func TestFetchBalanceUnsupportedType(t *testing.T) {
	f := DefaultModelFetcher{}
	for _, typ := range []string{"anthropic", "gemini"} {
		_, err := f.FetchBalance(context.Background(), typ, "https://x", "k")
		if !errors.Is(err, ErrBalanceUnsupported) {
			t.Errorf("type %s: err = %v, want ErrBalanceUnsupported", typ, err)
		}
	}
}
