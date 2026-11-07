# catalog/models.json

首方模型目录。后台每 6 小时从本文件拉一次（ETag 协商），改完 push 到 `main` 即生效，**不需要发版**；管理端 `POST /api/v1/admin/models/metadata/sync` 可立即刷新。

- 读取地址：`https://raw.githubusercontent.com/kiss-kedaya/sub2api/main/catalog/models.json`，可用环境变量 `SUB2API_MODEL_CATALOG_URL` 覆盖；设为 `off` 关闭远端目录、只用内置表。
- 优先级：本文件 > 内置首方表 > models.dev。文件里的 `reference_pricing` 只做展示（`reference_pricing` 字段），**实际计费价永远来自站点价卡**。
- 失败降级：文件拉不到 / JSON 不合法 / schema 不支持时保留上一份好快照；进程冷启动时先用内置表，后台补拉。

## 字段

| 字段 | 说明 |
| --- | --- |
| `display_name` / `description` | 展示名与简介 |
| `context_window` / `max_output_tokens` | 上下文与最大输出，token 数 |
| `input_modalities` / `output_modalities` | `text` / `image` / `video` / `audio` / `pdf` |
| `reasoning` | 是否支持思考 |
| `default_reasoning_level` / `supported_reasoning_levels` | 思考档位 |
| `tool_call` / `structured_output` / `attachment` / `temperature` | 能力标记 |
| `family` / `release_date` / `knowledge` / `open_weights` | 目录信息 |
| `reference_pricing` | `{input,output,cache_read,cache_write,unit}`，仅参考价 |

键名是模型 ID（小写）。查找会做归一化：去 vendor 前缀、去日期/`-high` 等后缀、点号版本转连字符（`claude-opus-4.5` → `claude-opus-4-5`）。

## 新增模型

在 `models` 里加一条即可；数值必须以官方文档为准，拿不到官方值就不写（留空让 models.dev 兜底），不要编造。
