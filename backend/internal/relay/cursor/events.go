package cursor

// 中立事件层：session 状态机把 Cursor 协议产出的增量翻译成与入口协议无关的
// 事件序列，translate_out 再据此渲染成 OpenAI / Anthropic 的 SSE 或聚合 JSON。
// session 不需要知道入口协议，renderer 不需要碰 protobuf。

// Event 是中立事件的密封接口。
type Event interface{ isCursorEvent() }

// TextDelta 是一段助手正文增量。
type TextDelta struct{ Text string }

// ReasoningDelta 是一段思维链（thinking）增量。
type ReasoningDelta struct{ Text string }

// Ready 表示上游流已建立（收到首条服务端消息，HTTP 层与鉴权/限流错误已排
// 除）。渲染层据此提前下发协议起始帧：thinking 模型首 token 可达 10 秒以上
// 且期间上游无任何增量，不提前发帧客户端会长时间收不到一个字节。
type Ready struct{}

// ToolCallStart 表示模型开始调用一个（用户下发的）工具。
type ToolCallStart struct {
	ID   string
	Name string
}

// ToolCallArgsDelta 是某个工具调用参数 JSON 的增量文本。Name 为当前已知的
// 工具名（可能为空）：工具名有时晚于参数流到达，渲染侧可借此延迟开块。
type ToolCallArgsDelta struct {
	ID   string
	Name string
	Args string
}

// ToolCallEnd 表示某个工具调用的参数已完整。ArgsJSON 为完整参数（可能为空，
// 此时以累积的 ToolCallArgsDelta 为准）。
type ToolCallEnd struct {
	ID       string
	Name     string
	ArgsJSON []byte
}

// UsageDelta 携带 token 计量增量或快照（Cursor 只给粗粒度累计值）。
type UsageDelta struct {
	// Tokens 是 token_delta 的增量值（若上游按增量上报）。
	Tokens int
	// PromptTokens / CompletionTokens 是快照（若上游给出）。
	PromptTokens     int
	CompletionTokens int
}

// Done 表示本轮生成结束。FinishReason 取 "stop" / "tool_calls" / "length" 等。
type Done struct{ FinishReason string }

// ErrEvent 表示流异常终止。
type ErrEvent struct{ Err error }

func (Ready) isCursorEvent()             {}
func (TextDelta) isCursorEvent()         {}
func (ReasoningDelta) isCursorEvent()    {}
func (ToolCallStart) isCursorEvent()     {}
func (ToolCallArgsDelta) isCursorEvent() {}
func (ToolCallEnd) isCursorEvent()       {}
func (UsageDelta) isCursorEvent()        {}
func (Done) isCursorEvent()              {}
func (ErrEvent) isCursorEvent()          {}
