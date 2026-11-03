# SSR 双服务模板（Nuxt BFF + Go API）

模板/示例文件，不改动现有生产部署配置（nginx / systemd / 雷池 / 端口）。

## 拓扑

```
浏览器 ──► 外层 nginx ──► upstream sub2api_bff
                              ├─ 127.0.0.1:3118  Nuxt SSR slot A
                              └─ 127.0.0.1:3119  Nuxt SSR slot B（可选，同版）
                                    │
                                    └─ NUXT_BACKEND_ORIGIN=http://127.0.0.1:3187
                                          │
                                          3187 本地 dispatch
                                          └─ upstream sub2api_go_bff
                                                ├─ local Go primary（127.0.0.1:8080）
                                                └─ peer Go backup（同版）
浏览器 ──► /v1 网关流量 ──► 直接 Go，不经过 Node / 3187
```

- 本次 502 的根因是 **3187 dispatch 硬编码单个 Go 后端**；外层 Node 双 slot 不是根因修复。
- 3187 dispatch 必须至少两个同版 Go 后端：local primary + peer backup；SSR 的 `NUXT_BACKEND_ORIGIN` 指向 `http://127.0.0.1:3187`，不要直接写 Go 端口。
- 外层 Node BFF 也可以做双 slot，但它是附加可用性，不是这次故障的修复点。
- 网关流量、SSE 长连接、WebSocket 升级不改语义，继续直连 Go。
- `/api/v1/**` 一律 `Cache-Control: no-store`。
- `proxy_next_upstream` 只重试 error/timeout/502/503/504，不启用 `non_idempotent`：GET/HEAD 可切 slot，登录/提交 POST 不重放。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `NUXT_BACKEND_ORIGIN` | 稳定的本地 3187 dispatch，例如 `http://127.0.0.1:3187`。禁止直接写单个 Go 后端端口。只允许 http/https，禁止带账号密码。 |
| `NUXT_SESSION_PASSWORD` | 会话密封密钥（≥32 字节随机串）。为空时登录、注册、刷新和 OAuth 均失败关闭（503），机器登录也不例外；会话探针返回匿名 `configured:false`。 |
| `PORT` / `HOST` / `NITRO_PORT` | 默认 `3118` / `127.0.0.1`，只给本机 nginx 用。联调 `4320`，预览 `4317`。HA 两个实例用 `3118`/`3119`，同时设置 `PORT` 和 `NITRO_PORT`（Nitro 实际监听以 `NITRO_PORT` 为准）。 |
| `NUXT_SESSION_COOKIE_SECURE` | `auto`（默认）/`true`/`false`。`auto` 只在直连对端是 loopback/私网且 `x-forwarded-proto=https` 时加 Secure。 |
| `NUXT_BACKEND_TIMEOUT_MS` | 等到上游响应头的上限，默认 `180000`。只限首字节，之后的流不会被切断。 |

> `NUXT_SESSION_PASSWORD` 为空时：公共接口仍可代理，但浏览器和机器均不能登录、注册或刷新。正式环境必须配置该密钥，不能把未配置 Cookie 会话当作机器免鉴权入口。

## 会话契约

- 浏览器登录（包括通行密钥）/ 2FA / 注册 / 刷新成功后，BFF 把 Go 的 token 对封进 `sub2_session`（HttpOnly、SameSite=Lax、HTTPS 下 Secure），**浏览器响应体里不再出现可用 token**：
  - `access_token` / `refresh_token` 置为空字符串（不是删字段，旧客户端写进 localStorage 是 `''`，请求拦截器因此不发送 Authorization）；
  - `data.session_managed: true`；
  - `data.csrf_token`：本次会话的 CSRF 值（公开值，给前端塞进运行期上下文）；
  - `user` / `expires_in` / `token_type` / `requires_2fa` / `temp_token` 原样保留。
- 服务端监控客户端兼容原有 JSON Bearer 契约：无会话、Cookie、Origin、Referer 和浏览器 Fetch Metadata 的 `application/json` 登录 / 2FA / 注册 / 刷新请求，保留 Go 返回的 `access_token`、`refresh_token`、有效期和错误。后续用 `Authorization: Bearer <access_token>` 请求 `/api/v1/auth/me` 查询自己的余额，刷新时提交自己的 `refresh_token`。不会为机器请求创建或清除浏览器会话。
- Nuxt adapter 明确发送 `X-Sub2api-Auth-Mode: cookie`。这是响应格式提示，**不是鉴权凭据**；自定义 `bearer` 提示不能覆盖已有 Cookie、浏览器来源或 CSRF 保护，也不按 User-Agent 放行。OAuth 回调与完成注册仍只走 Cookie 契约。
- 两种契约都由 Go 校验账号密码、已启用的二次验证、验证码/风控、Token 有效期与撤销状态及接口权限。监控客户端不支持二次验证时应补齐客户端支持，不能要求用户关闭二次验证。
- CSRF 字段位置：**`data.csrf_token`**（登录 / 刷新响应 `data` 下，与 `session_managed` 同级）；`GET /api/v1/auth/session` 也返回同名字段。
- 不再强制额外的双提交 CSRF Token。历史 `csrf_token`、`X-Sub2api-Csrf` 与 `X-CSRF-Token` 字段保留兼容，但缺失或过期不会阻断同源操作。
- `GET /api/v1/auth/session`（BFF 自有，不转发）：`{ authenticated, session_managed, configured, csrf_token, expires_at }`。未配置密码时返回 `configured:false` 的匿名结构而不是 503，公共页可正常渲染。
- `GET /api/v1/auth/me` 等只读接口由 cookie 会话鉴权，SSR 里用 `useRequestFetch()` 把 cookie/origin 带进来即可。
- 401 时 BFF 用会话里的 refresh token 静默续期并重放一次。
  - 同一进程内按 refresh token 单飞：并行 401 只花一次滚动 token。
  - 续期失败**不清 cookie**，401 原样返回；多实例/多标签竞争丢掉的那一方不会把用户踢下线，只有显式 logout 才清。

## CSRF / 防开放代理

- 路径：只接受 `/api/v1/**`，拒绝 `..`、`%2f`、`%5c`、`//`、反斜杠、控制字符、绝对 URL、query 混进路径。
- 方法：GET/HEAD/POST/PUT/PATCH/DELETE。
- 只发往 `NUXT_BACKEND_ORIGIN`；请求头走白名单，会话 cookie 由 BFF 管理，无会话时保留 Bearer 兼容；`Host` / 转发头不进上游。
- 公网入口覆盖客户端自带的转发 IP；有 WAF/CDN 时先配置准确的可信代理。BFF 只在直连对端可信时从右往左剥离内部代理跳数，遇到首个外部 IP 即停止，避免采信客户端伪造的最左侧值。静默续期保留同一 IP 和 User-Agent，以兼容 Go 的会话绑定。
- Cookie 会话下的写操作（包括刷新、退出）：不要求额外 Token，但 `Origin`/`Referer` 必须与 `Host` 同源，跨站 Fetch Metadata 被拒绝。登录/2FA/注册即使尚无会话，也必须同源；不会让跨站登录把浏览器绑定到攻击者账号。纯机器 Bearer 请求无环境凭证，仍由 Go 验证。
- 上游通用 `Set-Cookie` 与 CORS 响应头丢弃；只在准确匹配的 OAuth 路径桥接允许的 HttpOnly OAuth Cookie。机器 Token 兼容不会放宽 CORS。
- HttpOnly 只阻止脚本直接读取 Cookie，不等于防住 XSS 代操作、Cookie 被盗或服务端密钥泄漏；仍须保持 HTTPS、CSP、会话撤销与日志脱敏。
- 浏览器显式刷新必须已有有效密封会话，不收养请求体传入的刷新凭证。退出时 BFF 向 Go 提交当前会话的刷新凭证，访问令牌过期也可撤销该刷新族；只有清除本机 Cookie 不等于服务端撤销成功，后端失败仍按错误返回。

## 联调端口

- Nuxt dev / 联调：`PORT=4320`。
- 纯 mock upstream 预览：`PORT=4317`，`NUXT_BACKEND_ORIGIN` 指向本地 mock，不接生产 Go。
- 生产/示例：SSR `3118`（slot A）/ `3119`（slot B，可选）；两个实例共用同一 `.env`，只改 `PORT` 和 `NITRO_PORT`。

## systemd

`sub2api-ssr.service` 为模板：复制到 `/etc/systemd/system/`，改 `WorkingDirectory` / `User`，环境变量放 `EnvironmentFile`（权限 0600）。模板已按 `3118` 写，并同时设置 `PORT` / `NITRO_PORT`。

HA 需要两个 SSR 实例（可选）。复制两份 unit（或改成 `sub2api-ssr@.service` 模板），共用同一个 `EnvironmentFile`，只把端口改成 `3118` / `3119`；两个实例都起来后分别探活：

```
sudo systemctl daemon-reload
# 分别启动两份 unit（slot A / slot B，按实际 unit 名）
curl -fsS http://127.0.0.1:3118/health/frontend
curl -fsS http://127.0.0.1:3119/health/frontend
```

## nginx（3187 dispatch HA）

`nginx-ssr.example.conf` 是示例模板，不是生产配置。它现在包含两个 upstream：

- `upstream sub2api_bff`：外层 nginx -> Node SSR slot（示例 3118/3119）。这是外层可用性，**不是**本次 3187 dispatch 根因修复。
- `upstream sub2api_go_bff`：3187 本地 dispatch -> Go。必须有至少两个同版 Go 后端，`local primary + peer backup`；示例 peer 用 `192.0.2.10:8080` 文档地址，生产必须换成真实 peer 内网 IP:端口。

链路要求：

1. `ssr.env.example` 的 `NUXT_BACKEND_ORIGIN=http://127.0.0.1:3187`，指向本地 dispatch，不指向 8080 等 Go 端口。
2. 3187 dispatch 只监听 `127.0.0.1:3187`，只接 `/api/v1/**`；`/v1` 网关仍在外层 nginx 直连 Go。
3. 两个 Go 后端必须同版（与 `NUXT_EXPECTED_BACKEND_VERSION` 一致）；两个 SSR slot 也必须同版。
4. `proxy_next_upstream` 只重试 error/timeout/502/503/504，不带 `non_idempotent`；GET/HEAD 可切，登录/提交 POST 不重放。
5. 3187 dispatch 必须透传 BFF 清洗后的 `Host` / `X-Forwarded-Host` / `X-Forwarded-Proto` / `X-Forwarded-For` / `X-Real-IP`；不要用 `$host` / `$scheme` / `$remote_addr` 覆盖，否则 Go 的同源校验和客户端 IP 限流会看到 `127.0.0.1` / `http`。

接入/发布前：

1. 两个 Go slot 分别探活（按实际健康接口）；两个 SSR slot 分别 `curl -fsS http://127.0.0.1:3118/health/frontend` / `3119`。
2. `nginx -t` 校验语法。
3. 校验 dispatch/Go HA + origin：
   ```
   node deploy/nuxt/check-bff-ha.mjs check deploy/nuxt/nginx-ssr.example.conf --env deploy/nuxt/ssr.env.example
   sudo nginx -T > /tmp/nginx.conf
   node deploy/nuxt/check-bff-ha.mjs check /tmp/nginx.conf --env deploy/nuxt/ssr.env.example
   ```
4. 滚动发布：先起新 Go/SSR slot 并探活，改 dispatch upstream 后 reload；旧 slot 留到连接清空。不要为了省事把 3187 upstream 删成单后端。
5. 回滚：把 3187 dispatch upstream 指回旧 Go slot，仍保留两个 Go slot；不要改成单端口。

检查器会拒绝：3187 dispatch 单 Go 后端、缺 peer backup、dispatch 里的字面量 Go `proxy_pass`、覆盖 BFF forwarded 头、`NUXT_BACKEND_ORIGIN` 指向 8080、网关路径指到 dispatch/BFF、缺 `proxy_next_upstream`/`tries`。外层 Node 单 slot 只告警，不会误报成这次根因。`non_idempotent` 只告警。

## 版本契约与发布打包

页面和 Go API 必须来自同一个 commit。构建链在两端各写一份 `frontend-build.json`（版本、commit、每个文件的 sha256、`manifestHash`），Go 二进制因为 `-tags=embed` 会把 SPA 那份原样嵌进去；`release.json` 再把 SSR 产物和二进制绑在一起。

```bash
pnpm --dir frontend run build          # legacy SPA + 清单（Go 回滚用）
pnpm --dir frontend run build:ssr      # Nuxt SSR（Node 24.22.x）+ 清单
make release-ssr                       # seal → verify → stage
```

细节（清单字段、二进制内嵌校验、CI 的两道 commit 校验、Docker 构建）见 [RELEASE.md](./RELEASE.md)。

## 回滚

- 把 3187 dispatch 的 `sub2api_go_bff` 指回旧 Go slot，保留两个 Go slot；不要改成单端口。外层 Node slot 按需保留。
- 会话 cookie 只影响新前端：清 `sub2_session` 即可回到未登录态。
