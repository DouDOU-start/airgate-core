package pipeline

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/errlog"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
)

// ModerationChecker 内容审核窄接口（*moderation.Engine 天然满足；nil 安全）。
type ModerationChecker interface {
	Check(ctx context.Context, in moderation.CheckRequest) moderation.Decision
}

// moderationProtocolFor 入站端点 → 审核输入抽取协议。
// countTokens 类端点（不产生生成消费）与 multipart 图像编辑（v1 不审）返回 ok=false。
func moderationProtocolFor(endpoint string) (string, bool) {
	switch endpoint {
	case adaptor.EndpointChatCompletions:
		return moderation.ProtocolOpenAIChat, true
	case adaptor.EndpointResponses:
		return moderation.ProtocolOpenAIResponses, true
	case adaptor.EndpointImagesGenerations:
		return moderation.ProtocolOpenAIImages, true
	case adaptor.EndpointMessages:
		return moderation.ProtocolAnthropicMessages, true
	case adaptor.EndpointGenerateContent, adaptor.EndpointPredict:
		return moderation.ProtocolGemini, true
	default:
		return "", false
	}
}

// moderationCheck 触网前的内容审核预检。放行返回 true；
// 拦截时按入口协议写出错误体并落失败留痕，返回 false。
// 引擎未注入、端点不在审核范围、引擎内部故障时均放行（fail-open 由引擎保证）。
func (p *Pipeline) moderationCheck(c *gin.Context, keyInfo *auth.APIKeyInfo, req *dto.ChatRequest, endpoint string, start time.Time) bool {
	if p.moderation == nil {
		return true
	}
	protocol, ok := moderationProtocolFor(endpoint)
	if !ok {
		return true
	}
	body, err := req.Marshal()
	if err != nil {
		return true
	}
	d := p.moderation.Check(c.Request.Context(), moderation.CheckRequest{
		RequestID: requestIDOf(c),
		UserID:    keyInfo.UserID,
		UserEmail: keyInfo.UserEmail,
		APIKeyID:  keyInfo.KeyID,
		GroupID:   keyInfo.GroupID,
		Endpoint:  c.Request.URL.Path,
		Protocol:  protocol,
		Model:     req.Model,
		Body:      body,
	})
	if d.Allowed {
		return true
	}
	status := d.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusForbidden
	}
	code := moderationErrorCode(d.Action)
	writeError(c, status, "permission_error", code, d.Message)
	p.recordFailure(c, keyInfo, req, start, errlog.Entry{
		Phase: errlog.PhasePrecheckModeration, StatusCode: status,
		ErrorType: "permission_error", ErrorCode: code, Message: d.Message,
	})
	return false
}

func moderationErrorCode(action string) string {
	switch action {
	case moderation.ActionKeywordBlock:
		return "content_keyword_blocked"
	case moderation.ActionHashBlock:
		return "content_hash_blocked"
	default:
		return "content_blocked"
	}
}
