package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/ent/user"
	"github.com/DouDOU-start/airgate-core/internal/billing"
	"github.com/DouDOU-start/airgate-core/internal/routing"
	"github.com/DouDOU-start/airgate-core/internal/scheduler"
	sdk "github.com/DouDOU-start/airgate-sdk"
	pb "github.com/DouDOU-start/airgate-sdk/proto"
)

// HostService 是 Core 暴露给插件的反向 gRPC 能力的"底层实现"。
//
// 它本身不做权限校验——所有方法都认任何调用者。真正面向插件的实现是
// pluginHostHandle，它在每个 RPC 入口先做 capability 校验，再委托给本结构。
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
// HostService 自身不实现 pb.HostServiceServer—— 用 NewPluginHandle 给每个插件
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
//  2. 把 handle 作为 HostImpl 注入 GatewayGRPCPlugin / ExtensionGRPCPlugin / MiddlewareGRPCPlugin
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

// pluginHostHandle 是一个 per-plugin 的 HostServiceServer。
//
// 持有一个不可变的 base + 一个可变的 capability set（atomic 保护）。每个 RPC 入口先
// requireCap 再委托。capability set 的写入是 spawn 后由 manager 完成的，写入之后
// 在该插件生命周期内通常不再变（OnConfigUpdate 重新走 Init 时会重新创建 handle）。
type pluginHostHandle struct {
	pb.UnimplementedHostServiceServer

	base       *HostService
	pluginName string

	// caps 指针指向一个 map[sdk.Capability]bool。nil = "未授权状态"，所有 RPC 都拒绝。
	// 用 atomic.Pointer 是为了让 SetCapabilities 与 RPC 处理并发安全，无需 mutex。
	caps atomic.Pointer[map[sdk.Capability]bool]
}

// SetCapabilities 由 Manager 在 spawn 完成、Info() 拿到 capability 列表后调用。
//
// nil caps == 兼容模式（豁免校验），用于 sdk_version <= 0.2.x 的存量插件。
// 空 set（len=0）== 显式声明"什么都不要"，所有 RPC 都被拒。
func (h *pluginHostHandle) SetCapabilities(caps map[sdk.Capability]bool) {
	if caps == nil {
		h.caps.Store(nil)
		return
	}
	cloned := make(map[sdk.Capability]bool, len(caps))
	for k, v := range caps {
		cloned[k] = v
	}
	h.caps.Store(&cloned)
}

// requireCap 检查当前插件是否拥有 capability cap。
//
// 兼容模式（caps==nil）下放行任何调用并 log debug，便于审计哪些老插件依赖什么能力，
// 等老插件全部声明 capabilities 后再去掉兼容路径。
func (h *pluginHostHandle) requireCap(cap sdk.Capability) error {
	caps := h.caps.Load()
	if caps == nil {
		// 兼容模式：sdk_version 豁免的老插件
		slog.Debug("HostService capability 校验跳过（兼容模式）",
			"plugin", h.pluginName, "capability", cap)
		return nil
	}
	if (*caps)[cap] {
		return nil
	}
	// Warn 级别便于运维快速发现"插件代码与声明的 capability 不一致"
	slog.Warn("HostService capability 拒绝",
		"plugin", h.pluginName, "capability", cap)
	return status.Errorf(codes.PermissionDenied,
		"plugin %q lacks capability %q", h.pluginName, cap)
}

func (h *pluginHostHandle) SelectAccount(ctx context.Context, req *pb.HostSelectAccountRequest) (*pb.HostSelectAccountResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostSelectAccount); err != nil {
		return nil, err
	}
	return h.base.selectAccount(ctx, req)
}

func (h *pluginHostHandle) ProbeForward(ctx context.Context, req *pb.HostProbeForwardRequest) (*pb.HostProbeForwardResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostProbeForward); err != nil {
		return nil, err
	}
	return h.base.probeForward(ctx, req)
}

func (h *pluginHostHandle) ListGroups(ctx context.Context, req *pb.HostListGroupsRequest) (*pb.HostListGroupsResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostListGroups); err != nil {
		return nil, err
	}
	return h.base.listGroups(ctx, req)
}

func (h *pluginHostHandle) ReportAccountResult(ctx context.Context, req *pb.HostReportAccountResultRequest) (*pb.Empty, error) {
	if err := h.requireCap(sdk.CapabilityHostReportAccountResult); err != nil {
		return nil, err
	}
	return h.base.reportAccountResult(ctx, req)
}

func (h *pluginHostHandle) Forward(ctx context.Context, req *pb.HostForwardRequest) (*pb.HostForwardResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostForward); err != nil {
		return nil, err
	}
	return h.base.forward(ctx, req)
}

func (h *pluginHostHandle) ForwardStream(req *pb.HostForwardRequest, stream pb.HostService_ForwardStreamServer) error {
	if err := h.requireCap(sdk.CapabilityHostForward); err != nil {
		return err
	}
	return h.base.forwardStream(stream.Context(), req, stream)
}

func (h *pluginHostHandle) ListPlatforms(ctx context.Context, req *pb.HostListPlatformsRequest) (*pb.HostListPlatformsResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostListPlatforms); err != nil {
		return nil, err
	}
	return h.base.listPlatforms(ctx, req)
}

func (h *pluginHostHandle) ListModels(ctx context.Context, req *pb.HostListModelsRequest) (*pb.HostListModelsResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostListModels); err != nil {
		return nil, err
	}
	return h.base.listModels(ctx, req)
}

func (h *pluginHostHandle) GetUserInfo(ctx context.Context, req *pb.HostGetUserInfoRequest) (*pb.HostGetUserInfoResponse, error) {
	if err := h.requireCap(sdk.CapabilityHostGetUserInfo); err != nil {
		return nil, err
	}
	return h.base.getUserInfo(ctx, req)
}

// selectAccount 调度选号：走和真实用户请求完全相同的路径。
// 内部 worker，由 pluginHostHandle.SelectAccount 在 capability 校验后调用。
func (h *HostService) selectAccount(ctx context.Context, req *pb.HostSelectAccountRequest) (*pb.HostSelectAccountResponse, error) {
	if req.GroupId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "group_id 必须 > 0")
	}
	g, err := h.db.Group.Get(ctx, int(req.GroupId))
	if err != nil {
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

	excludeIDs := make([]int, 0, len(req.ExcludeAccountIds))
	for _, id := range req.ExcludeAccountIds {
		excludeIDs = append(excludeIDs, int(id))
	}

	acc, err := h.scheduler.SelectAccount(ctx, g.Platform, model, 0, int(req.GroupId), req.SessionId, excludeIDs...)
	if err != nil {
		// scheduler 自身的"无可用账户"是业务可预期错误，用 NotFound 让插件区分
		if errors.Is(err, scheduler.ErrNoAvailableAccount) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.HostSelectAccountResponse{
		AccountId:   int64(acc.ID),
		AccountName: acc.Name,
		Platform:    acc.Platform,
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
func (h *HostService) probeForward(ctx context.Context, req *pb.HostProbeForwardRequest) (*pb.HostProbeForwardResponse, error) {
	start := time.Now()
	resp := &pb.HostProbeForwardResponse{}

	if req.GroupId <= 0 {
		return errProbeResp("invalid_arg", "group_id 必须 > 0", start), nil
	}

	g, err := h.db.Group.Get(ctx, int(req.GroupId))
	if err != nil {
		if ent.IsNotFound(err) {
			return errProbeResp("group_not_found", err.Error(), start), nil
		}
		return errProbeResp("internal", err.Error(), start), nil
	}
	resp.Platform = g.Platform

	model := req.Model
	if model == "" {
		if models := h.manager.GetModels(g.Platform); len(models) > 0 {
			model = models[0].ID
		}
	}
	if model == "" {
		return errProbeResp("no_model", fmt.Sprintf("platform %s 没有可用 model", g.Platform), start), nil
	}
	resp.Model = model

	// 调度选号
	acc, err := h.scheduler.SelectAccount(ctx, g.Platform, model, 0, int(req.GroupId), "")
	if err != nil {
		return errProbeResp("no_account", err.Error(), start), nil
	}
	resp.AccountId = int64(acc.ID)

	// 加载完整账号 + proxy
	accFull, err := h.db.Account.Query().
		Where(account.IDEQ(acc.ID)).
		WithProxy().
		Only(ctx)
	if err != nil {
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

	// X-Airgate-Internal 让下游网关（如 gateway-claude 的 claude_code_only 开关）
	// 识别这是 HostService 自家的黑盒探测流量，跳过面向外部客户端的身份闸。
	// 与 account.TestAccount 的管理后台测试走同一约定，插件侧统一用这一个 header 判。
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
	resp.LatencyMs = latency.Milliseconds()
	resp.StatusCode = int64(outcome.Upstream.StatusCode)

	// 插件自身故障（进程异常等）—— 不经过状态机，仅记录。
	if fwdErr != nil {
		resp.Success = false
		resp.ErrorKind = "plugin_error"
		resp.ErrorMsg = truncateProbeErr(fwdErr.Error())
		return resp, nil
	}

	// 探测的判决同样交给状态机（与真实流量同一入口），让探测信号驱动账号状态。
	h.scheduler.Apply(ctx, acc.ID, scheduler.Judgment{
		Kind:       outcome.Kind,
		RetryAfter: outcome.RetryAfter,
		Reason:     outcome.Reason,
		Duration:   latency,
		IsPool:     accFull.UpstreamIsPool,
	})

	switch outcome.Kind {
	case sdk.OutcomeSuccess:
		resp.Success = true
	case sdk.OutcomeAccountRateLimited:
		resp.Success = false
		resp.ErrorKind = "rate_limited"
		resp.ErrorMsg = truncateProbeErr(outcome.Reason)
	case sdk.OutcomeAccountDead:
		resp.Success = false
		resp.ErrorKind = "account_error"
		resp.ErrorMsg = truncateProbeErr(outcome.Reason)
	case sdk.OutcomeUpstreamTransient, sdk.OutcomeStreamAborted:
		resp.Success = false
		resp.ErrorKind = "upstream_5xx"
		resp.ErrorMsg = truncateProbeErr(outcome.Reason)
	case sdk.OutcomeClientError:
		resp.Success = false
		resp.ErrorKind = "client_error"
		resp.ErrorMsg = truncateProbeErr(outcome.Reason)
	default:
		resp.Success = false
		resp.ErrorKind = "unknown"
		resp.ErrorMsg = truncateProbeErr(outcome.Reason)
	}
	return resp, nil
}

// listGroups 列出所有分组。内部 worker，由 pluginHostHandle.ListGroups 委托。
func (h *HostService) listGroups(ctx context.Context, _ *pb.HostListGroupsRequest) (*pb.HostListGroupsResponse, error) {
	slog.Debug("HostService.ListGroups", "module", "host")
	groups, err := h.db.Group.Query().All(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &pb.HostListGroupsResponse{
		Groups: make([]*pb.HostGroup, 0, len(groups)),
	}
	for _, g := range groups {
		resp.Groups = append(resp.Groups, &pb.HostGroup{
			Id:             int64(g.ID),
			Name:           g.Name,
			Platform:       g.Platform,
			IsExclusive:    g.IsExclusive,
			RateMultiplier: g.RateMultiplier,
		})
	}
	return resp, nil
}

// reportAccountResult 把账号调用结果反馈给 scheduler。
// 内部 worker，由 pluginHostHandle.ReportAccountResult 委托。
//
// success=true 直接走 Apply(OutcomeSuccess)；success=false 按"上游抖动"上报
// （由状态机的滚动窗口计数决定是否升级为 disabled），避免探测插件单次失败
// 就把账号标死。
func (h *HostService) reportAccountResult(ctx context.Context, req *pb.HostReportAccountResultRequest) (*pb.Empty, error) {
	if req.AccountId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id 必须 > 0")
	}
	kind := sdk.OutcomeUpstreamTransient
	if req.Success {
		kind = sdk.OutcomeSuccess
	}
	h.scheduler.Apply(ctx, int(req.AccountId), scheduler.Judgment{
		Kind:   kind,
		Reason: req.ErrorMsg,
	})
	return &pb.Empty{}, nil
}

// forward 非流式业务转发：调度 → 网关 → 计费 → 记录。
// 与 probeForward 的区别：走完整计费管线，不跳过 usage_log / 余额扣款。
// 账号级故障自动 failover，最多 maxHostForwardAttempts 次。
func (h *HostService) forward(ctx context.Context, req *pb.HostForwardRequest) (*pb.HostForwardResponse, error) {
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}

	routes, err := h.hostForwardRoutes(ctx, req)
	if err != nil {
		return nil, err
	}
	fwdCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var hardExclude []int
	for _, route := range routes {
		model := h.resolveHostModel(route.Platform, req.Model)
		if model == "" {
			slog.Warn("HostService Forward 候选分组没有可用模型", "platform", route.Platform, "group_id", route.GroupID)
			continue
		}
		inst := h.manager.GetPluginByPlatform(route.Platform)
		if inst == nil || inst.Gateway == nil {
			slog.Warn("HostService Forward 候选分组没有可用插件", "platform", route.Platform, "group_id", route.GroupID)
			continue
		}

		for attempt := 0; attempt < maxHostForwardAttempts; attempt++ {
			acc, err := h.scheduler.SelectAccount(ctx, route.Platform, model, 0, route.GroupID, "", hardExclude...)
			if err != nil {
				slog.Warn("HostService Forward 调度失败",
					"platform", route.Platform, "model", model, "group_id", route.GroupID,
					"effective_rate", route.EffectiveRate, "error", err)
				break
			}

			accFull, err := h.db.Account.Query().Where(account.IDEQ(acc.ID)).WithProxy().Only(ctx)
			if err != nil {
				slog.Error("HostService Forward 加载账号失败", "account_id", acc.ID, "error", err)
				return nil, hostForwardGenericError()
			}

			headers := hostForwardHeaders(req, route)
			fwdReq := &sdk.ForwardRequest{
				Account: hostSDKAccount(accFull),
				Body:    req.Body,
				Headers: headers,
				Model:   model,
				Stream:  false,
			}

			start := time.Now()
			outcome, fwdErr := inst.Gateway.Forward(fwdCtx, fwdReq)
			duration := time.Since(start)
			h.applyHostOutcome(ctx, acc.ID, accFull, outcome, duration)

			if fwdErr != nil || outcome.Kind.ShouldFailover() {
				slog.Warn("HostService Forward failover",
					"group_id", route.GroupID, "effective_rate", route.EffectiveRate,
					"account_id", acc.ID, "attempt", attempt+1,
					"kind", outcome.Kind, "reason", outcome.Reason, "error", fwdErr)
				hardExclude = append(hardExclude, acc.ID)
				continue
			}

			if outcome.Kind == sdk.OutcomeClientError {
				slog.Warn("HostService Forward 上游客户端错误，脱敏返回",
					"group_id", route.GroupID, "account_id", acc.ID,
					"status_code", outcome.Upstream.StatusCode, "reason", outcome.Reason)
				return nil, status.Error(codes.InvalidArgument, "请求无法完成，请检查输入后重试")
			}
			if outcome.Kind != sdk.OutcomeSuccess {
				slog.Warn("HostService Forward 判决失败",
					"group_id", route.GroupID, "account_id", acc.ID,
					"kind", outcome.Kind, "reason", outcome.Reason)
				break
			}

			resp := &pb.HostForwardResponse{
				StatusCode: int32(outcome.Upstream.StatusCode),
				Headers:    httpHeadersToProtoHost(outcome.Upstream.Headers),
				Body:       outcome.Upstream.Body,
			}

			if outcome.Kind == sdk.OutcomeSuccess && outcome.Usage != nil {
				h.recordHostForwardUsage(ctx, req, route, acc.ID, route.Platform, model, accFull, outcome, duration)
				resp.Usage = &pb.HostForwardUsage{
					InputTokens:  int64(outcome.Usage.InputTokens),
					OutputTokens: int64(outcome.Usage.OutputTokens),
					Cost:         outcome.Usage.InputCost + outcome.Usage.OutputCost + outcome.Usage.CachedInputCost + outcome.Usage.CacheCreationCost,
					Model:        model,
				}
			}

			return resp, nil
		}
	}

	return nil, hostForwardGenericError()
}

// forwardStream 流式业务转发。
// 账号级故障自动 failover：通过 failoverStreamWriter 延迟提交，
// 成功（< 400）时立即切换到真流式，失败时缓冲数据后丢弃重试。
func (h *HostService) forwardStream(ctx context.Context, req *pb.HostForwardRequest, stream pb.HostService_ForwardStreamServer) error {
	if req.UserId <= 0 {
		return status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}

	routes, err := h.hostForwardRoutes(ctx, req)
	if err != nil {
		return err
	}
	fwdCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()

	sw := &hostStreamWriter{stream: stream}
	var hardExclude []int

	for _, route := range routes {
		model := h.resolveHostModel(route.Platform, req.Model)
		if model == "" {
			slog.Warn("HostService ForwardStream 候选分组没有可用模型", "platform", route.Platform, "group_id", route.GroupID)
			continue
		}
		inst := h.manager.GetPluginByPlatform(route.Platform)
		if inst == nil || inst.Gateway == nil {
			slog.Warn("HostService ForwardStream 候选分组没有可用插件", "platform", route.Platform, "group_id", route.GroupID)
			continue
		}

		for attempt := 0; attempt < maxHostForwardAttempts; attempt++ {
			acc, err := h.scheduler.SelectAccount(ctx, route.Platform, model, 0, route.GroupID, "", hardExclude...)
			if err != nil {
				slog.Warn("HostService ForwardStream 调度失败",
					"platform", route.Platform, "model", model, "group_id", route.GroupID,
					"effective_rate", route.EffectiveRate, "error", err)
				break
			}

			accFull, err := h.db.Account.Query().Where(account.IDEQ(acc.ID)).WithProxy().Only(ctx)
			if err != nil {
				slog.Error("HostService ForwardStream 加载账号失败", "account_id", acc.ID, "error", err)
				return hostForwardGenericError()
			}

			fw := &failoverStreamWriter{target: sw}
			fwdReq := &sdk.ForwardRequest{
				Account: hostSDKAccount(accFull),
				Body:    req.Body,
				Headers: hostForwardHeaders(req, route),
				Model:   model,
				Stream:  true,
				Writer:  fw,
			}

			start := time.Now()
			outcome, fwdErr := inst.Gateway.Forward(fwdCtx, fwdReq)
			duration := time.Since(start)
			h.applyHostOutcome(ctx, acc.ID, accFull, outcome, duration)

			canRetry := !fw.committed && (fwdErr != nil || outcome.Kind.ShouldFailover())
			if canRetry {
				slog.Warn("HostService ForwardStream failover",
					"group_id", route.GroupID, "effective_rate", route.EffectiveRate,
					"account_id", acc.ID, "attempt", attempt+1,
					"kind", outcome.Kind, "reason", outcome.Reason, "error", fwdErr)
				hardExclude = append(hardExclude, acc.ID)
				continue
			}

			if outcome.Kind == sdk.OutcomeClientError {
				slog.Warn("HostService ForwardStream 上游客户端错误，脱敏返回",
					"group_id", route.GroupID, "account_id", acc.ID,
					"status_code", outcome.Upstream.StatusCode, "reason", outcome.Reason)
				return status.Error(codes.InvalidArgument, "请求无法完成，请检查输入后重试")
			}

			if !fw.committed {
				fw.flush()
			}

			if outcome.Kind != sdk.OutcomeSuccess && fwdErr == nil {
				slog.Warn("HostService ForwardStream 上游失败，流已提交无法重试",
					"group_id", route.GroupID, "effective_rate", route.EffectiveRate,
					"account_id", acc.ID, "kind", outcome.Kind,
					"status_code", outcome.Upstream.StatusCode, "reason", outcome.Reason,
					"stream_committed", fw.committed)
			}

			if fwdErr != nil {
				slog.Warn("HostService ForwardStream 插件调用失败", "group_id", route.GroupID, "account_id", acc.ID, "error", fwdErr)
				return hostForwardGenericError()
			}

			var usage *pb.HostForwardUsage
			if outcome.Kind == sdk.OutcomeSuccess && outcome.Usage != nil {
				h.recordHostForwardUsage(ctx, req, route, acc.ID, route.Platform, model, accFull, outcome, duration)
				usage = &pb.HostForwardUsage{
					InputTokens:  int64(outcome.Usage.InputTokens),
					OutputTokens: int64(outcome.Usage.OutputTokens),
					Cost:         outcome.Usage.InputCost + outcome.Usage.OutputCost + outcome.Usage.CachedInputCost + outcome.Usage.CacheCreationCost,
					Model:        model,
				}
			}

			return stream.Send(&pb.HostForwardChunk{Done: true, Usage: usage})
		}
	}

	return hostForwardGenericError()
}

// maxHostForwardAttempts 最大 failover 次数，与 Forwarder 保持一致。
const maxHostForwardAttempts = 3

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
	stream     pb.HostService_ForwardStreamServer
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
	_ = w.stream.Send(&pb.HostForwardChunk{
		StatusCode: int32(statusCode),
		Headers:    httpHeadersToProtoHost(w.header),
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
	if err := w.stream.Send(&pb.HostForwardChunk{Data: chunk}); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *hostStreamWriter) Flush() {}

// recordHostForwardUsage 为 Host.Forward 发起的请求记录 usage_log 并扣费。
// 与 forwarder.recordUsage 的区别：没有 APIKeyInfo，APIKeyID=0。
func (h *HostService) recordHostForwardUsage(
	ctx context.Context,
	req *pb.HostForwardRequest,
	route routing.Candidate,
	accountID int,
	platform, model string,
	accFull *ent.Account,
	outcome sdk.ForwardOutcome,
	duration time.Duration,
) {
	usage := outcome.Usage
	if usage == nil {
		return
	}

	calc := h.calculator.Calculate(billing.CalculateInput{
		InputCost:         usage.InputCost,
		OutputCost:        usage.OutputCost,
		CachedInputCost:   usage.CachedInputCost,
		CacheCreationCost: usage.CacheCreationCost,
		BillingRate:       route.EffectiveRate,
		AccountRate:       accFull.RateMultiplier,
	})

	h.scheduler.AddWindowCost(ctx, accountID, calc.AccountCost)

	actualModel := usage.Model
	if actualModel == "" {
		actualModel = model
	}

	h.recorder.Record(billing.UsageRecord{
		UserID:                int(req.UserId),
		APIKeyID:              0,
		AccountID:             accountID,
		GroupID:               route.GroupID,
		Platform:              platform,
		Model:                 actualModel,
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheCreationTokens:   usage.CacheCreationTokens,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		InputPrice:            usage.InputPrice,
		OutputPrice:           usage.OutputPrice,
		CachedInputPrice:      usage.CachedInputPrice,
		CacheCreationPrice:    usage.CacheCreationPrice,
		CacheCreation1hPrice:  usage.CacheCreation1hPrice,
		InputCost:             calc.InputCost,
		OutputCost:            calc.OutputCost,
		CachedInputCost:       calc.CachedInputCost,
		CacheCreationCost:     calc.CacheCreationCost,
		TotalCost:             calc.TotalCost,
		ActualCost:            calc.ActualCost,
		BilledCost:            calc.BilledCost,
		AccountCost:           calc.AccountCost,
		RateMultiplier:        calc.RateMultiplier,
		AccountRateMultiplier: calc.AccountRateMultiplier,
		ServiceTier:           usage.ServiceTier,
		Stream:                req.Stream,
		DurationMs:            duration.Milliseconds(),
		FirstTokenMs:          usage.FirstTokenMs,
	})
}

// listPlatforms 列出已加载的网关平台。
func (h *HostService) listPlatforms(_ context.Context, _ *pb.HostListPlatformsRequest) (*pb.HostListPlatformsResponse, error) {
	metas := h.manager.GetAllPluginMeta()
	seen := make(map[string]bool)
	var platforms []*pb.HostPlatform
	for _, m := range metas {
		if m.Type != "gateway" || m.Platform == "" || seen[m.Platform] {
			continue
		}
		seen[m.Platform] = true
		platforms = append(platforms, &pb.HostPlatform{
			Name:        m.Platform,
			DisplayName: m.DisplayName,
		})
	}
	return &pb.HostListPlatformsResponse{Platforms: platforms}, nil
}

// listModels 列出指定平台的模型列表。
func (h *HostService) listModels(_ context.Context, req *pb.HostListModelsRequest) (*pb.HostListModelsResponse, error) {
	if req.Platform == "" {
		return nil, status.Error(codes.InvalidArgument, "platform 不能为空")
	}
	models := h.manager.GetModels(req.Platform)
	resp := &pb.HostListModelsResponse{
		Models: make([]*pb.ModelInfoProto, 0, len(models)),
	}
	for _, m := range models {
		resp.Models = append(resp.Models, &pb.ModelInfoProto{
			Id:                       m.ID,
			Name:                     m.Name,
			InputPrice:               m.InputPrice,
			OutputPrice:              m.OutputPrice,
			CachedInputPrice:         m.CachedInputPrice,
			CacheCreationPrice:       m.CacheCreationPrice,
			CacheCreation_1HPrice:    m.CacheCreation1hPrice,
			ContextWindow:            int64(m.ContextWindow),
			MaxOutputTokens:          int64(m.MaxOutputTokens),
			InputPricePriority:       m.InputPricePriority,
			OutputPricePriority:      m.OutputPricePriority,
			CachedInputPricePriority: m.CachedInputPricePriority,
		})
	}
	return resp, nil
}

// getUserInfo 获取用户基本信息。
func (h *HostService) getUserInfo(ctx context.Context, req *pb.HostGetUserInfoRequest) (*pb.HostGetUserInfoResponse, error) {
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id 必须 > 0")
	}
	u, err := h.db.User.Get(ctx, int(req.UserId))
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "用户不存在")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.HostGetUserInfoResponse{
		UserId:   int64(u.ID),
		Username: u.Username,
		Email:    u.Email,
		Role:     string(u.Role),
		Balance:  u.Balance,
		Status:   string(u.Status),
	}, nil
}

// protoHeadersToHTTPHost / httpHeadersToProtoHost 是 host_service.go 内部的 header 转换。
// 与 grpc/gateway_server.go 的同名函数等价，但跨包引用会引入循环依赖。
func (h *HostService) hostForwardRoutes(ctx context.Context, req *pb.HostForwardRequest) ([]routing.Candidate, error) {
	if req.GroupId > 0 {
		u, err := h.db.User.Query().Where(user.IDEQ(int(req.UserId))).Only(ctx)
		if err != nil {
			slog.Error("HostService 查询用户失败", "user_id", req.UserId, "error", err)
			return nil, hostForwardGenericError()
		}
		g, err := h.db.Group.Get(ctx, int(req.GroupId))
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, status.Error(codes.NotFound, "分组不存在")
			}
			slog.Error("HostService 查询分组失败", "group_id", req.GroupId, "error", err)
			return nil, hostForwardGenericError()
		}
		if !routing.GroupMatchesRequirements(g, hostForwardRequirements(req)) {
			slog.Warn("HostService Forward 指定分组不满足请求能力", "group_id", req.GroupId, "model", req.Model, "path", req.Path)
			return nil, hostForwardGenericError()
		}
		return []routing.Candidate{{
			GroupID:                g.ID,
			Platform:               g.Platform,
			EffectiveRate:          billing.ResolveBillingRateForGroup(u.GroupRates, g.ID, g.RateMultiplier),
			GroupRateMultiplier:    g.RateMultiplier,
			GroupServiceTier:       g.ServiceTier,
			GroupForceInstructions: g.ForceInstructions,
			GroupPluginSettings:    clonePluginSettingsHost(g.PluginSettings),
			SortWeight:             g.SortWeight,
		}}, nil
	}

	platform := protoHeadersToHTTPHost(req.Headers).Get("X-Airgate-Platform")
	if platform == "" {
		return nil, status.Error(codes.InvalidArgument, "platform 不能为空")
	}
	u, err := h.db.User.Query().Where(user.IDEQ(int(req.UserId))).Only(ctx)
	if err != nil {
		slog.Error("HostService 查询用户失败", "user_id", req.UserId, "error", err)
		return nil, hostForwardGenericError()
	}
	routes, err := routing.ListEligibleGroups(ctx, h.db, int(req.UserId), platform, u.GroupRates, hostForwardRequirements(req))
	if err != nil {
		slog.Error("HostService 查询候选分组失败", "platform", platform, "user_id", req.UserId, "error", err)
		return nil, hostForwardGenericError()
	}
	if len(routes) == 0 {
		slog.Warn("HostService 没有可用候选分组", "platform", platform, "user_id", req.UserId)
		return nil, hostForwardGenericError()
	}
	return routes, nil
}

func hostForwardRequirements(req *pb.HostForwardRequest) routing.Requirements {
	if req == nil {
		return routing.Requirements{}
	}
	return routing.Requirements{NeedsImage: requestNeedsImage(req.Path, req.Model)}
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

func hostForwardHeaders(req *pb.HostForwardRequest, route routing.Candidate) http.Header {
	headers := protoHeadersToHTTPHost(req.Headers)
	headers.Set("X-Forwarded-Path", req.Path)
	headers.Set("X-Forwarded-Method", req.Method)
	headers.Set("X-Airgate-Internal", "host-forward")
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
			if v == "" {
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

func (h *HostService) applyHostOutcome(ctx context.Context, accountID int, accFull *ent.Account, outcome sdk.ForwardOutcome, duration time.Duration) {
	h.scheduler.Apply(ctx, accountID, scheduler.Judgment{
		Kind:       outcome.Kind,
		RetryAfter: outcome.RetryAfter,
		Reason:     outcome.Reason,
		Duration:   duration,
		IsPool:     accFull.UpstreamIsPool,
	})
}

func hostForwardGenericError() error {
	return status.Error(codes.Unavailable, "请求暂时无法完成，请稍后重试")
}

func protoHeadersToHTTPHost(ph map[string]*pb.HeaderValues) http.Header {
	h := make(http.Header, len(ph))
	for k, v := range ph {
		if v != nil {
			h[k] = v.Values
		}
	}
	return h
}

func httpHeadersToProtoHost(h http.Header) map[string]*pb.HeaderValues {
	ph := make(map[string]*pb.HeaderValues, len(h))
	for k, v := range h {
		ph[k] = &pb.HeaderValues{Values: v}
	}
	return ph
}

// errProbeResp 构造一个失败的 probe response（不通过 gRPC error 返回，
// 让插件能拿到 latency_ms 和 error_kind 写入自己的 health 表）。
func errProbeResp(kind, msg string, start time.Time) *pb.HostProbeForwardResponse {
	return &pb.HostProbeForwardResponse{
		Success:   false,
		ErrorKind: kind,
		ErrorMsg:  truncateProbeErr(msg),
		LatencyMs: time.Since(start).Milliseconds(),
	}
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
