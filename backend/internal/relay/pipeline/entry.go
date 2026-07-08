package pipeline

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/adaptor"
	"github.com/DouDOU-start/airgate-core/internal/relay/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/middleware"
)

// maxRequestBodyBytes chat completions 请求体上限（含多模态 base64 内容留足余量）。
const maxRequestBodyBytes = 32 << 20

// HandleChatCompletions POST /v1/chat/completions 入口 handler。
func (p *Pipeline) HandleChatCompletions(c *gin.Context) {
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "请求体超出大小限制")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_body", "读取请求体失败")
		return
	}

	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "请求体必须是 JSON 对象")
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}

	p.forward(c, keyInfo, req, adaptor.EndpointChatCompletions)
}

// HandleResponses POST /v1/responses 入口 handler（OpenAI Responses API create）。
//
// 复用 chat completions 的读体/大小/JSON 校验与 forward 主循环，仅端点标识不同：
// 请求体透传（input/instructions/tools 等原样进上游），调度/failover/计费一致。
func (p *Pipeline) HandleResponses(c *gin.Context) {
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "请求体超出大小限制")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_body", "读取请求体失败")
		return
	}

	req, err := dto.ParseChatRequest(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_json", "请求体必须是 JSON 对象")
		return
	}
	if req.Model == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_model", "缺少 model 字段")
		return
	}

	p.forward(c, keyInfo, req, adaptor.EndpointResponses)
}

// modelItem GET /v1/models 的单条模型（OpenAI 格式）。
type modelItem struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// modelList GET /v1/models 响应（OpenAI 格式）。
type modelList struct {
	Object string      `json:"object"`
	Data   []modelItem `json:"data"`
}

// HandleModels GET /v1/models：按 keyInfo 分组聚合可用渠道的模型并集。
func (p *Pipeline) HandleModels(c *gin.Context) {
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}

	models := p.registry.ModelsForGroup(keyInfo.GroupID)
	items := make([]modelItem, 0, len(models))
	for _, m := range models {
		items = append(items, modelItem{ID: m, Object: "model", Created: 0, OwnedBy: "airgate"})
	}
	c.JSON(http.StatusOK, modelList{Object: "list", Data: items})
}

// requireKeyInfo 从 gin ctx 取 APIKeyAuth 写入的 keyInfo；缺失（装配错误）写 401。
func requireKeyInfo(c *gin.Context) (*auth.APIKeyInfo, bool) {
	value, exists := c.Get(middleware.CtxKeyKeyInfo)
	if !exists {
		writeError(c, http.StatusUnauthorized, "authentication_error", "missing_api_key", "缺少 API Key")
		return nil, false
	}
	keyInfo, ok := value.(*auth.APIKeyInfo)
	if !ok || keyInfo == nil {
		writeError(c, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "API Key 信息无效")
		return nil, false
	}
	return keyInfo, true
}
