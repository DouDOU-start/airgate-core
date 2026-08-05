package task

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DouDOU-start/airgate-core/internal/relay/registry"
)

// Info 单次上游调用的上下文（提交/轮询/内容代理共用；每个 attempt 独立构造）。
type Info struct {
	// ChannelKey 本次选中（或任务落库时快照）的密钥端点（只读）。
	ChannelKey *registry.ChannelKeySnapshot
	// APIKey 本次选中的上游密钥（明文）。
	APIKey string
	// RequestModel 对外模型名 / UpstreamModel 上游模型名（经 key 的 model_mapping）。
	RequestModel  string
	UpstreamModel string
	// Client 出口 HTTP 客户端（子系统共享复用）。
	Client *http.Client
}

// SubmitRequest 提交请求的调度视图：原始体透传，此处只承载调度与估价所需字段。
type SubmitRequest struct {
	// Model 对外模型名（调度与计费用；suno 由 action 合成，如 suno_music）。
	Model string
	// Action 平台内动作（video 恒 "generate"；suno 为 music/lyrics）。
	Action string
	// Seconds 视频时长参数（估价用；无则 0，估价按平台默认值兜底）。
	Seconds int
	// Resolution 视频分辨率档位（如 480p/720p/1080p）；用于按档秒价估价与结算。
	Resolution string
	// Body 原始请求体（JSON 或 multipart，原样直发上游，仅定点重写 model）。
	Body []byte
	// ContentType 原始 Content-Type（multipart 含 boundary，原样转发）。
	ContentType string
}

// Status 上游任务状态的归一化结果（提交响应与查询响应共用）。
type Status struct {
	// Status 归一化状态（Status* 常量）。
	Status string
	// Progress 进度 0-100。
	Progress int
	// FailReason 失败原因（仅 failure 有意义）。
	FailReason string
	// Seconds 上游返回的实际产出时长（结算用；无则 0，保留估价）。
	Seconds int
	// Raw 上游原始响应（落 task.data，查询端点据此重建响应）。
	Raw json.RawMessage
}

// Adaptor 任务平台适配器：提交侧透传（URL/认证/模型重写），
// 查询侧状态归一化（计量与状态提取，非翻译）。
// 调度/重试/禁用/计费一律在 flow/poller，adaptor 不碰。
type Adaptor interface {
	// ParseSubmit 从入站请求提取调度/估价字段（不改动 body）。
	// action 为路由携带的平台内动作（video 传空串）。
	ParseSubmit(action, contentType string, body []byte) (*SubmitRequest, error)
	// BuildSubmitRequest 构建上游提交请求（体透传，仅定点重写 model）。
	BuildSubmitRequest(ctx context.Context, info *Info, req *SubmitRequest) (*http.Request, error)
	// ParseSubmitResponse 从上游 2xx 提交响应提取 task_id 与初始状态。
	ParseSubmitResponse(body []byte) (taskID string, st *Status, err error)
	// BuildQueryRequest 构建上游单任务查询请求（轮询用）。
	BuildQueryRequest(ctx context.Context, info *Info, taskID string) (*http.Request, error)
	// ParseQueryResponse 归一化上游查询响应。
	ParseQueryResponse(body []byte) (*Status, error)
	// RenderTask 由本地 task 行重建入口协议的查询响应体
	// （model 回写对外名、状态按协议词表映射）。
	RenderTask(t *Task) []byte
}

// ContentProxy 可选能力：任务产物内容实时代理（GET /v1/videos/{id}/content）。
type ContentProxy interface {
	BuildContentRequest(ctx context.Context, info *Info, taskID string) (*http.Request, error)
}

// BatchQuerying 可选能力：一次查多个任务（suno；轮询器按渠道批量走此路径）。
type BatchQuerying interface {
	BuildBatchQueryRequest(ctx context.Context, info *Info, taskIDs []string) (*http.Request, error)
	// ParseBatchQueryResponse 返回 task_id → 归一化状态；缺席的 ID 视为本轮无更新。
	ParseBatchQueryResponse(body []byte) (map[string]*Status, error)
	// RenderTaskList 批量查询端点的响应重建（协议自带列表包裹形态）。
	RenderTaskList(ts []*Task) []byte
}

// factories 平台 → 适配器工厂。注册发生在各子包 init，运行期只读，无需加锁。
var factories = map[string]func() Adaptor{}

// Register 注册平台适配器工厂（由平台子包 init 调用；平台值 = 渠道 Type）。
func Register(platform string, factory func() Adaptor) {
	factories[platform] = factory
}

// GetAdaptor 按平台获取适配器；未注册平台报错。
func GetAdaptor(platform string) (Adaptor, error) {
	factory, ok := factories[platform]
	if !ok {
		return nil, fmt.Errorf("不支持的任务平台: %s", platform)
	}
	return factory(), nil
}
