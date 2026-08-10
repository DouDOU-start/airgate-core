# 仓库内模型价格目录

`model_prices_and_context_window.json` 是模型价格同步功能使用的仓库内置目录，格式兼容 LiteLLM 的模型价格 JSON，并支持 AirGate 扩展字段。

## 修改方式

直接编辑 JSON 后提交即可。重新构建并重启服务后，管理后台「模型价格」页面的「同步模型」和「一键同步价格」会读取新目录。

价格字段单位如下：

- `input_cost_per_token`、`output_cost_per_token`：USD / token；
- `cache_read_input_token_cost`：缓存读取 USD / token；
- `cache_creation_input_token_cost`、`cache_creation_input_token_cost_above_1hr`：缓存写入 USD / token；
- `output_cost_per_image`：USD / 张。

## AirGate 扩展字段

`airgate_pricing_extra` 用于保存 LiteLLM 目录无法表达的本地计费信息，例如：

- `service_tiers`：服务档倍率；
- `long_context`：长上下文阈值和倍率；
- `image`：图像分辨率价格；
- `video`：视频按秒和分辨率价格。

## 同步语义

- 「同步模型」只导入管理员勾选的条目；
- 「一键同步价格」会刷新已有模型及 CPA 支持模型；
- 本地数据库中的自定义 `pricing_extra` 字段会保留，目录中明确提供的 AirGate 扩展字段会更新它；
- 同步不会删除数据库中不在目录内的模型。

`backend/internal/bootstrap/priceseed/model-prices.seed.yaml` 仍然是首次启动时的补缺种子，采用只新增不覆盖语义；日常同步和调价应维护本目录或使用管理后台。
