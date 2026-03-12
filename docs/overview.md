# AirGate Core 概览

`airgate-core` 是 AirGate 的内核。

一句话理解它：

> Core 负责统一管理、统一装配，插件负责具体平台能力。

## 3 个仓库怎么分

- `airgate-core`
  - 运行时
  - 管理后台
  - 账号、计费、插件装配
- `airgate-sdk`
  - 插件接口
  - gRPC 协议
  - 插件开发规范
- `airgate-openai`
  - 一个真实插件实现

## Core 负责什么

- 用户、分组、账号、API Key、使用记录
- 插件安装、启用、禁用、卸载
- 动态注册插件路由
- 托管插件前端资源
- 做统一调度、限流、并发控制、计费

## Core 不负责什么

- 不定义插件接口，接口在 `airgate-sdk`
- 不保存平台专属逻辑，平台能力在插件里
- 不把 `plugin.yaml` 当成运行时真相

## 关键概念

| 概念 | 含义 |
| --- | --- |
| `PluginInfo.ID` | 插件运行时唯一标识，Core 用它做 API、资源路径和缓存键 |
| `platform` | 业务平台键，用在账号、调度、计费 |
| `display_name` | UI 展示名 |
| `plugin.yaml` | 分发产物，用于安装和发布 |

## 一个请求怎么走

1. 客户端请求 Core 暴露的网关路径
2. Core 选择账号并做限流、并发控制、计费准备
3. Core 调用插件转发请求
4. 插件和上游平台通信
5. Core 记录使用量并返回结果

## 一个插件怎么接进来

1. Core 启动插件进程
2. 通过 gRPC 读取 `Info / Platform / Routes / Models`
3. Core 注册插件路由和前端资源
4. 管理后台显示插件和账号表单

## 继续看

1. `architecture.md` — Core 和插件的协作机制
2. `marketplace.md` — 插件市场安装流程
