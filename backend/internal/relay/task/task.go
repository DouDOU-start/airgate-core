// Package task 实现异步任务转发子系统（视频/音乐等「提交-轮询」型接口），
// 与同步转发（pipeline）并列，不塞进同步主循环。
//
// 与「零翻译纯透传」红线的边界：
//   - 提交请求体仍透传：入口协议与渠道协议同构（registry.Pick 协议过滤保证），
//     adaptor 只做 URL 拼接、认证头、模型名重写——与同步 adaptor 契约一致；
//   - 查询响应不透传：客户端查任务读本地 task 表快照（后台轮询保鲜），
//     adaptor 解析上游查询响应做状态归一化后按入口协议重建响应体。
//     这是「计量与状态提取」，口径同 errfmt 对上游错误的「语义保留、载体重建」。
//
// 计费为预扣-结算-退款三段式：提交时同步扣 user.balance（估价×倍率），
// 终态成功按实际用量结算差额多退少补并落 usage_log（SkipBalanceCharge），
// 失败/超时全额退款；三管道口径与同步转发完全一致。
//
// 依赖约束：本包禁止 import ent 与 internal/app/*（同 registry 范式），
// 数据落库经 Store、余额动账经 BalanceOps 窄接口由外部注入。
package task

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// 平台常量：值与渠道 Type、入口协议（registry.Protocol*）、task.platform 同值。
const (
	PlatformOpenAIVideo = registry.ProtocolOpenAIVideo
	PlatformSuno        = registry.ProtocolSuno
)

// 任务状态常量（与 ent schema 的 status 枚举一致）。
const (
	StatusSubmitted  = "submitted"
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusSuccess    = "success"
	StatusFailure    = "failure"
)

// UnfinishedStatuses 未终态集合（轮询扫描与 CAS 条件共用）。
var UnfinishedStatuses = []string{StatusSubmitted, StatusQueued, StatusInProgress}

// IsTerminal 判断状态是否为终态。
func IsTerminal(status string) bool {
	return status == StatusSuccess || status == StatusFailure
}

// Task 任务领域对象（store ↔ flow/poller 传递；字段口径见 ent/schema/task.go）。
type Task struct {
	ID            int
	TaskID        string
	Platform      string
	Action        string
	Status        string
	Progress      int
	FailReason    string
	RequestModel  string
	UpstreamModel string

	HoldAmount            float64
	EstTotal              float64
	RateMultiplier        float64
	SellRate              float64
	AccountRateMultiplier float64
	Settled               bool
	Seconds               int

	Data       json.RawMessage
	SubmitTime time.Time
	FinishTime *time.Time
	RequestID  string

	UserID    int
	UserEmail string
	APIKeyID  int
	GroupID   int
	ChannelID int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// StatusUpdate 状态刷新（CAS：仅当行仍处未终态时生效，防轮询/实时查询并发覆盖）。
type StatusUpdate struct {
	Status     string
	Progress   int
	FailReason string
	// Seconds 上游返回的实际时长（>0 时更新，结算以此为准）。
	Seconds int
	// Data 上游最新原始响应快照；nil 表示不更新。
	Data json.RawMessage
	// FinishTime 终态时刻；nil 表示不更新。
	FinishTime *time.Time
}

// Store 任务持久化窄接口（由 infra/store 实现；本包禁止 import ent）。
type Store interface {
	// Insert 落库新任务，返回自增 ID。
	Insert(ctx context.Context, t *Task) (int, error)
	// GetForUser 按 (platform, task_id, user) 查任务（多条命中取最新）；未命中返回 (nil, nil)。
	GetForUser(ctx context.Context, platform, taskID string, userID int) (*Task, error)
	// ListForUser 批量按 task_id 查（suno fetch）；未命中的 ID 静默缺席。
	ListForUser(ctx context.Context, platform string, taskIDs []string, userID int) ([]*Task, error)
	// ListUnfinished 未终态任务按 updated_at 升序（轮询扫描）。
	ListUnfinished(ctx context.Context, limit int) ([]*Task, error)
	// CountUnfinished 未终态任务数（轮询空转短路）。
	CountUnfinished(ctx context.Context) (int, error)
	// UpdateStatusCAS 条件更新：仅当行状态仍属 UnfinishedStatuses 时应用 upd，
	// 返回是否更新到行。
	UpdateStatusCAS(ctx context.Context, id int, upd StatusUpdate) (bool, error)
	// MarkSettled 结算幂等闸：settled=false → true 的 CAS，返回是否置位成功。
	MarkSettled(ctx context.Context, id int) (bool, error)
}

// ErrInsufficientBalance 预扣时余额不足（由 BalanceOps 实现方映射）。
var ErrInsufficientBalance = errors.New("余额不足")

// BalanceOps 余额动账窄接口（由 app/user 服务经适配器实现）。
type BalanceOps interface {
	// Hold 预扣：余额不足返回 ErrInsufficientBalance（余额预检与扣款一次完成）。
	Hold(ctx context.Context, userID int, amount float64, remark string) error
	// Adjust 结算/退款动账：amount 正数入账、负数补扣（允许扣穿——服务已消费）。
	// idemKey 非空时幂等，重复提交视为成功。
	Adjust(ctx context.Context, userID int, amount float64, remark, idemKey string) error
}
