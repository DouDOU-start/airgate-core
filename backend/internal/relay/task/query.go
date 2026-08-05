package task

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/DouDOU-start/airgate-core/internal/relay/errfmt"
	"github.com/DouDOU-start/airgate-core/internal/relay/outcome"
	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// maxBatchFetchIDs 批量查询单次 ID 数上限。
const maxBatchFetchIDs = 100

// HandleVideoGet GET /v1/videos/:task_id：读本地 task 快照重建视频对象。
// 同一路由兼容 xAI 原生视频与既有 OpenAI Video；优先查 xAI 任务，避免
// Grok Build 的轮询被旧任务 handler 误判为不存在。
func (f *Flow) HandleVideoGet(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	t, ad, ok := f.videoTaskForRequest(c)
	if !ok {
		return
	}
	c.Data(http.StatusOK, "application/json", ad.RenderTask(t))
}

func (f *Flow) videoTaskForRequest(c *gin.Context) (*Task, Adaptor, bool) {
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return nil, nil, false
	}
	taskID := c.Param("task_id")
	if taskID == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_task_id", "缺少任务 ID")
		return nil, nil, false
	}
	for _, platform := range []string{PlatformXAIVideo, PlatformOpenAIVideo} {
		t, err := f.store.GetForUser(c.Request.Context(), platform, taskID, keyInfo.UserID)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "任务查询失败")
			return nil, nil, false
		}
		if t == nil {
			continue
		}
		ad, err := GetAdaptor(platform)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "server_error", "internal_error", err.Error())
			return nil, nil, false
		}
		return t, ad, true
	}
	writeError(c, http.StatusNotFound, "invalid_request_error", "task_not_found", "任务不存在")
	return nil, nil, false
}

// HandleVideoContent GET /v1/videos/:task_id/content：成片内容实时代理
// （回任务落库的原渠道取，流式转发不落盘不缓存）。
func (f *Flow) HandleVideoContent(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolOpenAI)
	t, ad, ok := f.taskForRequest(c, PlatformOpenAIVideo)
	if !ok {
		return
	}
	if t.Status != StatusSuccess {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "task_not_completed",
			"任务尚未完成，当前状态: "+t.Status)
		return
	}
	cp, ok := ad.(ContentProxy)
	if !ok {
		writeError(c, http.StatusNotFound, "invalid_request_error", "not_supported", "该平台不支持内容下载")
		return
	}

	ch, ok := f.resolveTaskKey(t)
	if !ok {
		writeError(c, http.StatusBadGateway, "server_error", "channel_gone", "任务所属密钥端点已不存在，无法获取内容")
		return
	}
	apiKey := ch.APIKey
	if apiKey == "" {
		writeError(c, http.StatusBadGateway, "server_error", "channel_no_key", "任务所属密钥端点无可用密钥")
		return
	}
	info := &Info{ChannelKey: ch, APIKey: apiKey, RequestModel: t.RequestModel, UpstreamModel: t.UpstreamModel, Client: f.client}

	httpReq, err := cp.BuildContentRequest(c.Request.Context(), info, t.TaskID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", err.Error())
		return
	}
	resp, err := f.client.Do(httpReq)
	if err != nil {
		writeError(c, http.StatusBadGateway, "server_error", "upstream_error", "上游内容获取失败")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		up := errfmt.ParseUpstream(resp.StatusCode, body)
		// 出口给用户前抹掉上游渠道身份（密钥 + base_url/主机/IP）。
		up.Message = outcome.SanitizeUpstreamLeak(up.Message, []string{apiKey}, ch.BaseURL)
		writeUpstreamError(c, resp.StatusCode, up)
		return
	}

	// 媒体内容流式转发：透传 Content-Type / Content-Length / Content-Disposition。
	header := c.Writer.Header()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		header.Set("Content-Type", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		header.Set("Content-Length", cl)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		header.Set("Content-Disposition", cd)
	}
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		slog.Warn("task_content_proxy_aborted", "task_id", t.TaskID, "error", err)
	}
}

// resolveTaskKey 解析任务对应的密钥端点快照（内容代理用）：
// 优先按 channel_key_id 取；已删除或存量任务（key_id=0）回退到同渠道任一可用 key。
func (f *Flow) resolveTaskKey(t *Task) (*registry.ChannelKeySnapshot, bool) {
	if t.ChannelKeyID > 0 {
		if ch, ok := f.registry.Snapshot(t.ChannelKeyID); ok {
			return ch, true
		}
	}
	if t.ChannelID > 0 {
		return f.registry.AnyKeyForChannel(t.ChannelID)
	}
	return nil, false
}

// HandleSunoFetchByID GET /suno/fetch/:task_id：单任务查询（本地快照）。
func (f *Flow) HandleSunoFetchByID(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolSuno)
	t, ad, ok := f.taskForRequest(c, PlatformSuno)
	if !ok {
		return
	}
	c.Data(http.StatusOK, "application/json", ad.RenderTask(t))
}

// HandleSunoFetch POST /suno/fetch：批量任务查询（body {"ids":[...]}，本地快照）。
func (f *Flow) HandleSunoFetch(c *gin.Context) {
	setEntryProtocol(c, registry.ProtocolSuno)
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20)).Decode(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "invalid_body", "请求体须为 {\"ids\":[...]}")
		return
	}
	if len(req.IDs) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_ids", "缺少 ids")
		return
	}
	if len(req.IDs) > maxBatchFetchIDs {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "too_many_ids",
			"单次最多查询 "+strconv.Itoa(maxBatchFetchIDs)+" 个任务")
		return
	}

	ad, err := GetAdaptor(PlatformSuno)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", err.Error())
		return
	}
	bq, ok := ad.(BatchQuerying)
	if !ok {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "suno 适配器缺少批量查询能力")
		return
	}
	ts, err := f.store.ListForUser(c.Request.Context(), PlatformSuno, req.IDs, keyInfo.UserID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "任务查询失败")
		return
	}
	c.Data(http.StatusOK, "application/json", bq.RenderTaskList(ts))
}

// taskForRequest 查询入口公共段：鉴权信息 → 按 (platform, :task_id, user) 取本地任务。
// 失败时已写出错误体，返回 ok=false。
func (f *Flow) taskForRequest(c *gin.Context, platform string) (*Task, Adaptor, bool) {
	keyInfo, ok := requireKeyInfo(c)
	if !ok {
		return nil, nil, false
	}
	taskID := c.Param("task_id")
	if taskID == "" {
		writeError(c, http.StatusBadRequest, "invalid_request_error", "missing_task_id", "缺少任务 ID")
		return nil, nil, false
	}
	ad, err := GetAdaptor(platform)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", err.Error())
		return nil, nil, false
	}
	t, err := f.store.GetForUser(c.Request.Context(), platform, taskID, keyInfo.UserID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "server_error", "internal_error", "任务查询失败")
		return nil, nil, false
	}
	if t == nil {
		writeError(c, http.StatusNotFound, "invalid_request_error", "task_not_found", "任务不存在")
		return nil, nil, false
	}
	return t, ad, true
}
