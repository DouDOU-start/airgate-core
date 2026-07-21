package moderation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type apiRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type apiInputPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *apiImageURLRef `json:"image_url,omitempty"`
}

type apiImageURLRef struct {
	URL string `json:"url"`
}

type apiResponse struct {
	Results []apiResult `json:"results"`
}

type apiResult struct {
	Flagged        bool               `json:"flagged"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

// KeyStatus 单个审核 key 的健康状态（配置页与状态页展示）。
type KeyStatus struct {
	Index          int        `json:"index"`
	KeyHash        string     `json:"key_hash"`
	Masked         string     `json:"masked"`
	Status         string     `json:"status"` // unknown / ok / error / frozen
	FailureCount   int        `json:"failure_count"`
	SuccessCount   int64      `json:"success_count"`
	LastError      string     `json:"last_error"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	FrozenUntil    *time.Time `json:"frozen_until,omitempty"`
	LastLatencyMS  int        `json:"last_latency_ms"`
	LastHTTPStatus int        `json:"last_http_status"`
	LastTested     bool       `json:"last_tested"`
	Configured     bool       `json:"configured"`
}

// KeyLoad 单个审核 key 的同步调用负载指标（pre_block 状态页展示）。
type KeyLoad struct {
	Index          int    `json:"index"`
	KeyHash        string `json:"key_hash"`
	Masked         string `json:"masked"`
	Status         string `json:"status"`
	Active         int64  `json:"active"`
	Total          int64  `json:"total"`
	Success        int64  `json:"success"`
	Errors         int64  `json:"errors"`
	AvgLatencyMS   int64  `json:"avg_latency_ms"`
	LastLatencyMS  int    `json:"last_latency_ms"`
	LastHTTPStatus int    `json:"last_http_status"`
}

type keyHealth struct {
	Hash           string
	Masked         string
	FailureCount   int
	SuccessCount   int64
	LastError      string
	LastCheckedAt  time.Time
	FrozenUntil    time.Time
	LastLatencyMS  int
	LastHTTPStatus int
	LastTested     bool
	SyncActive     int64
	SyncTotal      int64
	SyncSuccess    int64
	SyncErrors     int64
	SyncLatencyMS  int64
}

// apiClient 外部审核 API 客户端：多 key round-robin 轮询 + 按 HTTP 状态分级
// 冻结熔断 + 重试退避；key 健康状态供 admin 观测。
type apiClient struct {
	http     *http.Client
	cursor   atomic.Uint64
	healthMu sync.Mutex
	health   map[string]*keyHealth
}

func newAPIClient() *apiClient {
	return &apiClient{
		http:   &http.Client{},
		health: make(map[string]*keyHealth),
	}
}

// call 带重试与熔断的审核调用：round-robin 取非冻结 key，失败退避
// 100*(n+1)ms 重试（400 视为请求本身问题不重试），成功即回写健康并返回。
// trackLoad 为 true 时记录同步负载指标（pre_block 同步链路专用）。
func (c *apiClient) call(ctx context.Context, cfg *Config, input any, trackLoad bool) (*apiResult, error) {
	attempts := cfg.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}
	if attempts > maxRetryCount+1 {
		attempts = maxRetryCount + 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		key, ok := c.nextUsableKey(cfg)
		if !ok {
			lastErr = errors.New("no moderation api key available")
			break
		}
		if trackLoad {
			c.beginKeyCall(key)
		}
		start := time.Now()
		httpStatus := 0
		result, err := c.callOnce(ctx, cfg, key, input, &httpStatus)
		latency := int(time.Since(start).Milliseconds())
		if err == nil {
			if trackLoad {
				c.finishKeyCall(key, latency, true)
			}
			c.markKeySuccess(key, latency, httpStatus)
			return result, nil
		}
		if trackLoad {
			c.finishKeyCall(key, latency, false)
		}
		c.markKeyError(key, err.Error(), latency, httpStatus)
		lastErr = err
		if httpStatus == http.StatusBadRequest {
			break
		}
		if attempt == attempts-1 {
			break
		}
		wait := time.Duration(100*(attempt+1)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

// callOnce 单次调用 {base_url}/v1/moderations，解析 results[0]。
func (c *apiClient) callOnce(ctx context.Context, cfg *Config, apiKey string, input any, httpStatus *int) (*apiResult, error) {
	endpoint, err := url.JoinPath(strings.TrimRight(cfg.BaseURL, "/"), "/v1/moderations")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(apiRequest{Model: cfg.Model, Input: input})
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if httpStatus != nil {
		*httpStatus = resp.StatusCode
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("moderation api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Results) == 0 {
		return nil, errors.New("moderation api returned empty results")
	}
	return &out.Results[0], nil
}

func (c *apiClient) nextUsableKey(cfg *Config) (string, bool) {
	keys := cfg.APIKeys
	if len(keys) == 0 {
		return "", false
	}
	now := time.Now()
	for i := 0; i < len(keys); i++ {
		idx := int(c.cursor.Add(1)-1) % len(keys)
		key := keys[idx]
		if !c.isKeyFrozen(key, now) {
			return key, true
		}
	}
	return "", false
}

func (c *apiClient) isKeyFrozen(key string, now time.Time) bool {
	hash := KeyHash(key)
	if hash == "" {
		return false
	}
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.health[hash]
	return state != nil && state.FrozenUntil.After(now)
}

func (c *apiClient) beginKeyCall(key string) {
	hash := KeyHash(key)
	if hash == "" {
		return
	}
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	c.ensureHealthLocked(hash, MaskSecretTail(key)).SyncActive++
}

func (c *apiClient) finishKeyCall(key string, latencyMS int, success bool) {
	hash := KeyHash(key)
	if hash == "" {
		return
	}
	if latencyMS < 0 {
		latencyMS = 0
	}
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.ensureHealthLocked(hash, MaskSecretTail(key))
	if state.SyncActive > 0 {
		state.SyncActive--
	}
	state.SyncTotal++
	state.SyncLatencyMS += int64(latencyMS)
	if success {
		state.SyncSuccess++
		return
	}
	state.SyncErrors++
}

func (c *apiClient) markKeySuccess(key string, latencyMS int, httpStatus int) {
	hash := KeyHash(key)
	if hash == "" {
		return
	}
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.ensureHealthLocked(hash, MaskSecretTail(key))
	state.FailureCount = 0
	state.SuccessCount++
	state.LastError = ""
	state.LastCheckedAt = time.Now()
	state.FrozenUntil = time.Time{}
	state.LastLatencyMS = latencyMS
	state.LastHTTPStatus = httpStatus
	state.LastTested = true
}

func (c *apiClient) markKeyError(key string, errText string, latencyMS int, httpStatus int) {
	hash := KeyHash(key)
	if hash == "" {
		return
	}
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.ensureHealthLocked(hash, MaskSecretTail(key))
	freeze := freezeDurationForHTTPStatus(httpStatus)
	if freeze > 0 {
		state.FailureCount++
		state.FrozenUntil = time.Now().Add(freeze)
	}
	state.LastError = trimRunes(errText, 180)
	state.LastCheckedAt = time.Now()
	state.LastLatencyMS = latencyMS
	state.LastHTTPStatus = httpStatus
	state.LastTested = true
}

// freezeDurationForHTTPStatus 按上游状态分级冻结：0（网络错前未定状态）与
// 400（请求问题非 key 问题）不冻结；401/403 认证错冻 10 分钟；
// 429/529 限流冻 1 分钟；其余（5xx 等）冻 10 秒。
func freezeDurationForHTTPStatus(httpStatus int) time.Duration {
	switch httpStatus {
	case http.StatusUnauthorized, http.StatusForbidden:
		return keyAuthFreezeDuration
	case http.StatusTooManyRequests, 529:
		return keyRateLimitFreezeDuration
	case 0, http.StatusBadRequest:
		return 0
	default:
		return keyHTTPErrorFreezeDuration
	}
}

func (c *apiClient) ensureHealthLocked(hash string, masked string) *keyHealth {
	state := c.health[hash]
	if state == nil {
		state = &keyHealth{Hash: hash}
		c.health[hash] = state
	}
	if strings.TrimSpace(masked) != "" {
		state.Masked = masked
	}
	return state
}

// KeyStatuses 按配置 key 列表导出健康状态快照。
func (c *apiClient) KeyStatuses(keys []string) []KeyStatus {
	out := make([]KeyStatus, 0, len(keys))
	for idx, key := range keys {
		out = append(out, c.keyStatusForHash(idx, KeyHash(key), MaskSecretTail(key), true))
	}
	return out
}

// KeyLoads 按配置 key 列表导出同步负载指标快照。
func (c *apiClient) KeyLoads(keys []string) []KeyLoad {
	out := make([]KeyLoad, 0, len(keys))
	for idx, key := range keys {
		out = append(out, c.keyLoadForHash(idx, KeyHash(key), MaskSecretTail(key)))
	}
	return out
}

func (c *apiClient) availableKeyCount(keys []string) int64 {
	now := time.Now()
	var count int64
	for _, key := range keys {
		if !c.isKeyFrozen(key, now) {
			count++
		}
	}
	return count
}

func (c *apiClient) keyLoadForHash(index int, hash string, masked string) KeyLoad {
	load := KeyLoad{Index: index, KeyHash: hash, Masked: masked, Status: "unknown"}
	status := c.keyStatusForHash(index, hash, masked, true)
	load.Status = status.Status
	load.LastLatencyMS = status.LastLatencyMS
	load.LastHTTPStatus = status.LastHTTPStatus
	if hash == "" {
		return load
	}
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.health[hash]
	if state == nil {
		return load
	}
	load.Active = state.SyncActive
	load.Total = state.SyncTotal
	load.Success = state.SyncSuccess
	load.Errors = state.SyncErrors
	if state.SyncTotal > 0 {
		load.AvgLatencyMS = state.SyncLatencyMS / state.SyncTotal
	}
	return load
}

func (c *apiClient) keyStatusForHash(index int, hash string, masked string, configured bool) KeyStatus {
	status := KeyStatus{Index: index, KeyHash: hash, Masked: masked, Status: "unknown", Configured: configured}
	if hash == "" {
		return status
	}
	now := time.Now()
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	state := c.health[hash]
	if state == nil {
		return status
	}
	status.FailureCount = state.FailureCount
	status.SuccessCount = state.SuccessCount
	status.LastError = state.LastError
	status.LastLatencyMS = state.LastLatencyMS
	status.LastHTTPStatus = state.LastHTTPStatus
	status.LastTested = state.LastTested
	if !state.LastCheckedAt.IsZero() {
		t := state.LastCheckedAt
		status.LastCheckedAt = &t
	}
	if state.FrozenUntil.After(now) {
		t := state.FrozenUntil
		status.FrozenUntil = &t
		status.Status = "frozen"
		return status
	}
	if state.LastError != "" {
		status.Status = "error"
		return status
	}
	if state.SuccessCount > 0 || state.LastTested {
		status.Status = "ok"
	}
	return status
}

// KeyHash 审核 key 的 sha256 标识（配置页按 hash 删除、状态对齐用，不可逆）。
func KeyHash(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
