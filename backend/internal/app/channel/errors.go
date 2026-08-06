package channel

import "errors"

var (
	// ErrChannelNotFound 表示目标渠道不存在。
	ErrChannelNotFound = errors.New("渠道不存在")
	// ErrInvalidReference 表示引用的分组或代理不存在。
	ErrInvalidReference = errors.New("引用的分组或代理不存在")
	// ErrInvalidBulkAction 表示批量操作动作非法或缺少必要参数。
	ErrInvalidBulkAction = errors.New("无效的批量操作")
	// ErrInvalidProtocolSet 表示同一凭证选择了非法协议组合。
	ErrInvalidProtocolSet = errors.New("无效的协议组合")
	// ErrNoAPIKey 表示渠道未配置任何 API Key。
	ErrNoAPIKey = errors.New("渠道未配置 API Key")
	// ErrTesterNotReady 表示转发管线尚未就绪，渠道测试不可用。
	ErrTesterNotReady = errors.New("转发管线未就绪")
	// ErrTestFailed 表示渠道测试请求失败（上游返回错误）。
	ErrTestFailed = errors.New("渠道测试失败")
	// ErrModelFetchFailed 表示拉取上游模型列表失败。
	ErrModelFetchFailed = errors.New("拉取模型列表失败")
	// ErrBalanceFetchFailed 表示查询上游余额失败（所有 key 均失败）。
	ErrBalanceFetchFailed = errors.New("查询余额失败")
)
