# agent.proto 来源与本地改动

## 来源

Cursor CLI Agent 服务的 Connect-RPC 协议定义（`agent.v1.AgentService`），
取自开源项目 [can1357/oh-my-pi](https://github.com/can1357/oh-my-pi)
（MIT 许可）中的 `packages/ai/src/providers/cursor/proto/agent.proto`。

该 proto 是从 Cursor 官方客户端反解得到的完整协议镜像，自包含、单 package
`agent.v1`、无外部 import；工具的 JSON schema 与参数均以 `bytes` 承载
（应用层用 `google.golang.org/protobuf/types/known/structpb` 编解码），
因此 Go 侧生成不依赖任何 well-known types。

## 本地改动

上游 proto 把 Cursor 混淆版里的嵌套 message 拍平成 `Parent_Child` 形式。
protoc-gen-go 会为 oneof case 生成同名的 `Parent_Child` wrapper 类型，两者
在 Go 包内撞名（`redeclared`）。为让 Go 代码可编译，对以下 11 个**独立
message 定义**统一追加 `Msg` 后缀（其父 message 的 oneof 字段引用同步改名，
oneof wrapper 由字段名生成、不受影响）。这些 message 全属 Cursor IDE 专有
功能（审批弹窗 / Exa 搜索 / Web 搜索 / Slack 线程 / IDE 状态 / 图片选择），
与 Run 转发主流程无关：

- `ExaFetchRequestResponse_Approved` → `ExaFetchRequestResponse_ApprovedMsg`
- `ExaFetchRequestResponse_Rejected` → `ExaFetchRequestResponse_RejectedMsg`
- `ExaSearchRequestResponse_Approved` → `ExaSearchRequestResponse_ApprovedMsg`
- `ExaSearchRequestResponse_Rejected` → `ExaSearchRequestResponse_RejectedMsg`
- `InvocationContext_IdeState` → `InvocationContext_IdeStateMsg`
- `InvocationContext_SlackThread` → `InvocationContext_SlackThreadMsg`
- `SelectedImage_BlobIdWithData` → `SelectedImage_BlobIdWithDataMsg`
- `SwitchModeRequestResponse_Approved` → `SwitchModeRequestResponse_ApprovedMsg`
- `SwitchModeRequestResponse_Rejected` → `SwitchModeRequestResponse_RejectedMsg`
- `WebSearchRequestResponse_Approved` → `WebSearchRequestResponse_ApprovedMsg`
- `WebSearchRequestResponse_Rejected` → `WebSearchRequestResponse_RejectedMsg`

此外在文件头加了 `option go_package`。

## 重新生成

```bash
cd backend/internal/relay/cursor/proto
protoc --go_out=. --go_opt=module=github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto agent.proto
```

升级 proto 时需重新应用上述 11 处重命名（或改用其它去冲突方案）。
