# 各客户端 Base URL 填法与 /v1 前缀兼容规则

> 适用范围：本仓库（sub2api）自带的 API 网关。目标读者是配置客户端的中转用户与排障人员。
> 结论先行：Base URL 填 `https://kedaya.ai` 或 `https://kedaya.ai/v1` **都能用**；多写几个 `/v1`、
> 或者一个 `/v1` 都不写，服务端也会自动纠正到规范路径。

## 1. 为什么会有这个坑

不同协议的客户端对 Base URL 的约定不一样：

- **Anthropic SDK / Claude Code**：Base URL 应该是**裸域名**，客户端自己拼 `/v1/messages`；
- **OpenAI SDK / 多数 OpenAI 兼容客户端**：Base URL 通常要**带 `/v1`**，客户端再拼 `/chat/completions`。

用户在同一个网关里混用两种填法，就会出现 Base URL 已经带了 `/v1`、客户端又拼一次，请求变成
`POST /v1/v1/messages`；或者按 OpenAI 习惯填了带 `/v1` 的地址去跑 Anthropic 协议，少拼/多拼都会 404。

服务端现在对这两种错法都做了容错：**网关协议路径的 `/v1` 前缀个数不再敏感**。

## 2. 服务端兼容规则

网关会在路由匹配之前规范化请求路径（只改路径，不改 query string、header、body、HTTP method），
然后仍走同一套 `/v1` 路由和同一套中间件（鉴权、限流、计费、日志与直接请求规范路径完全一致）。

| 请求路径 | 规范化结果 | 说明 |
| --- | --- | --- |
| `/v1/messages` | `/v1/messages` | 规范写法 |
| `/v1/v1/messages` | `/v1/messages` | Base URL 带了 `/v1`，客户端又拼一次（最常见） |
| `/v1/v1/v1/messages` | `/v1/messages` | 多写两次 |
| `/v1/v1/v1/v1/v1/v1/messages` | `/v1/messages` | 连续 6 个 `/v1` 也照样折叠，前缀个数不设上限 |
| `/messages` | `/v1/messages` | 完全没写 `/v1`（Anthropic 习惯填裸域名） |

覆盖的网关协议根路径（含子路径）以网关实际注册的路由为准，主要包括：

`messages`、`messages/count_tokens`、`responses`（含 `/responses/*` 子路径）、`chat/completions`、
`models`、`models/:model`、`embeddings`、`images/*`、`videos/*`、`tools/*`、`alpha/search`、
`tts`、`stt`、`custom-voices/*`、`realtime`、`web_search`、`x_search`、`live`、`usage`、`systemone`。

**不会被误伤的路径**：

- `/api/v1/*`（后台管理 API，与网关 `/v1` 无关）；
- `/v1beta/*`（Gemini 原生 API，前缀是 `/v1beta` 而不是 `/v1`）；
- `/admin/*`、静态资源（`/assets/*` 等）、`/health`、`/readyz` 探针；
- SPA 页面路由，包括与网关同名的 `/usage` 页面（只有显式 `/v1/usage` 或 `/v1/v1/usage` 才走网关）。

**诊断友好**：折叠后仍然不存在的 `/v1/v1/*` 子路径会返回 JSON 404（不是空 404），例如：

```json
{
  "error": {
    "type": "invalid_request_error",
    "code": "gateway_path_not_found",
    "message": "Gateway path \"/v1/v1/messages/typo\" does not exist after collapsing duplicated /v1 prefixes to \"/v1/messages/typo\".",
    "hint": "Base URL 请填写 https://kedaya.ai（Anthropic SDK / Claude Code 推荐）或 https://kedaya.ai/v1（OpenAI SDK 推荐）；重复的 /v1 前缀会被自动折叠为单个 /v1，但 /v1/messages/typo 不在网关支持的协议路径内，请检查路径是否拼错。",
    "original_path": "/v1/v1/messages/typo",
    "normalized_path": "/v1/messages/typo"
  }
}
```

## 3. 各客户端推荐填法

| 客户端 / SDK | 推荐 Base URL | 说明 |
| --- | --- | --- |
| Anthropic SDK（Python / TypeScript） | `https://kedaya.ai` | SDK 自己拼 `/v1/messages`；填 `https://kedaya.ai/v1` 也能用（服务端折叠重复前缀） |
| Claude Code（`ANTHROPIC_BASE_URL`） | `https://kedaya.ai` | 同上；`ANTHROPIC_BASE_URL` 填裸域名最稳 |
| OpenAI SDK（Python / Node，`base_url`） | `https://kedaya.ai/v1` | SDK 拼 `/chat/completions`；填裸域名也能用（服务端补 `/v1`） |
| OpenAI SDK 使用 Responses API | `https://kedaya.ai/v1` | 请求落到 `/v1/responses`；裸域名同样可用 |
| Cursor（OpenAI 兼容自定义 API） | `https://kedaya.ai/v1` | 需要填写完整 `/v1` 的客户端，按 OpenAI 习惯 |
| Codex CLI / Codex app | `https://kedaya.ai/v1` | 模型发现与 Responses 请求都走 `/v1/*`；`/backend-api/codex/*` 是 Codex 专用直达入口，不是 Base URL |
| Gemini SDK / CLI | `https://kedaya.ai` | 走 `/v1beta/*`，不能写成 `.../v1`（`/v1beta` 不是 `/v1`，服务端不会改写） |
| 其他 OpenAI 兼容客户端 | 先按 `https://kedaya.ai/v1` 填 | 填错成裸域名或多写 `/v1` 也会被自动纠正，不需要改配置 |

> 一句话记法：**Anthropic 系填裸域名，OpenAI 系填 `/v1`；填反了或多写了也能用，但推荐按上表填。**

## 4. 排障与统计

服务端为每一次路径规范化记一条 **Info** 级结构化日志（不刷 WARN/ERROR）：

字段：`component=gateway.path_alias`、`rule`（`duplicate_version_prefix` / `missing_version_prefix`）、
`original_path`、`normalized_path`、`duplicate_v1_prefixes`、`method`、`user_agent`。

同时访问日志（`component=http.access`）会带上 `gateway_original_path`、`gateway_path_alias_rule`
（必要时含 `duplicate_v1_prefixes`），可以直接按 `user_agent` / `gateway_original_path` 统计：

- 哪些客户端把 Base URL 填成了带 `/v1` 的形式（`duplicate_version_prefix`）；
- 哪些客户端完全没带 `/v1`（`missing_version_prefix`）。

排查步骤：

1. 用 `curl -i -X POST https://kedaya.ai/v1/v1/messages` 看是否返回 `401`（说明路由已命中、只是缺 Key）；
   返回 `404` + `gateway_path_not_found` 说明子路径写错，按 `hint` 修正。
2. 服务端日志按 `component=gateway.path_alias` 过滤，看 `original_path` 与 `normalized_path`。
3. 折叠规则是 Go 层实现的，本地运行、IP 直连端口与经过反向代理的行为完全一致，不依赖 nginx 改写。

## 5. 实现位置

- `backend/internal/server/middleware/gateway_path_alias.go`：路径规范化 handler（纯函数折叠 + 路由命中校验 + 诊断 JSON + 日志）。
- `backend/internal/server/http.go`：在 `ProvideHTTPServer` 里把 gin 引擎包进规范化 handler，确保所有入口（含直连端口）生效。
- `backend/internal/server/middleware/logger.go`：访问日志补充 `gateway_original_path` 等字段。
- 单测：`gateway_path_alias_test.go`（middleware / routes / server 三层，覆盖 0/1/2/3/6 个 `/v1` 前缀、query/header/body 保留、反证不误伤）。
