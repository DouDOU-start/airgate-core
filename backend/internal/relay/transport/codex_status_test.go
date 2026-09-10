package transport

import (
	"net/http"
	"testing"
	"time"
)

func TestRewriteCodexRetryableStatusTreatsCapacityAs429(t *testing.T) {
	body := []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model."}}`)
	result := Result{StatusCode: http.StatusBadRequest, Body: body}

	rewriteCodexRetryableStatus(&result)

	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", result.StatusCode)
	}
	if string(result.Body) != string(body) {
		t.Fatalf("body was rewritten: %s", result.Body)
	}
	if result.Headers.Get("Retry-After") != "" {
		t.Fatalf("capacity errors should not invent Retry-After, got %q", result.Headers.Get("Retry-After"))
	}
}

func TestRewriteCodexRetryableStatusTreatsUsageLimitAs429(t *testing.T) {
	body := []byte(`{"error":{"type":"usage_limit_reached","message":"You've hit your usage limit.","resets_in_seconds":120}}`)
	result := Result{StatusCode: http.StatusBadRequest, Body: body}

	rewriteCodexRetryableStatus(&result)

	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", result.StatusCode)
	}
	if got := result.Headers.Get("Retry-After"); got != "120" {
		t.Fatalf("Retry-After = %q, want 120", got)
	}
}

func TestRewriteCodexRetryableStatusPreservesExistingRetryAfter(t *testing.T) {
	body := []byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":90}}`)
	result := Result{
		StatusCode: http.StatusBadRequest,
		Body:       body,
		Headers:    http.Header{"Retry-After": []string{"30"}},
	}

	rewriteCodexRetryableStatus(&result)

	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", result.StatusCode)
	}
	if got := result.Headers.Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want original 30", got)
	}
}

func TestRewriteCodexRetryableStatusLeavesOrdinaryClientErrors(t *testing.T) {
	result := Result{
		StatusCode: http.StatusBadRequest,
		Body:       []byte(`{"error":{"message":"invalid json","type":"invalid_request_error"}}`),
	}

	rewriteCodexRetryableStatus(&result)

	if result.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", result.StatusCode)
	}
}

func TestRewriteCodexRetryableStatusIgnoresCommittedStreamsAndSuccess(t *testing.T) {
	body := []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model."}}`)

	written := Result{StatusCode: http.StatusBadRequest, Body: body, Written: true}
	rewriteCodexRetryableStatus(&written)
	if written.StatusCode != http.StatusBadRequest {
		t.Fatalf("written stream status = %d, want 400", written.StatusCode)
	}

	success := Result{StatusCode: http.StatusOK, Body: body}
	rewriteCodexRetryableStatus(&success)
	if success.StatusCode != http.StatusOK {
		t.Fatalf("success status = %d, want 200", success.StatusCode)
	}
}

func TestParseCodexRetryAfterPrefersResetsAt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"error":{"type":"usage_limit_reached","resets_at":1700000300,"resets_in_seconds":1}}`)

	got := parseCodexRetryAfter(body, now)
	if got == nil {
		t.Fatal("expected retryAfter")
		return
	}
	if *got != 5*time.Minute {
		t.Fatalf("retryAfter = %v, want 5m", *got)
	}
}

func TestIsCodexUsageLimitErrorExcludesTransientRateLimit(t *testing.T) {
	if isCodexUsageLimitError([]byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`)) {
		t.Fatal("transient rate limit must not be treated as usage_limit_reached")
	}
	if !isCodexUsageLimitError([]byte(`{"error":{"type":"usage_limit_reached"}}`)) {
		t.Fatal("nested usage_limit_reached not detected")
	}
	if !isCodexUsageLimitError([]byte(`{"type":"usage_limit_reached"}`)) {
		t.Fatal("top-level usage_limit_reached not detected")
	}
}
