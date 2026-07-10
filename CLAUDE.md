# airgate-core — Claude 开发指南（standalone-gateway 分支）

> **本分支是独立单体网关架构**，与 master 插件架构长期分叉、不合回。仓库外的任何文档（monorepo 根 CLAUDE.md、skill `core-dev`/`develop-plugin`）描述的都是 master 插件线，对本分支一律不适用。
>
> **动手开发前先读 skill `airgate-dev`**（`.claude/skills/airgate-dev/SKILL.md`）：架构、子系统边界、新增领域套路、前端落点都在那里。

## 🚫 红线

- **分层五件套**：dto → handler（不写业务）→ service（不碰 gin/http、不 import ent）→ store（唯一 import ent）→ ent/schema。
- **改 `ent/schema/` 后须 `make ent` 并提交生成代码**；生成代码不可手改。
- **装配两处接线**：`internal/bootstrap/http_handlers.go` + `internal/server/router.go` `registerRoutes()`。
- **转发路由（/v1、/v1beta、/suno）错误一律按入口协议原生形态**（`internal/relay/errfmt` 分发），不用 `response.*`；管理面照旧 `response.*`。
- **渠道 api_keys 明文永不出现在任何 API 响应**（只出 count + 尾 4 位 hint）；加解密用 `internal/auth`，在 service 层做。
- **relay 子系统（registry/task 等）禁止 import ent 与 app 包**，经窄接口注入。
- 复用优先（新领域参照 channel 域全链路）；注释中文；`_test.go` 同包、表驱动。
- 需求/架构变更**同步更新 skill `airgate-dev` 与 `README.md`**，防止文档漂移。

## 常用命令

```bash
make ent   # 改 ent/schema 后重新生成
make ci    # 提交前自检
```
