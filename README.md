# airgate-core

AirGate 的核心服务仓库。

## 这个仓库给谁看

- 运行和维护 AirGate 的人
- 开发 Core 后端或管理后台的人
- 需要了解 Core 和插件边界的人

## 它负责什么

- 管理用户、分组、账号、API Key、计费和使用记录
- 动态加载插件，并把插件路由接到 Core
- 托管插件前端资源，在管理后台里渲染插件页面

## 它不负责什么

- 插件接口定义：看 `airgate-sdk`
- OpenAI 插件实现：看 `airgate-openai`

## 仓库结构

- `backend/`：Go 后端
- `web/`：管理后台前端
- `docs/`：Core 相关文档

## 常用命令

```bash
make install
make dev
make build

cd backend && go test ./...
cd web && npm run build
```

## 建议先看

1. `docs/README.md`
2. `docs/overview.md`
3. `docs/architecture.md`
4. `docs/marketplace.md`
