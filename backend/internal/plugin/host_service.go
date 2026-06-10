package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/ent/group"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	pb "github.com/DouDOU-start/airgate-sdk/protocol/proto"
	sdk "github.com/DouDOU-start/airgate-sdk/sdkgo"
)

// HostService 是 Core 暴露给插件的反向 gRPC 能力的"底层实现"。
//
// 它本身不做插件 capability 校验。真正面向插件的实现是 pluginHostHandle，
// 它在 Invoke / InvokeStream 入口按 method 做 capability 校验，再委托给本结构。
//
// 设计原则（详见 ADR-0001）：
//   - 提供通用平台原语层——新增插件应只组合已有 RPC，无需扩 proto；
//   - ProbeForward 与普通 Forward 严格隔离：跳过 usage_log 写入、跳过余额扣款，
//     但仍然 ReportResult 让账号状态机受益；
//   - Forward 走完整管线（调度 → 网关 → 计费 → 记录），用于操练场等面向用户的插件；
//   - 不要求插件持有 admin_api_key——broker 子进程隧道天然互信，但仍然要做
//     capability 级权限隔离。
type HostService struct {
	db          *ent.Client
	manager     *Manager
	scheduler   *scheduler.Scheduler
	concurrency *scheduler.ConcurrencyManager
	calculator  *billing.Calculator
	recorder    *billing.Recorder
}

// NewHostService 构造 HostService 工厂。
// 由 server 在创建 Manager + scheduler 之后立即创建并 SetHostService 注入到 Manager。
//
// HostService 自身不实现 pb.CoreInvokeServiceServer——用 NewPluginHandle 给每个插件
// 派生一个 pluginHostHandle 才是真正的 server 实例。
func NewHostService(
	db *ent.Client,
	mgr *Manager,
	sched *scheduler.Scheduler,
	concurrency *scheduler.ConcurrencyManager,
	calculator *billing.Calculator,
	recorder *billing.Recorder,
) *HostService {
	return &HostService{
		db:          db,
		manager:     mgr,
		scheduler:   sched,
		concurrency: concurrency,
		calculator:  calculator,
		recorder:    recorder,
	}
}

// NewPluginHandle 为指定插件派生一个 host handle。
//
// 调用流程：
//  1. Manager 在 spawn 插件之前调本方法创建一个 handle，初始 capability = nil（拒绝所有）
//  2. 把 handle 作为 CoreInvokeImpl 注入 GatewayGRPCPlugin / ExtensionGRPCPlugin / MiddlewareGRPCPlugin
//  3. spawn 完成 → Info() 拿到 capability 列表 → 调 handle.SetCapabilities(...)
//  4. 之后插件调任何 RPC 都会按当前 capability set 过滤
//
// 这个时序窗口意味着：插件的 Init() 阶段**不应该**调 host RPC（capability 还没绑），
// 只能在 Start() 之后用。这是有意为之——Init 应该是同步的、不依赖 core 反向通道。
func (h *HostService) NewPluginHandle(pluginName string) *pluginHostHandle {
	return &pluginHostHandle{base: h, pluginName: pluginName}
}

// ============================================================================
// pluginHostHandle —— 实际暴露给插件的 server，做 capability 校验后委托到 base
// ============================================================================

// pluginHostHandle 是一个 per-plugin 的 CoreInvokeServiceServer。
//
// 持有一个不可变的 base + 一个可变的 capability set（atomic 保护）。每个 RPC 入口先
// requireMethod 再委托。capability set 的写入是 spawn 后由 manager 完成的，写入之后
// 在该插件生命周期内通常不再变（OnConfigUpdate 重新走 Init 时会重新创建 handle）。
type pluginHostHandle struct {
	pb.UnimplementedCoreInvokeServiceServer

	base       *HostService
	pluginName string

	// caps 指针指向一个 map[sdk.Capability]bool。nil = capability 尚未绑定，所有 RPC 都拒绝。
	// 用 atomic.Pointer 是为了让 SetCapabilities 与 RPC 处理并发安全，无需 mutex。
	caps atomic.Pointer[map[sdk.Capability]bool]
}

// SetCapabilities 由 Manager 在 spawn 完成、Info() 拿到 capability 列表后调用。
//
// 空 set（len=0）== 显式声明"什么都不要"，所有 RPC 都被拒。
func (h *pluginHostHandle) SetCapabilities(caps map[sdk.Capability]bool) {
	cloned := make(map[sdk.Capability]bool, len(caps))
	for k, v := range caps {
		cloned[k] = v
	}
	h.caps.Store(&cloned)
}

func (h *pluginHostHandle) requireMethod(method string) error {
	caps := h.caps.Load()
	if caps == nil {
		slog.Warn("host_service_capability_unbound",
			sdk.LogFieldPluginID, h.pluginName, "method", method)
		return status.Errorf(codes.PermissionDenied,
			"plugin %q capabilities are not bound", h.pluginName)
	}
	if (*caps)[sdk.CapabilityHostInvoke] || (*caps)[sdk.CapabilityForHostMethod(method)] {
		return nil
	}
	slog.Warn("host_service_method_denied",
		sdk.LogFieldPluginID, h.pluginName, "method", method)
	return status.Errorf(codes.PermissionDenied,
		"plugin %q lacks host invoke capability for method %q", h.pluginName, method)
}

func (h *pluginHostHandle) Invoke(ctx context.Context, req *pb.HostInvokeRequest) (*pb.HostInvokeResponse, error) {
	if req == nil || req.Method == "" {
		return nil, status.Error(codes.InvalidArgument, "method 不能为空")
	}
	if err := h.requireMethod(req.Method); err != nil {
		return nil, err
	}
	payload, err := h.base.invoke(ctx, h.pluginName, req.Method, req.Payload, req.IdempotencyKey, req.Metadata)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode response payload: %v", err)
	}
	return &pb.HostInvokeResponse{
		Status:   "ok",
		Payload:  encoded,
		Metadata: map[string]string{"method": req.Method},
	}, nil
}

func (h *pluginHostHandle) InvokeStream(stream pb.CoreInvokeService_InvokeStreamServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.Method == "" {
		return status.Error(codes.InvalidArgument, "stream 首帧 method 不能为空")
	}
	if err := h.requireMethod(first.Method); err != nil {
		return err
	}
	return h.base.invokeStream(stream.Context(), h.pluginName, first, stream)
}

const (
	hostMethodSchedulerSelectAccount = "scheduler.select_account"
	hostMethodSchedulerReportResult  = "scheduler.report_account_result"
	hostMethodProbeForward           = "probe.forward"
	hostMethodGroupsList             = "groups.list"
	hostMethodGatewayForward         = "gateway.forward"
	hostMethodPlatformsList          = "platforms.list"
	hostMethodModelsList             = "models.list"
	hostMethodUsersGet               = "users.get"
	hostMethodAssetsStore            = "assets.store"
	hostMethodAssetsStoreURL         = "assets.store_url"
	hostMethodAssetsGetURL           = "assets.get_url"
	hostMethodAssetsGetBytes         = "assets.get_bytes"
	hostMethodAssetsDelete           = "assets.delete"
	hostMethodTasksCreate            = "tasks.create"
	hostMethodTasksUpdate            = "tasks.update"
	hostMethodTasksGet               = "tasks.get"
	hostMethodTasksList              = "tasks.list"
	hostMethodTasksDelete            = "tasks.delete"
)

func (h *HostService) invoke(
	ctx context.Context,
	pluginID, method string,
	payload []byte,
	idempotencyKey string,
	metadata map[string]string,
) (map[string]interface{}, error) {
	_ = metadata
	switch method {
	case hostMethodSchedulerSelectAccount:
		var req hostSelectAccountRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.selectAccount(ctx, req)
	case hostMethodSchedulerReportResult:
		var req hostReportAccountResultRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.reportAccountResult(ctx, req)
	case hostMethodProbeForward:
		var req hostProbeForwardRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.probeForward(ctx, req)
	case hostMethodGroupsList:
		var req hostListGroupsRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.listGroups(ctx, req)
	case hostMethodGatewayForward:
		var req hostForwardRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.forward(ctx, req)
	case hostMethodPlatformsList:
		return h.listPlatforms(ctx)
	case hostMethodModelsList:
		var req hostListModelsRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.listModels(ctx, req)
	case hostMethodUsersGet:
		var req hostGetUserInfoRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.getUserInfo(ctx, req)
	case hostMethodAssetsStore:
		var req hostStoreAssetRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.storeAsset(ctx, req)
	case hostMethodAssetsStoreURL:
		var req hostStoreAssetFromURLRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.storeAssetFromURL(ctx, req)
	case hostMethodAssetsGetURL:
		var req hostGetAssetURLRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.getAssetURL(ctx, req)
	case hostMethodAssetsGetBytes:
		var req hostGetAssetBytesRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.getAssetBytes(ctx, req)
	case hostMethodAssetsDelete:
		var req hostDeleteAssetRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.deleteAsset(ctx, req)
	case hostMethodTasksCreate:
		var req hostCreateTaskRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		if idempotencyKey != "" && req.IdempotencyKey == "" {
			req.IdempotencyKey = idempotencyKey
		}
		return h.createTask(ctx, pluginID, req)
	case hostMethodTasksUpdate:
		var req hostUpdateTaskRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.updateTask(ctx, pluginID, req)
	case hostMethodTasksGet:
		var req hostGetTaskRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.getTask(ctx, pluginID, req)
	case hostMethodTasksList:
		var req hostListTasksRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.listTasks(ctx, pluginID, req)
	case hostMethodTasksDelete:
		var req hostDeleteTaskRequest
		if err := decodeHostPayload(payload, &req); err != nil {
			return nil, err
		}
		return h.deleteTask(ctx, pluginID, req)
	default:
		return nil, status.Errorf(codes.Unimplemented, "unknown host method: %s", method)
	}
}

func (h *HostService) invokeStream(
	ctx context.Context,
	pluginID string,
	first *pb.HostStreamFrame,
	stream pb.CoreInvokeService_InvokeStreamServer,
) error {
	_ = pluginID
	switch first.Method {
	case hostMethodGatewayForward:
		var req hostForwardRequest
		if err := decodeHostPayload(first.Payload, &req); err != nil {
			return err
		}
		req.Stream = true
		return h.forwardStream(ctx, req, stream)
	default:
		return status.Errorf(codes.Unimplemented, "unknown host stream method: %s", first.Method)
	}
}

func decodeHostPayload(payload []byte, out interface{}) error {
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid payload JSON: %v", err)
	}
	return nil
}

type hostSelectAccountRequest struct {
	GroupID           int64   `json:"group_id"`
	Model             string  `json:"model"`
	SessionID         string  `json:"session_id"`
	ExcludeAccountIDs []int64 `json:"exclude_account_ids"`
}

type hostReportAccountResultRequest struct {
	AccountID int64  `json:"account_id"`
	Success   bool   `json:"success"`
	ErrorMsg  string `json:"error_msg"`
}

type hostProbeForwardRequest struct {
	GroupID int64  `json:"group_id"`
	Model   string `json:"model"`
}

type hostForwardRequest struct {
	UserID   int64                  `json:"user_id"`
	GroupID  int64                  `json:"group_id"`
	APIKeyID int64                  `json:"api_key_id,omitempty"`
	Model    string                 `json:"model"`
	Method   string                 `json:"method"`
	Path     string                 `json:"path"`
	Headers  map[string]interface{} `json:"headers"`
	Body     interface{}            `json:"body"`
	Stream   bool                   `json:"stream"`
}

type hostListModelsRequest struct {
	Platform string `json:"platform"`
}

type hostGetUserInfoRequest struct {
	UserID int64 `json:"user_id"`
}

type hostStoreAssetRequest struct {
	UserID        int64  `json:"user_id"`
	Purpose       string `json:"purpose"` // core 枚举：chat/upload/generated/task-input/temp
	ContentType   string `json:"content_type"`
	FileExtension string `json:"file_extension"`
	Data          []byte `json:"data"`
}

type hostStoreAssetFromURLRequest struct {
	UserID    int64  `json:"user_id"`
	Purpose   string `json:"purpose"` // core 枚举：chat/upload/generated/task-input/temp
	SourceURL string `json:"source_url"`
}

type hostGetAssetURLRequest struct {
	ObjectKey string `json:"object_key"`
}

type hostGetAssetBytesRequest struct {
	ObjectKey string `json:"object_key"`
}

type hostDeleteAssetRequest struct {
	ObjectKey string `json:"object_key"`
}

type hostCreateTaskRequest struct {
	PluginID       string                 `json:"plugin_id"`
	TaskType       string                 `json:"task_type"`
	UserID         int64                  `json:"user_id"`
	Input          map[string]interface{} `json:"input"`
	Attributes     map[string]interface{} `json:"attributes"`
	Execution      map[string]interface{} `json:"execution"`
	Priority       int                    `json:"priority"`
	MaxAttempts    int                    `json:"max_attempts"`
	PublicTaskID   string                 `json:"public_task_id"`
	IdempotencyKey string                 `json:"idempotency_key"`
}

type hostUpdateTaskRequest struct {
	TaskID       int64                  `json:"task_id"`
	Status       string                 `json:"status"`
	Progress     *int                   `json:"progress"`
	Stage        *string                `json:"stage"`
	Output       map[string]interface{} `json:"output"`
	Attributes   map[string]interface{} `json:"attributes"`
	Execution    map[string]interface{} `json:"execution"`
	ErrorType    string                 `json:"error_type"`
	ErrorCode    string                 `json:"error_code"`
	ErrorMessage string                 `json:"error_message"`
	UsageID      *int                   `json:"usage_id"`
}

type hostGetTaskRequest struct {
	PluginID     string `json:"plugin_id"`
	TaskID       int64  `json:"task_id"`
	PublicTaskID string `json:"public_task_id"`
	UserID       int64  `json:"user_id"`
}

type hostListTasksRequest struct {
	PluginID string `json:"plugin_id"`
	UserID   int64  `json:"user_id"`
	TaskType string `json:"task_type"`
	Status   string `json:"status"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

type hostDeleteTaskRequest struct {
	PluginID string `json:"plugin_id"`
	TaskID   int64  `json:"task_id"`
	UserID   int64  `json:"user_id"`
}

// selectAccount 调度选号：走和真实用户请求完全相同的路径。
func (h *HostService) selectAccount(ctx context.Context, req hostSelectAccountRequest) (map[string]interface{}, error) {
	if req.GroupID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "group_id 必须 > 0")
	}
	g, err := h.db.Group.Get(ctx, int(req.GroupID))
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, cerr
		}
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "分组不存在")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	model := req.Model
	if model == "" {
		if models := h.manager.GetModels(g.Platform); len(models) > 0 {
			model = models[0].ID
		}
	}

	excludeIDs := make([]int, 0, len(req.ExcludeAccountIDs))
	for _, id := range req.ExcludeAccountIDs {
		excludeIDs = append(excludeIDs, int(id))
	}

	acc, err := h.scheduler.SelectAccount(ctx, g.Platform, model, 0, int(req.GroupID), req.SessionID, excludeIDs...)
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, cerr
		}
		// scheduler 自身的"无可用账户"是业务可预期错误，用 NotFound 让插件区分
		if errors.Is(err, scheduler.ErrNoAvailableAccount) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{
		"account_id":   int64(acc.ID),
		"account_name": acc.Name,
		"platform":     acc.Platform,
	}, nil
}

// probeForward 黑盒探测：自动调度 + 直接执行 + 反馈状态机。
// 内部 worker，由 pluginHostHandle.ProbeForward 在 capability 校验后调用。
//
// 与普通 forwarder 的区别：
//   - 不写 usage_log（recorder 完全不参与）
//   - 不扣用户余额
//   - 不消耗用户配额
//   - 不走 RPM/并发/window-cost 限流（探测请求不应被限流挡掉，否则失去意义）
//   - 仍然 scheduler.ReportResult，让真实流量和探测共同驱动账号状态机
//
// 失败语义：所有错误都不通过 gRPC error 返回，而是写入 response.error_kind/msg。
// 调用方（探测插件）需要把 error_kind 持久化到自己的 group_health_probes 表。
func (h *HostService) probeForward(ctx context.Context, req hostProbeForwardRequest) (map[string]interface{}, error) {
	start := time.Now()
	resp := map[string]interface{}{}

	if req.GroupID <= 0 {
		return errProbeResp("invalid_arg", "group_id 必须 > 0", start), nil
	}

	g, err := h.db.Group.Get(ctx, int(req.GroupID))
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, cerr
		}
		if ent.IsNotFound(err) {
			return errProbeResp("group_not_found", err.Error(), start), nil
		}
		return errProbeResp("internal", err.Error(), start), nil
	}
	resp["platform"] = g.Platform

	model := req.Model
	if model == "" {
		if models := h.manager.GetModels(g.Platform); len(models) > 0 {
			model = pickProbeModel(models)
		}
	}
	if model == "" {
		return errProbeResp("no_model", fmt.Sprintf("platform %s 没有可用 model", g.Platform), start), nil
	}
	resp["model"] = model

	// 调度选号
	acc, err := h.scheduler.SelectAccount(ctx, g.Platform, model, 0, int(req.GroupID), "")
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, cerr
		}
		return errProbeResp("no_account", err.Error(), start), nil
	}
	resp["account_id"] = int64(acc.ID)

	// 加载完整账号 + proxy
	accFull, err := h.db.Account.Query().
		Where(account.IDEQ(acc.ID)).
		WithProxy().
		Only(ctx)
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, cerr
		}
		return errProbeResp("internal", "加载账号失败: "+err.Error(), start), nil
	}

	inst := h.manager.GetPluginByPlatform(g.Platform)
	if inst == nil || inst.Gateway == nil {
		return errProbeResp("plugin_missing", "platform "+g.Platform+" 没有可用插件", start), nil
	}

	// 构造最小探测请求：固定 prompt "hi"，stream=false（无需 Writer，结果通过 Body 返回）
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"stream":     false,
		"max_tokens": 5,
	})

	fwdReq := &sdk.ForwardRequest{
		Account: &sdk.Account{
			ID:          int64(accFull.ID),
			Name:        accFull.Name,
			Platform:    accFull.Platform,
			Type:        accFull.Type,
			Credentials: cloneStringMapHost(accFull.Credentials),
			ProxyURL:    proxyURLFromAccount(accFull),
		},
		Body: body,
		Headers: http.Header{
			"Content-Type":       {"application/json"},
			"X-Airgate-Internal": {"probe"},
		},
		Model:  model,
		Stream: false,
	}

	// 调用插件，限制最长 30s（探测不应卡住调度循环）
	fwdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	outcome, fwdErr := inst.Gateway.Forward(fwdCtx, fwdReq)
	latency := time.Since(start)
	resp["latency_ms"] = latency.Milliseconds()
	resp["status_code"] = int64(outcome.Upstream.StatusCode)

	// 插件自身故障（进程异常等）—— 不经过状态机，仅记录。
	if fwdErr != nil {
		resp["success"] = false
		resp["error_kind"] = "plugin_error"
		resp["error_msg"] = truncateProbeErr(fwdErr.Error())
		return resp, nil
	}

	// 探测成功时通知状态机，让降级账号有机会恢复；探测失败时不触发降级，
	// 避免探测模型不可用（如上游缺通道）误伤整个账号的可调度性。
	// 失败信号由 health 插件自行记录到 group_health_probes，不经过账号状态机。
	if outcome.Kind.IsSuccess() {
		h.scheduler.Apply(ctx, acc.ID, scheduler.Judgment{
			Kind:     outcome.Kind,
			Duration: latency,
			IsPool:   accFull.UpstreamIsPool,
			Family:   h.resolveModelFamily(accFull.Platform, model),
		})
	}

	switch outcome.Kind {
	case sdk.OutcomeSuccess:
		resp["success"] = true
	case sdk.OutcomeAccountRateLimited:
		resp["success"] = false
		resp["error_kind"] = "rate_limited"
		resp["error_msg"] = truncateProbeErr(outcome.Reason)
	case sdk.OutcomeAccountDead:
		resp["success"] = false
		resp["error_kind"] = "account_error"
		resp["error_msg"] = truncateProbeErr(outcome.Reason)
	case sdk.OutcomeUpstreamTransient, sdk.OutcomeStreamAborted:
		resp["success"] = false
		resp["error_kind"] = "upstream_5xx"
		resp["error_msg"] = truncateProbeErr(outcome.Reason)
	case sdk.OutcomeClientError:
		resp["success"] = false
		resp["error_kind"] = "client_error"
		resp["error_msg"] = truncateProbeErr(outcome.Reason)
	default:
		resp["success"] = false
		resp["error_kind"] = "unknown"
		resp["error_msg"] = truncateProbeErr(outcome.Reason)
	}
	return resp, nil
}

// hostListGroupsRequest groups.list 的可选过滤参数。
// 空 payload（旧调用方）等价于"列出全部分组"，保持向后兼容。
type hostListGroupsRequest struct {
	// PublicOnly=true 时按状态页可见性过滤：仅返回 status_visible=true 的分组；
	// 若同时传 UserID>0，追加该用户在 user_allowed_groups 里被授权的专属分组。
	// 可见性/授权判断留在 core——插件不应自行查 core 表实现这类过滤。
	PublicOnly bool  `json:"public_only"`
	UserID     int64 `json:"user_id"`
}

// listGroups 列出分组（默认全部；支持状态页可见性过滤）。
func (h *HostService) listGroups(ctx context.Context, req hostListGroupsRequest) (map[string]interface{}, error) {
	slog.Debug("host_service_list_groups", "module", "host",
		"public_only", req.PublicOnly, "user_id", req.UserID)
	q := h.db.Group.Query()
	if req.PublicOnly {
		if req.UserID > 0 {
			q = q.Where(group.Or(
				group.StatusVisible(true),
				group.HasAllowedUsersWith(user.ID(int(req.UserID))),
			))
		} else {
			q = q.Where(group.StatusVisible(true))
		}
	}
	groups, err := q.All(ctx)
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, cerr
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]map[string]interface{}, 0, len(groups))
	for _, g := range groups {
		items = append(items, map[string]interface{}{
			"id":              int64(g.ID),
			"name":            g.Name,
			"platform":        g.Platform,
			"is_exclusive":    g.IsExclusive,
			"rate_multiplier": g.RateMultiplier,
			"note":            g.Note,
			"status_visible":  g.StatusVisible,
		})
	}
	return map[string]interface{}{"groups": items}, nil
}

// reportAccountResult 把账号调用结果反馈给 scheduler。
// 内部 worker，由 pluginHostHandle.ReportAccountResult 委托。
//
// success=true 直接走 Apply(OutcomeSuccess)；success=false 按"上游抖动"上报
// （由状态机的滚动窗口计数决定是否升级为 disabled），避免探测插件单次失败
// 就把账号标死。
func (h *HostService) reportAccountResult(ctx context.Context, req hostReportAccountResultRequest) (map[string]interface{}, error) {
	if req.AccountID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id 必须 > 0")
	}
	kind := sdk.OutcomeUpstreamTransient
	if req.Success {
		kind = sdk.OutcomeSuccess
	}
	h.scheduler.Apply(ctx, int(req.AccountID), scheduler.Judgment{
		Kind:   kind,
		Reason: req.ErrorMsg,
	})
	return map[string]interface{}{"ok": true}, nil
}

// forward 非流式业务转发：调度 → 网关 → 计费 → 记录。
// 与 probeForward 的区别：走完整计费管线，不跳过 usage_log / 余额扣款。
// 账号级故障自动 failover，最多 maxHostForwardAttempts 次。
func (h *HostService) forward(ctx context.Context, req hostForwardRequest) (map[string]interface{}, error) {
	if req.UserID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}
	if err := h.checkHostForwardBalance(ctx, req.UserID); err != nil {
		return nil, err
	}

	routes, userEmail, err := h.hostForwardRoutes(ctx, req)
	if err != nil {
		return nil, err
	}
	fwdCtx, cancel := context.WithTimeout(ctx, hostForwardTimeout(h.manager, req))
	defer cancel()

	hardExclude := make([]int, 0, maxHostForwardAttempts*len(routes))
	var lastUpstream sdk.UpstreamResponse
	hasLastUpstream := false
	for _, route := range routes {
		model := h.resolveHostModel(route.Platform, req.Model)
		if model == "" {
			slog.Warn("host_forward_no_model",
				sdk.LogFieldPlatform, route.Platform, sdk.LogFieldGroupID, route.GroupID)
			continue
		}
		inst := h.manager.GetPluginByPlatform(route.Platform)
		if inst == nil || inst.Gateway == nil {
			slog.Warn("host_forward_no_plugin",
				sdk.LogFieldPlatform, route.Platform, sdk.LogFieldGroupID, route.GroupID)
			continue
		}

		for attempt := 0; attempt < maxHostForwardAttempts; attempt++ {
			acc, err := h.scheduler.SelectAccountWithRequirements(ctx, route.Platform, model, 0, route.GroupID, "", hostAccountRequirements(h.manager, req), hardExclude...)
			if err != nil {
				if cerr := hostContextError(err); cerr != nil {
					return nil, cerr
				}
				slog.Warn("host_forward_pick_account_failed",
					sdk.LogFieldPlatform, route.Platform,
					sdk.LogFieldModel, model,
					sdk.LogFieldGroupID, route.GroupID,
					"effective_rate", route.EffectiveRate,
					sdk.LogFieldError, err,
				)
				break
			}

			accFull, err := h.db.Account.Query().Where(account.IDEQ(acc.ID)).WithProxy().Only(ctx)
			if err != nil {
				if cerr := hostContextError(err); cerr != nil {
					return nil, cerr
				}
				slog.Error("host_forward_account_load_failed",
					sdk.LogFieldAccountID, acc.ID, sdk.LogFieldError, err)
				return nil, hostForwardGenericError()
			}

			headers := hostForwardHeaders(req, route)
			applyAccountCapabilityHeaders(headers, accFull)
			fwdReq := &sdk.ForwardRequest{
				Account: hostSDKAccount(accFull),
				Body:    hostForwardBody(req.Body),
				Headers: headers,
				Model:   model,
				Stream:  false,
			}

			start := time.Now()
			outcome, fwdErr := inst.Gateway.Forward(fwdCtx, fwdReq)
			duration := time.Since(start)
			h.applyHostOutcome(ctx, acc.ID, accFull, model, outcome, duration)
			if returnableUpstream(outcome.Upstream) {
				lastUpstream = outcome.Upstream
				hasLastUpstream = true
			}
			if cerr := hostContextError(fwdErr); cerr != nil {
				return nil, cerr
			}

			if fwdErr != nil || outcome.Kind.ShouldFailover() {
				slog.Warn("host_forward_attempt_failed",
					sdk.LogFieldGroupID, route.GroupID,
					"effective_rate", route.EffectiveRate,
					sdk.LogFieldAccountID, acc.ID,
					"attempt", attempt+1,
					"kind", outcome.Kind,
					sdk.LogFieldReason, outcome.Reason,
					sdk.LogFieldError, fwdErr,
				)
				hardExclude = append(hardExclude, acc.ID)
				continue
			}

			if outcome.Kind == sdk.OutcomeClientError {
				slog.Warn("host_forward_client_error",
					sdk.LogFieldGroupID, route.GroupID,
					sdk.LogFieldAccountID, acc.ID,
					sdk.LogFieldStatus, outcome.Upstream.StatusCode,
					sdk.LogFieldReason, outcome.Reason,
				)
				if returnableUpstream(outcome.Upstream) {
					return hostForwardPayload(outcome), nil
				}
				return nil, hostForwardClientError(outcome)
			}
			if outcome.Kind != sdk.OutcomeSuccess {
				slog.Warn("host_forward_outcome_failed",
					sdk.LogFieldGroupID, route.GroupID,
					sdk.LogFieldAccountID, acc.ID,
					"kind", outcome.Kind,
					sdk.LogFieldReason, outcome.Reason,
				)
				if returnableUpstream(outcome.Upstream) {
					return hostForwardPayload(outcome), nil
				}
				break
			}

			resp := hostForwardPayload(outcome)

			if outcome.Usage != nil {
				if usageID, err := h.recordHostForwardUsage(ctx, req, route, acc.ID, route.Platform, model, accFull, userEmail, outcome, duration); err != nil {
					slog.Error("host_forward_record_usage_failed",
						sdk.LogFieldUserID, req.UserID,
						sdk.LogFieldAccountID, acc.ID,
						sdk.LogFieldError, err,
					)
				} else if usageID > 0 {
					resp["usage_id"] = usageID
				}
				resp["usage"] = outcome.Usage
			}

			return resp, nil
		}
	}

	if hasLastUpstream {
		return hostForwardPayload(sdk.ForwardOutcome{Upstream: lastUpstream}), nil
	}
	return nil, hostForwardGenericError()
}

// forwardStream 流式业务转发。
// 账号级故障自动 failover：通过 failoverStreamWriter 延迟提交，
// 成功（< 400）时立即切换到真流式，失败时缓冲数据后丢弃重试。
func (h *HostService) forwardStream(ctx context.Context, req hostForwardRequest, stream pb.CoreInvokeService_InvokeStreamServer) error {
	if req.UserID <= 0 {
		return status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}
	if err := h.checkHostForwardBalance(ctx, req.UserID); err != nil {
		return err
	}

	routes, userEmail, err := h.hostForwardRoutes(ctx, req)
	if err != nil {
		return err
	}
	fwdCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()

	sw := &hostStreamWriter{stream: stream}
	hardExclude := make([]int, 0, maxHostForwardAttempts*len(routes))

	for _, route := range routes {
		model := h.resolveHostModel(route.Platform, req.Model)
		if model == "" {
			slog.Warn("host_forward_stream_no_model",
				sdk.LogFieldPlatform, route.Platform, sdk.LogFieldGroupID, route.GroupID)
			continue
		}
		inst := h.manager.GetPluginByPlatform(route.Platform)
		if inst == nil || inst.Gateway == nil {
			slog.Warn("host_forward_stream_no_plugin",
				sdk.LogFieldPlatform, route.Platform, sdk.LogFieldGroupID, route.GroupID)
			continue
		}

		for attempt := 0; attempt < maxHostForwardAttempts; attempt++ {
			acc, err := h.scheduler.SelectAccountWithRequirements(ctx, route.Platform, model, 0, route.GroupID, "", hostAccountRequirements(h.manager, req), hardExclude...)
			if err != nil {
				if cerr := hostContextError(err); cerr != nil {
					return cerr
				}
				slog.Warn("host_forward_stream_pick_account_failed",
					sdk.LogFieldPlatform, route.Platform,
					sdk.LogFieldModel, model,
					sdk.LogFieldGroupID, route.GroupID,
					"effective_rate", route.EffectiveRate,
					sdk.LogFieldError, err,
				)
				break
			}

			accFull, err := h.db.Account.Query().Where(account.IDEQ(acc.ID)).WithProxy().Only(ctx)
			if err != nil {
				if cerr := hostContextError(err); cerr != nil {
					return cerr
				}
				slog.Error("host_forward_stream_account_load_failed",
					sdk.LogFieldAccountID, acc.ID, sdk.LogFieldError, err)
				return hostForwardGenericError()
			}

			fw := &failoverStreamWriter{target: sw}
			headers := hostForwardHeaders(req, route)
			applyAccountCapabilityHeaders(headers, accFull)
			fwdReq := &sdk.ForwardRequest{
				Account: hostSDKAccount(accFull),
				Body:    hostForwardBody(req.Body),
				Headers: headers,
				Model:   model,
				Stream:  true,
				Writer:  fw,
			}

			start := time.Now()
			outcome, fwdErr := inst.Gateway.Forward(fwdCtx, fwdReq)
			duration := time.Since(start)
			h.applyHostOutcome(ctx, acc.ID, accFull, model, outcome, duration)
			if cerr := hostContextError(fwdErr); cerr != nil {
				return cerr
			}

			canRetry := !fw.committed && (fwdErr != nil || outcome.Kind.ShouldFailover())
			if canRetry {
				slog.Warn("host_forward_stream_attempt_failed",
					sdk.LogFieldGroupID, route.GroupID,
					"effective_rate", route.EffectiveRate,
					sdk.LogFieldAccountID, acc.ID,
					"attempt", attempt+1,
					"kind", outcome.Kind,
					sdk.LogFieldReason, outcome.Reason,
					sdk.LogFieldError, fwdErr,
				)
				hardExclude = append(hardExclude, acc.ID)
				continue
			}

			if outcome.Kind == sdk.OutcomeClientError {
				slog.Warn("host_forward_stream_client_error",
					sdk.LogFieldGroupID, route.GroupID,
					sdk.LogFieldAccountID, acc.ID,
					sdk.LogFieldStatus, outcome.Upstream.StatusCode,
					sdk.LogFieldReason, outcome.Reason,
				)
				return hostForwardClientError(outcome)
			}

			if !fw.committed {
				fw.flush()
			}

			if outcome.Kind != sdk.OutcomeSuccess && fwdErr == nil {
				slog.Warn("host_forward_stream_committed_failure",
					sdk.LogFieldGroupID, route.GroupID,
					"effective_rate", route.EffectiveRate,
					sdk.LogFieldAccountID, acc.ID,
					"kind", outcome.Kind,
					sdk.LogFieldStatus, outcome.Upstream.StatusCode,
					sdk.LogFieldReason, outcome.Reason,
					"stream_committed", fw.committed,
				)
			}

			if fwdErr != nil {
				slog.Warn("host_forward_stream_plugin_error",
					sdk.LogFieldGroupID, route.GroupID,
					sdk.LogFieldAccountID, acc.ID,
					sdk.LogFieldError, fwdErr,
				)
				return hostForwardGenericError()
			}

			var usage *sdk.Usage
			if outcome.Usage != nil {
				if _, err := h.recordHostForwardUsage(ctx, req, route, acc.ID, route.Platform, model, accFull, userEmail, outcome, duration); err != nil {
					slog.Error("host_forward_stream_record_usage_failed",
						sdk.LogFieldUserID, req.UserID,
						sdk.LogFieldAccountID, acc.ID,
						sdk.LogFieldError, err,
					)
				}
				usage = outcome.Usage
			}

			return stream.Send(&pb.HostStreamFrame{
				Event:  "done",
				Status: "ok",
				Payload: mustHostPayload(map[string]interface{}{
					"usage": usage,
				}),
				Done: true,
			})
		}
	}

	return hostForwardGenericError()
}

// maxHostForwardAttempts 最大 failover 次数，与 Forwarder 保持一致。
const (
	maxHostForwardAttempts    = 3
	defaultHostForwardTimeout = 120 * time.Second
	imageHostForwardTimeout   = 300 * time.Second
)

func hostForwardTimeout(mgr *Manager, req hostForwardRequest) time.Duration {
	if requestHasImageWorkload(mgr, req.Path, req.Model, hostForwardBody(req.Body)) {
		return imageHostForwardTimeout
	}
	return defaultHostForwardTimeout
}

// failoverStreamWriter 包装 hostStreamWriter，支持 failover 重试。
// 成功响应（StatusCode < 400）立即提交到真正的 gRPC stream，实现真流式；
// 错误响应缓冲数据，允许调用方丢弃后重试下一个账号。
type failoverStreamWriter struct {
	target    *hostStreamWriter
	committed bool
	bufStatus int
	bufHdr    http.Header
	bufData   [][]byte
}

func (w *failoverStreamWriter) Header() http.Header {
	if w.committed {
		return w.target.Header()
	}
	if w.bufHdr == nil {
		w.bufHdr = make(http.Header)
	}
	return w.bufHdr
}

func (w *failoverStreamWriter) WriteHeader(statusCode int) {
	if w.committed {
		w.target.WriteHeader(statusCode)
		return
	}
	w.bufStatus = statusCode
	if statusCode > 0 && statusCode < 400 {
		w.flush()
	}
}

func (w *failoverStreamWriter) Write(data []byte) (int, error) {
	if w.committed {
		return w.target.Write(data)
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	w.bufData = append(w.bufData, buf)
	return len(data), nil
}

func (w *failoverStreamWriter) Flush() {
	if w.committed {
		w.target.Flush()
	}
}

func (w *failoverStreamWriter) flush() {
	if w.committed {
		return
	}
	w.committed = true
	for k, v := range w.bufHdr {
		w.target.Header()[k] = v
	}
	if w.bufStatus > 0 {
		w.target.WriteHeader(w.bufStatus)
	}
	for _, d := range w.bufData {
		if _, err := w.target.Write(d); err != nil {
			return
		}
	}
	w.bufData = nil
}

// hostStreamWriter 适配 http.ResponseWriter，将流式数据转为 gRPC stream chunks。
type hostStreamWriter struct {
	stream     pb.CoreInvokeService_InvokeStreamServer
	headerSent bool
	header     http.Header
	statusCode int
}

func (w *hostStreamWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *hostStreamWriter) WriteHeader(statusCode int) {
	if w.headerSent {
		return
	}
	w.statusCode = statusCode
	w.headerSent = true
	_ = w.stream.Send(&pb.HostStreamFrame{
		Event:  "headers",
		Status: "ok",
		Payload: mustHostPayload(map[string]interface{}{
			"status_code": statusCode,
			"headers":     httpHeadersToProtoHost(w.header),
		}),
	})
}

func (w *hostStreamWriter) Write(data []byte) (int, error) {
	if !w.headerSent {
		w.WriteHeader(http.StatusOK)
	}
	if len(data) == 0 {
		return 0, nil
	}
	chunk := make([]byte, len(data))
	copy(chunk, data)
	if err := w.stream.Send(&pb.HostStreamFrame{
		Event: "chunk",
		Payload: mustHostPayload(map[string]interface{}{
			"data": string(chunk),
		}),
	}); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *hostStreamWriter) Flush() {}

// recordHostForwardUsage 为 Host gateway.forward 调用发起的请求记录 usage_log 并扣费。
// 与 forwarder.recordUsage 的区别：没有 APIKeyInfo，APIKeyID=0。
func (h *HostService) recordHostForwardUsage(
	ctx context.Context,
	req hostForwardRequest,
	route routing.Candidate,
	accountID int,
	platform, model string,
	accFull *ent.Account,
	userEmail string,
	outcome sdk.ForwardOutcome,
	duration time.Duration,
) (int, error) {
	usage := outcome.Usage
	if usage == nil {
		return 0, nil
	}
	usageValues := usageSnapshotFromSDK(usage)

	calcInput := billing.CalculateInput{
		InputCost:         usageValues.InputCost,
		ImageInputCost:    usageValues.ImageInputCost,
		OutputCost:        usageValues.OutputCost,
		CachedInputCost:   usageValues.CachedInputCost,
		CacheCreationCost: usageValues.CacheCreationCost,
		ImageCost:         usageValues.ImageCost,
		BillingRate:       route.EffectiveRate,
		AccountRate:       accFull.RateMultiplier,
	}
	var imageFixedPriceApplied bool
	var imageFixedPriceReplacesTotal bool
	if override, ok := imageOutputBillingOverride(usage, route.UserPluginSettings, route.GroupPluginSettings); ok {
		calcInput.ImageBillingCostOverride = &override.cost
		calcInput.ImageBillingCostOverrideReplacesTotal = override.replacesTotal
		imageFixedPriceApplied = true
		imageFixedPriceReplacesTotal = override.replacesTotal
	}
	calc := h.calculator.Calculate(calcInput)

	h.scheduler.AddWindowCost(ctx, accountID, calc.AccountCost)

	actualModel := usage.Model
	if actualModel == "" {
		actualModel = model
	}

	record := billing.UsageRecord{
		UserID:                       int(req.UserID),
		UserEmail:                    userEmail,
		APIKeyID:                     int(req.APIKeyID),
		AccountID:                    accountID,
		GroupID:                      route.GroupID,
		Platform:                     platform,
		Model:                        actualModel,
		InputTokens:                  usageValues.InputTokens,
		OutputTokens:                 usageValues.OutputTokens,
		CachedInputTokens:            usageValues.CachedInputTokens,
		CacheCreationTokens:          usageValues.CacheCreationTokens,
		CacheCreation5mTokens:        usageValues.CacheCreation5mTokens,
		CacheCreation1hTokens:        usageValues.CacheCreation1hTokens,
		ReasoningOutputTokens:        usageValues.ReasoningOutputTokens,
		InputPrice:                   usageValues.InputPrice,
		OutputPrice:                  usageValues.OutputPrice,
		CachedInputPrice:             usageValues.CachedInputPrice,
		CacheCreationPrice:           usageValues.CacheCreationPrice,
		CacheCreation1hPrice:         usageValues.CacheCreation1hPrice,
		InputCost:                    calc.InputCost,
		OutputCost:                   calc.OutputCost,
		CachedInputCost:              calc.CachedInputCost,
		CacheCreationCost:            calc.CacheCreationCost,
		ImageCost:                    calc.ImageCost,
		ImageFixedPriceApplied:       imageFixedPriceApplied,
		ImageFixedPriceReplacesTotal: imageFixedPriceReplacesTotal,
		TotalCost:                    calc.TotalCost,
		ActualCost:                   calc.ActualCost,
		BilledCost:                   calc.BilledCost,
		AccountCost:                  calc.AccountCost,
		RateMultiplier:               calc.RateMultiplier,
		AccountRateMultiplier:        calc.AccountRateMultiplier,
		ServiceTier:                  usageValues.ServiceTier,
		ImageSize:                    usageValues.ImageSize,
		Endpoint:                     req.Path,
		ReasoningEffort:              resolveReasoningEffort(hostForwardReasoningEffort(req), usage),
		Stream:                       req.Stream,
		DurationMs:                   duration.Milliseconds(),
		FirstTokenMs:                 usageValues.FirstTokenMs,
		UsageAttributes:              usage.Attributes,
		UsageMetrics:                 usage.Metrics,
		UsageCostDetails:             usage.CostDetails,
		UsageMetadata:                usage.Metadata,
	}
	if h.recorder == nil {
		return 0, nil
	}
	return h.recorder.RecordSync(ctx, record)
}

// listPlatforms 列出已加载的网关平台。
func (h *HostService) listPlatforms(_ context.Context) (map[string]interface{}, error) {
	metas := h.manager.GetAllPluginMeta()
	seen := make(map[string]struct{}, len(metas))
	platforms := make([]map[string]interface{}, 0, len(metas))
	for _, m := range metas {
		if m.Type != "gateway" || m.Platform == "" {
			continue
		}
		if _, ok := seen[m.Platform]; ok {
			continue
		}
		seen[m.Platform] = struct{}{}
		platforms = append(platforms, map[string]interface{}{
			"name":         m.Platform,
			"display_name": m.DisplayName,
		})
	}
	return map[string]interface{}{"platforms": platforms}, nil
}

// listModels 列出指定平台的模型列表。
func (h *HostService) listModels(_ context.Context, req hostListModelsRequest) (map[string]interface{}, error) {
	if req.Platform == "" {
		return nil, status.Error(codes.InvalidArgument, "platform 不能为空")
	}
	models := h.manager.GetModels(req.Platform)
	items := make([]map[string]interface{}, 0, len(models))
	for _, m := range models {
		items = append(items, map[string]interface{}{
			"id":                m.ID,
			"name":              m.Name,
			"context_window":    int64(m.ContextWindow),
			"max_output_tokens": int64(m.MaxOutputTokens),
			"capabilities":      m.Capabilities,
			"metadata":          m.Metadata,
		})
	}
	return map[string]interface{}{"models": items}, nil
}

// getUserInfo 获取用户基本信息。
func (h *HostService) getUserInfo(ctx context.Context, req hostGetUserInfoRequest) (map[string]interface{}, error) {
	if req.UserID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}
	u, err := h.db.User.Get(ctx, int(req.UserID))
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "用户不存在")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{
		"user_id":  int64(u.ID),
		"username": u.Username,
		"email":    u.Email,
		"role":     string(u.Role),
		"balance":  u.Balance,
		"status":   string(u.Status),
	}, nil
}

func (h *HostService) storeAsset(ctx context.Context, req hostStoreAssetRequest) (map[string]interface{}, error) {
	if req.UserID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}
	if len(req.Data) == 0 {
		return nil, status.Error(codes.InvalidArgument, "asset data is required")
	}
	purpose, ok := parseAssetPurpose(req.Purpose)
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid purpose %q (allowed: chat/upload/generated/task-input/temp)", req.Purpose)
	}
	storage, err := NewAssetStorage(ctx, h.db)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	asset, err := storage.Store(ctx, req.UserID, purpose, req.ContentType, req.FileExtension, req.Data)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{
		"asset_id":     asset.ID,
		"object_key":   asset.ObjectKey,
		"public_url":   asset.PublicURL,
		"size_bytes":   asset.SizeBytes,
		"content_type": asset.ContentType,
	}, nil
}

func (h *HostService) storeAssetFromURL(ctx context.Context, req hostStoreAssetFromURLRequest) (map[string]interface{}, error) {
	if req.UserID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}
	if req.SourceURL == "" {
		return nil, status.Error(codes.InvalidArgument, "source_url is required")
	}
	purpose, ok := parseAssetPurpose(req.Purpose)
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid purpose %q (allowed: chat/upload/generated/task-input/temp)", req.Purpose)
	}
	storage, err := NewAssetStorage(ctx, h.db)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	asset, err := storage.StoreFromURL(ctx, req.UserID, purpose, req.SourceURL)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{
		"asset_id":     asset.ID,
		"object_key":   asset.ObjectKey,
		"public_url":   asset.PublicURL,
		"size_bytes":   asset.SizeBytes,
		"content_type": asset.ContentType,
	}, nil
}

func (h *HostService) getAssetURL(ctx context.Context, req hostGetAssetURLRequest) (map[string]interface{}, error) {
	storage, err := NewAssetStorage(ctx, h.db)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	publicURL, err := storage.PublicURL(ctx, req.ObjectKey)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{"public_url": publicURL}, nil
}

func (h *HostService) getAssetBytes(ctx context.Context, req hostGetAssetBytesRequest) (map[string]interface{}, error) {
	storage, err := NewAssetStorage(ctx, h.db)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	data, contentType, err := storage.GetBytes(ctx, req.ObjectKey)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{"data": data, "content_type": contentType}, nil
}

func (h *HostService) deleteAsset(ctx context.Context, req hostDeleteAssetRequest) (map[string]interface{}, error) {
	if req.ObjectKey == "" {
		return nil, status.Error(codes.InvalidArgument, "object_key 不能为空")
	}
	storage, err := NewAssetStorage(ctx, h.db)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := storage.Delete(ctx, req.ObjectKey); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return map[string]interface{}{"deleted": true}, nil
}

// protoHeadersToHTTPHost / httpHeadersToProtoHost 是 host_service.go 内部的 header 转换。
// 与 grpc/gateway_server.go 的同名函数等价，但跨包引用会引入循环依赖。
func (h *HostService) hostForwardRoutes(ctx context.Context, req hostForwardRequest) ([]routing.Candidate, string, error) {
	if req.GroupID > 0 {
		u, err := h.db.User.Query().Where(user.IDEQ(int(req.UserID))).Only(ctx)
		if err != nil {
			if cerr := hostContextError(err); cerr != nil {
				return nil, "", cerr
			}
			slog.Error("host_forward_user_lookup_failed",
				sdk.LogFieldUserID, req.UserID, sdk.LogFieldError, err)
			return nil, "", hostForwardGenericError()
		}
		g, err := h.db.Group.Get(ctx, int(req.GroupID))
		if err != nil {
			if cerr := hostContextError(err); cerr != nil {
				return nil, "", cerr
			}
			if ent.IsNotFound(err) {
				return nil, "", status.Error(codes.NotFound, "分组不存在")
			}
			slog.Error("host_forward_group_lookup_failed",
				sdk.LogFieldGroupID, req.GroupID, sdk.LogFieldError, err)
			return nil, "", hostForwardGenericError()
		}
		if !routing.GroupMatchesRequirements(g, hostForwardRequirements(h.manager, req)) {
			slog.Warn("host_forward_group_requirement_unmet",
				sdk.LogFieldGroupID, req.GroupID,
				sdk.LogFieldModel, req.Model,
				sdk.LogFieldPath, req.Path,
			)
			return nil, "", hostForwardGenericError()
		}
		return []routing.Candidate{{
			GroupID:                g.ID,
			Platform:               g.Platform,
			EffectiveRate:          billing.ResolveBillingRateForGroup(u.GroupRates, g.ID, g.RateMultiplier),
			GroupRateMultiplier:    g.RateMultiplier,
			GroupServiceTier:       g.ServiceTier,
			GroupForceInstructions: g.ForceInstructions,
			GroupPluginSettings:    clonePluginSettingsHost(g.PluginSettings),
			UserPluginSettings:     clonePluginSettingsHost(u.GroupPluginSettings[int64(g.ID)]),
			SortWeight:             g.SortWeight,
		}}, u.Email, nil
	}

	platform := protoHeadersToHTTPHost(req.Headers).Get("X-Airgate-Platform")
	if platform == "" && req.Model != "" {
		platform = h.manager.FindPlatformByModel(req.Model)
	}
	if platform == "" {
		return nil, "", status.Error(codes.InvalidArgument, "platform 不能为空")
	}
	u, err := h.db.User.Query().Where(user.IDEQ(int(req.UserID))).Only(ctx)
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, "", cerr
		}
		slog.Error("host_forward_user_lookup_failed",
			sdk.LogFieldUserID, req.UserID, sdk.LogFieldError, err)
		return nil, "", hostForwardGenericError()
	}
	routes, err := routing.ListEligibleGroups(ctx, h.db, int(req.UserID), platform, u.GroupRates, u.GroupPluginSettings, hostForwardRequirements(h.manager, req))
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return nil, "", cerr
		}
		slog.Error("host_forward_routes_lookup_failed",
			sdk.LogFieldPlatform, platform,
			sdk.LogFieldUserID, req.UserID,
			sdk.LogFieldError, err,
		)
		return nil, "", hostForwardGenericError()
	}
	if len(routes) == 0 {
		slog.Warn("host_forward_no_eligible_route",
			sdk.LogFieldPlatform, platform,
			sdk.LogFieldUserID, req.UserID,
		)
		return nil, "", hostForwardGenericError()
	}
	return routes, u.Email, nil
}

func hostForwardRequirements(mgr *Manager, req hostForwardRequest) routing.Requirements {
	return routing.Requirements{NeedsImage: requestNeedsImage(mgr, req.Path, req.Model, hostForwardBody(req.Body))}
}

func hostAccountRequirements(mgr *Manager, req hostForwardRequest) scheduler.AccountRequirements {
	return accountRequirementsForRequest(mgr, req.Path, req.Model, hostForwardBody(req.Body))
}

func hostForwardReasoningEffort(req hostForwardRequest) string {
	return parseBody(hostForwardBody(req.Body), protoHeadersToHTTPHost(req.Headers).Get("Content-Type")).ReasoningEffort
}

func (h *HostService) resolveHostModel(platform, model string) string {
	if model != "" {
		return model
	}
	models := h.manager.GetModels(platform)
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}

func hostForwardHeaders(req hostForwardRequest, route routing.Candidate) http.Header {
	headers := protoHeadersToHTTPHost(req.Headers)
	headers.Set("X-Forwarded-Path", req.Path)
	headers.Set("X-Forwarded-Method", req.Method)
	headers.Set("X-Airgate-Internal", "host-forward")
	if req.UserID > 0 {
		headers.Set("X-Airgate-User-ID", strconv.FormatInt(req.UserID, 10))
	}
	if route.GroupID > 0 {
		headers.Set("X-Airgate-Group-ID", strconv.Itoa(route.GroupID))
	}
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}
	if route.GroupServiceTier != "" {
		headers.Set("X-Airgate-Service-Tier", route.GroupServiceTier)
	}
	if route.GroupForceInstructions != "" {
		headers.Set("X-Airgate-Force-Instructions", route.GroupForceInstructions)
	}
	for plugin, kv := range route.GroupPluginSettings {
		for k, v := range kv {
			if v == "" || !shouldForwardPluginSetting(plugin, k) {
				continue
			}
			headers.Set("X-Airgate-Plugin-"+canonicalHeaderToken(plugin)+"-"+canonicalHeaderToken(k), v)
		}
	}
	return headers
}

func hostSDKAccount(acc *ent.Account) *sdk.Account {
	return &sdk.Account{
		ID:          int64(acc.ID),
		Name:        acc.Name,
		Platform:    acc.Platform,
		Type:        acc.Type,
		Credentials: cloneStringMapHost(acc.Credentials),
		ProxyURL:    proxyURLFromAccount(acc),
	}
}

func (h *HostService) applyHostOutcome(ctx context.Context, accountID int, accFull *ent.Account, model string, outcome sdk.ForwardOutcome, duration time.Duration) {
	reason := outcome.Reason
	if outcome.Kind.IsAccountFault() && model != "" {
		reason = "[" + model + "] " + reason
	}
	h.scheduler.Apply(ctx, accountID, scheduler.Judgment{
		Kind:           outcome.Kind,
		RetryAfter:     outcome.RetryAfter,
		Reason:         reason,
		Duration:       duration,
		IsPool:         accFull.UpstreamIsPool,
		UpstreamStatus: outcome.Upstream.StatusCode,
		Family:         h.resolveModelFamily(accFull.Platform, model),
	})
}

// resolveModelFamily 从插件目录优先获取家族键，未命中时回退到硬编码规则。
func (h *HostService) resolveModelFamily(platform, model string) string {
	if h.manager != nil {
		if family := h.manager.ModelFamily(model); family != "" {
			return family
		}
	}
	return scheduler.ModelFamily(platform, model)
}

func (h *HostService) checkHostForwardBalance(ctx context.Context, userID int64) error {
	u, err := h.db.User.Query().Where(user.IDEQ(int(userID))).Only(ctx)
	if err != nil {
		if cerr := hostContextError(err); cerr != nil {
			return cerr
		}
		if ent.IsNotFound(err) {
			return status.Error(codes.NotFound, "用户不存在")
		}
		slog.Error("host_forward_balance_check_user_lookup_failed",
			sdk.LogFieldUserID, userID, sdk.LogFieldError, err)
		return hostForwardGenericError()
	}
	if u.Balance <= 0 {
		return hostForwardInsufficientQuotaError()
	}
	return nil
}

func hostForwardGenericError() error {
	return status.Error(codes.Unavailable, "请求暂时无法完成，请稍后重试")
}

func hostContextError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return nil
	}
}

func hostForwardClientError(outcome sdk.ForwardOutcome) error {
	return status.Error(codes.InvalidArgument, sanitizedClientErrorMessage(outcome))
}

func hostForwardPayload(outcome sdk.ForwardOutcome) map[string]interface{} {
	return map[string]interface{}{
		"status_code": outcome.Upstream.StatusCode,
		"headers":     httpHeadersToProtoHost(outcome.Upstream.Headers),
		"body":        string(outcome.Upstream.Body),
	}
}

func hostForwardInsufficientQuotaError() error {
	return status.Error(codes.ResourceExhausted, "余额不足")
}

func protoHeadersToHTTPHost(ph map[string]interface{}) http.Header {
	h := make(http.Header, len(ph))
	for k, v := range ph {
		switch values := v.(type) {
		case []string:
			h[k] = append([]string(nil), values...)
		case []interface{}:
			for _, item := range values {
				h.Add(k, fmt.Sprint(item))
			}
		case map[string]interface{}:
			if raw, ok := values["values"]; ok {
				switch vv := raw.(type) {
				case []interface{}:
					for _, item := range vv {
						h.Add(k, fmt.Sprint(item))
					}
				case []string:
					h[k] = append([]string(nil), vv...)
				case string:
					h.Set(k, vv)
				}
			}
		case string:
			h.Set(k, values)
		default:
			if v != nil {
				h.Set(k, fmt.Sprint(v))
			}
		}
	}
	return h
}

func httpHeadersToProtoHost(h http.Header) map[string]interface{} {
	ph := make(map[string]interface{}, len(h))
	for k, v := range h {
		ph[k] = append([]string(nil), v...)
	}
	return ph
}

func hostForwardBody(raw interface{}) []byte {
	switch v := raw.(type) {
	case nil:
		return nil
	case []byte:
		return v
	case string:
		return []byte(v)
	case json.RawMessage:
		return []byte(v)
	default:
		body, _ := json.Marshal(v)
		return body
	}
}

func mustHostPayload(payload map[string]interface{}) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"error":"payload encode failed"}`)
	}
	return data
}

// errProbeResp 构造一个失败的 probe response（不通过 gRPC error 返回，
// 让插件能拿到 latency_ms 和 error_kind 写入自己的 health 表）。
func errProbeResp(kind, msg string, start time.Time) map[string]interface{} {
	return map[string]interface{}{
		"success":    false,
		"error_kind": kind,
		"error_msg":  truncateProbeErr(msg),
		"latency_ms": time.Since(start).Milliseconds(),
	}
}

// pickProbeModel 从模型列表中选一个非图片模型用于探测。
// 图片模型探测需要实际生图（成本高），跳过；如果全是图片模型则返回空。
// 直接使用 ModelInfo.HasCapability 判断，无需经过 Manager 全局查找。
func pickProbeModel(models []sdk.ModelInfo) string {
	for _, m := range models {
		if !m.HasCapability(sdk.ModelCapImageGeneration) {
			return m.ID
		}
	}
	return ""
}

// truncateProbeErr 限制 error_msg 长度，避免巨型上游错误体污染探测表。
func truncateProbeErr(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// cloneStringMapHost / proxyURLFromAccount 是 host_service.go 内部独立的小 helper。
// 与 internal/app/account/service.go 里的同名 helper 重复，但跨包引用 service 层
// 会引入循环依赖（service 层依赖 plugin 包），所以这里复制一份。

func cloneStringMapHost(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for k, v := range input {
		cloned[k] = v
	}
	return cloned
}

func clonePluginSettingsHost(input map[string]map[string]string) map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]map[string]string, len(input))
	for plugin, settings := range input {
		if len(settings) == 0 {
			continue
		}
		cloned[plugin] = cloneStringMapHost(settings)
	}
	return cloned
}

// proxyURLFromAccount 从 ent.Account 的 proxy edge 拼装 proxy URL。
// 与 account.buildProxyURL 等价，但接收 ent.Proxy 而非 service.Proxy。
func proxyURLFromAccount(a *ent.Account) string {
	if a == nil || a.Edges.Proxy == nil {
		return ""
	}
	p := a.Edges.Proxy
	if p.Username != "" {
		return fmt.Sprintf("%s://%s:%s@%s:%d", p.Protocol, p.Username, p.Password, p.Address, p.Port)
	}
	return fmt.Sprintf("%s://%s:%d", p.Protocol, p.Address, p.Port)
}
