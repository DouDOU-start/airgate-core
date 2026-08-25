package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"

	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
)

var ErrRelayHookV2AccountTestUnavailable = errors.New("账号测试增强不可用")

type relayHookV2AccountTestRequest struct {
	Version  string          `json:"version"`
	Mode     string          `json:"mode"`
	Platform string          `json:"platform"`
	Endpoint string          `json:"endpoint"`
	Model    string          `json:"model"`
	Body     json.RawMessage `json:"body"`
}

type relayHookV2AccountTestDecision struct {
	Version     string          `json:"version"`
	RequestBody json.RawMessage `json:"request_body,omitempty"`
}

// TransformAccountTest 为管理员显式选择的测试模式执行 v2 请求体变换。
// 与真实转发的 fail-open 不同，管理员主动选择增强测试时采用 fail-closed：
// 没有处理器、超时或非法 body 都直接返回错误，避免悄悄退化成普通测试。
func (m *Manager) TransformAccountTest(ctx context.Context, mode, platform, endpoint, model string, body []byte) ([]byte, error) {
	if len(body) == 0 || len(body) > maxRelayHookV2BodyBytes || !jsonObjectBytes(body) {
		return nil, fmt.Errorf("%w: invalid_request_body", ErrRelayHookV2AccountTestUnavailable)
	}
	parsed := parseBody(body, "application/json")
	if parsed.Model != model {
		return nil, fmt.Errorf("%w: model_mismatch", ErrRelayHookV2AccountTestUnavailable)
	}
	chain := m.relayHookV2ChainFor(hookv2.CapabilityAccountTestTransformV1)
	if len(chain) == 0 {
		return nil, ErrRelayHookV2AccountTestUnavailable
	}

	chainCtx, cancel := context.WithTimeout(ctx, relayHookV2AccountTestTimeout)
	defer cancel()
	logger := sdk.LoggerFromContext(ctx)
	current := append([]byte(nil), body...)
	applied := false
	for _, item := range chain {
		if err := chainCtx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrRelayHookV2AccountTestUnavailable, relayHookV2ErrorCode(err))
		}
		payload, err := json.Marshal(relayHookV2AccountTestRequest{
			Version:  relayHookV2Version,
			Mode:     mode,
			Platform: platform,
			Endpoint: endpoint,
			Model:    model,
			Body:     current,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: invalid_request_payload", ErrRelayHookV2AccountTestUnavailable)
		}
		response, called, err := item.hook.invoke(chainCtx, hookv2.Request{
			Method: http.MethodPost,
			Path:   relayHookV2AccountTestPath,
			Header: map[string][]string{"Content-Type": {"application/json"}},
			Body:   payload,
		})
		if !called {
			continue
		}
		if err != nil {
			item.hook.recordFailure(time.Now())
			logger.Warn("account_test_hook_v2_call_failed", sdk.LogFieldPluginID, item.name, "error_code", relayHookV2ErrorCode(err))
			return nil, fmt.Errorf("%w: %s", ErrRelayHookV2AccountTestUnavailable, relayHookV2ErrorCode(err))
		}
		if response.StatusCode == http.StatusNoContent {
			item.hook.recordSuccess()
			continue
		}
		if response.StatusCode != http.StatusOK {
			item.hook.recordFailure(time.Now())
			return nil, fmt.Errorf("%w: unexpected_status_%d", ErrRelayHookV2AccountTestUnavailable, response.StatusCode)
		}
		var decision relayHookV2AccountTestDecision
		if err := json.Unmarshal(response.Body, &decision); err != nil {
			item.hook.recordFailure(time.Now())
			return nil, fmt.Errorf("%w: invalid_json", ErrRelayHookV2AccountTestUnavailable)
		}
		replacement, changed, err := normalizeRelayHookV2Body(&forwardState{model: model, stream: parsed.Stream}, relayHookV2Decision{
			Version:     decision.Version,
			RequestBody: decision.RequestBody,
		})
		if err != nil {
			item.hook.recordFailure(time.Now())
			return nil, fmt.Errorf("%w: %s", ErrRelayHookV2AccountTestUnavailable, relayHookV2ErrorCode(err))
		}
		item.hook.recordSuccess()
		if changed {
			current = replacement
			applied = true
		}
	}
	if !applied {
		return nil, ErrRelayHookV2AccountTestUnavailable
	}
	return current, nil
}
