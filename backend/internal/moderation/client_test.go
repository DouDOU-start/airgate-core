package moderation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig(baseURL string, keys ...string) *Config {
	cfg := DefaultConfig()
	cfg.BaseURL = baseURL
	cfg.APIKeys = keys
	cfg.RetryCount = 2
	cfg.TimeoutMS = 2000
	return cfg
}

func moderationServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestClientCallSuccess(t *testing.T) {
	var calls atomic.Int64
	srv := moderationServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/moderations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key-a" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"results":[{"flagged":true,"category_scores":{"hate":0.9}}]}`))
	})
	c := newAPIClient()
	result, err := c.call(context.Background(), testConfig(srv.URL, "key-a"), "text", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Flagged || result.CategoryScores["hate"] != 0.9 {
		t.Fatalf("result = %+v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestClientRetryAndFreeze(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantCalls  int64 // RetryCount=2 → 最多 3 次
		wantFreeze time.Duration
	}{
		{"500 重试并短冻结", http.StatusInternalServerError, 3, keyHTTPErrorFreezeDuration},
		{"429 重试并限流冻结", http.StatusTooManyRequests, 3, keyRateLimitFreezeDuration},
		{"401 重试后认证冻结", http.StatusUnauthorized, 3, keyAuthFreezeDuration},
		{"400 不重试不冻结", http.StatusBadRequest, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			srv := moderationServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
			})
			c := newAPIClient()
			cfg := testConfig(srv.URL, "only-key")
			_, err := c.call(context.Background(), cfg, "text", false)
			if err == nil {
				t.Fatal("应返回错误")
			}
			// 单 key 场景：首次失败即冻结后，后续尝试拿不到可用 key。
			if tt.wantFreeze > 0 {
				if calls.Load() != 1 {
					t.Fatalf("calls = %d，冻结后不应重打同一 key", calls.Load())
				}
				if !c.isKeyFrozen("only-key", time.Now()) {
					t.Fatal("key 应处于冻结")
				}
				st := c.keyStatusForHash(0, KeyHash("only-key"), MaskSecretTail("only-key"), true)
				if st.Status != "frozen" || st.FrozenUntil == nil {
					t.Fatalf("status = %+v", st)
				}
				until := time.Until(*st.FrozenUntil)
				if until > tt.wantFreeze || until < tt.wantFreeze-5*time.Second {
					t.Fatalf("冻结时长 %v，期望约 %v", until, tt.wantFreeze)
				}
				return
			}
			if calls.Load() != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
			if c.isKeyFrozen("only-key", time.Now()) {
				t.Fatal("400 不应冻结")
			}
		})
	}
}

func TestClientRoundRobinSkipsFrozen(t *testing.T) {
	var hits []string
	srv := moderationServer(t, func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		hits = append(hits, key)
		if key == "Bearer bad-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"flagged":false,"category_scores":{}}]}`))
	})
	c := newAPIClient()
	cfg := testConfig(srv.URL, "bad-key", "good-key")
	// 第一次：bad-key 401 冻结 → 重试轮到 good-key 成功。
	if _, err := c.call(context.Background(), cfg, "text", false); err != nil {
		t.Fatal(err)
	}
	// 后续调用应全部跳过冻结的 bad-key。
	for i := 0; i < 4; i++ {
		if _, err := c.call(context.Background(), cfg, "text", false); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range hits[2:] {
		if h == "Bearer bad-key" {
			t.Fatal("冻结的 key 不应再被调用")
		}
	}
	// 成功后清零冻结与失败计数。
	st := c.keyStatusForHash(1, KeyHash("good-key"), "", true)
	if st.Status != "ok" || st.FailureCount != 0 {
		t.Fatalf("good-key status = %+v", st)
	}
}

func TestClientSuccessClearsFreeze(t *testing.T) {
	c := newAPIClient()
	c.markKeyError("k", "boom", 10, http.StatusInternalServerError)
	if !c.isKeyFrozen("k", time.Now()) {
		t.Fatal("应冻结")
	}
	c.markKeySuccess("k", 5, http.StatusOK)
	if c.isKeyFrozen("k", time.Now()) {
		t.Fatal("成功后应解冻")
	}
	st := c.keyStatusForHash(0, KeyHash("k"), "", true)
	if st.FailureCount != 0 || st.LastError != "" || st.Status != "ok" {
		t.Fatalf("status = %+v", st)
	}
}
