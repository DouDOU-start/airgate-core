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
//   - 克制暴露面：只暴露 ProbeForward / SelectAccount / ListGroups /
//     ReportAccountResult，未来需要再加；
//   - ProbeForward 与普通 Forward 严格隔离：跳过 usage_log 写入、跳过余额扣款，
//     但仍然 ReportResult 让账号状态机受益；
//   - 不要求插件持有 admin_api_key——broker 子进程隧道天然互信，但仍然要做
//     capability 级权限隔离。
type HostService struct {
	db        *ent.Client
	manager   *Manager
	scheduler *scheduler.Scheduler
}

// NewHostService 构造 HostService 工厂。
// 由 server 在创建 Manager + scheduler 之后立即创建并 SetHostService 注入到 Manager。
//
// HostService 自身不实现 pb.HostServiceServer—— 用 NewPluginHandle 给每个插件
// 派生一个 pluginHostHandle 才是真正的 server 实例。
func NewHostService(db *ent.Client, mgr *Manager, sched *scheduler.Scheduler) *HostService {
	return &HostService{db: db, manager: mgr, scheduler: sched}
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

	// caps 指针指向一个 map[string]bool。nil = "未授权状态"，所有 RPC 都拒绝。
	// 用 atomic.Pointer 是为了让 SetCapabilities 与 RPC 处理并发安全，无需 mutex。
	caps atomic.Pointer[map[string]bool]
}

// SetCapabilities 由 Manager 在 spawn 完成、Info() 拿到 capability 列表后调用。
//
// nil caps == 兼容模式（豁免校验），用于 sdk_version <= 0.2.x 的存量插件。
// 空 set（len=0）== 显式声明"什么都不要"，所有 RPC 都被拒。
func (h *pluginHostHandle) SetCapabilities(caps map[string]bool) {
	if caps == nil {
		h.caps.Store(nil)
		return
	}
	cloned := make(map[string]bool, len(caps))
	for k, v := range caps {
		cloned[k] = v
	}
	h.caps.Store(&cloned)
}

// requireCap 检查当前插件是否拥有 capability cap。
//
// 兼容模式（caps==nil）下放行任何调用并 log debug，便于审计哪些老插件依赖什么能力，
// 等老插件全部声明 capabilities 后再去掉兼容路径。
func (h *pluginHostHandle) requireCap(cap string) error {
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

	fwdReq := &sdk.ForwardRequest{
		Account: &sdk.Account{
			ID:          int64(accFull.ID),
			Name:        accFull.Name,
			Platform:    accFull.Platform,
			Type:        accFull.Type,
			Credentials: cloneStringMapHost(accFull.Credentials),
			ProxyURL:    proxyURLFromAccount(accFull),
		},
		Body:    body,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Model:   model,
		Stream:  false,
	}

	// 调用插件，限制最长 30s（探测不应卡住调度循环）
	fwdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, fwdErr := inst.Gateway.Forward(fwdCtx, fwdReq)
	latency := time.Since(start)
	resp.LatencyMs = latency.Milliseconds()

	// 反馈调度器（成功 / 失败都会让状态机受益）
	if fwdErr != nil {
		resp.Success = false
		resp.ErrorKind = "forward_error"
		resp.ErrorMsg = truncateProbeErr(fwdErr.Error())
		h.scheduler.ReportResult(acc.ID, false, latency, fwdErr.Error())
		return resp, nil
	}
	if result == nil {
		resp.Success = false
		resp.ErrorKind = "nil_result"
		h.scheduler.ReportResult(acc.ID, false, latency, "插件返回 nil")
		return resp, nil
	}

	resp.StatusCode = int64(result.StatusCode)
	if result.StatusCode >= 400 {
		resp.Success = false
		switch {
		case result.StatusCode == 429:
			resp.ErrorKind = "rate_limited"
		case result.StatusCode >= 500:
			resp.ErrorKind = "upstream_5xx"
		default:
			resp.ErrorKind = "upstream_4xx"
		}
		resp.ErrorMsg = truncateProbeErr(result.ErrorMessage)
		h.scheduler.ReportResult(acc.ID, false, latency, result.ErrorMessage)
		return resp, nil
	}

	resp.Success = true
	h.scheduler.ReportResult(acc.ID, true, latency)
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
func (h *HostService) reportAccountResult(_ context.Context, req *pb.HostReportAccountResultRequest) (*pb.Empty, error) {
	if req.AccountId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id 必须 > 0")
	}
	// latency 未知时传 0
	if req.Success {
		h.scheduler.ReportResult(int(req.AccountId), true, 0)
	} else {
		h.scheduler.ReportResult(int(req.AccountId), false, 0, req.ErrorMsg)
	}
	return &pb.Empty{}, nil
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
