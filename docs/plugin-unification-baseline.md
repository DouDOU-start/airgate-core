# 插件 / SDK / Core 统一基线图

> 用途：给 `airgate-core / airgate-sdk / 插件仓库` 做对照与统一。
>
> 原则：图为主，文字最少。

## 1. 当前状态

```mermaid
flowchart LR
    subgraph Plugin["插件仓库"]
        PY["plugin.yaml"]
        PI["Info() / Platform() / Routes() / AccountTypes()"]
        PF["web/src/index.ts"]
        OA["OAuthHandler"]
        WA["GetWebAssets()"]
    end

    subgraph SDK["airgate-sdk"]
        SI["PluginInfo"]
        SG["Simple / Advanced / Payment / Extension"]
        SO["OAuth gRPC 协议"]
        SW["WebAssetsProvider"]
    end

    subgraph Core["airgate-core"]
        CM["Manager"]
        CA["admin/plugins + admin/accounts"]
        CW["plugin-loader.ts"]
        DB["accounts.platform / plugins / usage"]
    end

    PI --> SI
    OA --> SO
    WA --> SW
    SG --> CM
    SI --> CM
    SO --> CA
    SW --> CM
    PF --> CW
    CA --> CW
    CM --> DB

    PY -.存在但不是运行时真相.-> CM
    PI -.和 PY 重复.-> PY
```

## 2. 当前主要分裂点

```mermaid
flowchart TB
    A["标识分裂"]
    A1["plugin name\ncore 安装/目录/API"]
    A2["PluginInfo.ID\nsdk 元信息"]
    A3["platform\n账号/调度/计费"]

    B["元信息分裂"]
    B1["plugin.yaml"]
    B2["Info()"]

    C["前端协议分裂"]
    C1["core: plugin-loader.ts"]
    C2["plugin: 本地自写 TS 接口"]

    D["能力分裂"]
    D1["sdk: simple/advanced/payment/extension"]
    D2["core: 当前主链路只真正装配 simple"]

    A --> A1
    A --> A2
    A --> A3
    B --> B1
    B --> B2
    C --> C1
    C --> C2
    D --> D1
    D --> D2
```

## 3. 统一目标

```mermaid
flowchart TB
    subgraph Source["唯一运行时真相"]
        INFO["PluginInfo\n- name\n- display_name\n- version\n- type\n- platform\n- capabilities\n- config_fields\n- account_types\n- frontend_pages"]
    end

    subgraph Dist["分发/构建清单"]
        YAML["plugin.yaml\n仅构建/发布/市场使用\n不作为运行时真相"]
    end

    subgraph Runtime["运行时消费方"]
        CORE["core manager / server / web"]
        SDK["sdk grpc / type defs"]
        PLUGIN["plugin impl"]
    end

    PLUGIN --> INFO
    INFO --> SDK
    INFO --> CORE
    INFO -.镜像或生成.-> YAML
```

## 4. 标识职责

```mermaid
flowchart LR
    N["name\n插件唯一安装键"] --> N1["目录名"]
    N --> N2["/api/v1/admin/plugins/:name/*"]
    N --> N3["/plugins/{name}/assets/*"]
    N --> N4["前端动态加载键"]

    P["platform\n业务平台键"] --> P1["accounts.platform"]
    P --> P2["groups.platform"]
    P --> P3["usage.platform"]
    P --> P4["调度 / 路由匹配"]

    D["display_name\n仅展示"] --> D1["插件列表"]
    D --> D2["管理后台 UI"]

    X["id\n废弃或仅兼容"] -.逐步移除.-> N
```

## 5. 后端统一链路

```mermaid
flowchart LR
    P["插件实现\nInfo / Platform / OAuth / Assets"] --> G["sdk gRPC"]
    G --> M["core Manager"]
    M --> C1["缓存\nname -> instance\nplatform -> schema/models"]
    M --> C2["提取前端资源\n/plugins/{name}/assets/*"]
    C1 --> API["admin API"]
    API --> WEB["core web"]
```

## 6. 前端插件协议

```mermaid
flowchart LR
    A["插件前端模块\n默认导出"] --> B["routes?"]
    A --> C["accountForm?"]

    D["core web"] --> E["加载 /plugins/{name}/assets/index.js"]
    D --> F["注入 shared\nreact / react-dom / jsx-runtime"]
    D --> G["注入 props\ncredentials / onChange / accountType / onSuggestedName / oauth bridge"]

    E --> A
    F --> A
    G --> C
```

```ts
export interface PluginFrontendModule {
  routes?: Array<{ path: string; component: ComponentType }>;
  accountForm?: ComponentType<AccountFormProps>;
}
```

## 7. OAuth 边界

```mermaid
sequenceDiagram
    participant U as 用户
    participant F as 插件 accountForm
    participant C as core 通用 OAuth API
    participant P as 插件 OAuthHandler
    participant O as 上游 OAuth

    U->>F: 点击生成授权链接
    F->>C: oauth.start()
    C->>P: StartOAuth()
    P->>O: 生成授权 URL / state
    O-->>P: authorize_url
    P-->>C: authorize_url + state
    C-->>F: authorize_url + state
    F-->>U: 展示/复制授权链接

    U->>O: 浏览器授权
    O-->>U: 回调 URL
    U->>F: 粘贴完整回调 URL
    F->>C: oauth.exchange(callback_url)
    C->>P: HandleOAuthCallback(code, state)
    P->>O: token exchange
    O-->>P: access_token / refresh_token
    P-->>C: credentials + account_name
    C-->>F: credentials + account_name
```

## 8. 账号 schema 统一目标

```mermaid
flowchart LR
    T["PluginInfo.AccountTypes\n新模型"] --> API["/accounts/credentials-schema/:platform"]
    API --> WEB["默认表单 或 插件 accountForm"]

    F["PluginInfo.CredentialFields\n旧兼容"] -.仅兼容单类型插件.-> API
```

## 9. 插件类型支持边界

| 类型 | SDK | Core 当前 | 统一建议 |
| --- | --- | --- | --- |
| SimpleGateway | 有 | 已装配 | 正式支持 |
| AdvancedGateway | 有 | 未完整装配 | 暂不宣称正式支持 |
| Payment | 有 | 未完整装配 | 暂不宣称正式支持 |
| Extension | 有 | 未完整装配 | 暂不宣称正式支持 |

## 10. 落地顺序

```mermaid
flowchart LR
    S1["步骤 1\n统一 name / platform / display_name"] --> S2["步骤 2\n确定 PluginInfo 为运行时唯一真相"]
    S2 --> S3["步骤 3\n把前端插件协议正式化并版本化"]
    S3 --> S4["步骤 4\n让 AccountTypes 成为 schema 主模型"]
    S4 --> S5["步骤 5\n补齐或收窄插件类型支持声明"]
```

## 11. 本轮先收口（只动 Core）

```mermaid
flowchart LR
    A["文档先对齐"] --> B["插件列表补充\ndisplay_name / version / author / type"]
    B --> C["credentials-schema 接口\n优先返回 AccountTypes"]
    C --> D["Accounts 页面默认表单\n支持 account_types 多类型渲染"]
```

| 项目 | 本轮目标 |
| --- | --- |
| 插件元信息 | 让 `name` 和 `display_name` 在 core 管理端同时可见 |
| 默认账号表单 | 不再只认 `fields`，改为优先认 `account_types` |
| 兼容策略 | `fields` 继续保留，作为旧模型兼容输出 |

## 12. Core 统一策略

```mermaid
flowchart LR
    A["插件实现"] --> B["Info().ID"]
    B --> C["Core canonical plugin name"]
    C --> D["admin/plugins/:name/*"]
    C --> E["/plugins/{name}/assets/*"]
    C --> F["前端动态加载键"]
    C --> G["运行时 instances / routes / frontendPages"]
```

```mermaid
flowchart TB
    A["配置 name / 目录名 / 上传名 / GitHub repo 名"] --> B["仅作为 hint / source"]
    B --> C["如果和 Info().ID 不同"]
    C --> D["Core 建立 alias 兼容"]
    C --> E["Core API 一律返回 canonical name"]
```

| 字段 | 角色 |
| --- | --- |
| `Info().ID` | Core 内部唯一 canonical plugin name |
| `platform` | 账号、调度、计费业务键 |
| `display_name` | UI 展示名 |
| `plugins.dev[].name` | 仅开发模式 hint，实际以 `Info().ID` 为准 |

## 13. 本轮代码目标

```mermaid
flowchart LR
    A["统一 schema"] --> B["统一 plugin metadata 展示"]
    B --> C["统一 canonical plugin name = Info().ID"]
    C --> D["保留 alias 兼容旧 name / 目录名 / dev hint"]
```

## 14. plugin.yaml 定位

```mermaid
flowchart LR
    A["运行时元信息\nInfo / Platform / Routes / Models / AccountTypes"] --> B["manifest generator"]
    C["分发专属元信息\nmin_core_version / dependencies"] --> B
    B --> D["plugin.yaml"]
```

```mermaid
flowchart LR
    A["Core 运行时"] -.不读取 plugin.yaml 作为真相.-> D["plugin.yaml"]
    B["插件开发者"] --> C["改运行时代码"]
    C --> D
```

| 项 | 规则 |
| --- | --- |
| 运行时真相 | `Info().ID / Name / Version / Type`、`Platform()`、`Routes()`、`Models()`、`AccountTypes()` |
| 分发产物 | `plugin.yaml` |
| 生成方式 | `plugin.yaml` 由 generator 从运行时元信息生成 |
| 允许手写的字段 | 仅分发专属字段，如 `min_core_version`、`dependencies` |

## 15. SDK / 插件仓库落地规范

```mermaid
flowchart LR
    A["airgate-sdk"] --> A1["只统一语义"]
    A1 --> A2["PluginInfo.ID = canonical plugin name"]
    A1 --> A3["PluginInfo.Name = display_name"]
    A1 --> A4["Platform() = 业务键"]

    B["插件仓库"] --> B1["metadata.go\n单源声明"]
    B1 --> B2["Info() / Platform() / Routes()"]
    B2 --> B3["genmanifest"]
    B3 --> B4["plugin.yaml"]
    B3 --> B5["manifest sync test"]
```

```mermaid
flowchart TB
    A["允许修改的层"] --> B["metadata.go\n插件运行时元信息"]
    B --> C["gateway.go\n仅引用单源，不再手写重复字面量"]
    B --> D["genmanifest\n把运行时元信息投影成 plugin.yaml"]
    D --> E["plugin.yaml\n提交到仓库，供分发/发布使用"]
    D --> F["测试\n校验仓库内 plugin.yaml 与 generator 输出一致"]
```

| 层 | 规则 |
| --- | --- |
| `airgate-sdk` | 只补字段语义和职责注释，不在本轮引入新协议字段 |
| 插件运行时 | 统一从单源元信息构造 `Info / Platform / Routes` |
| 插件分发 | `plugin.yaml` 必须由 generator 生成，不再手工维护 |
| 防漂移机制 | 至少保留 1 个测试，校验 `plugin.yaml` 与 generator 输出完全一致 |
