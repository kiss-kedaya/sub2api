# SSR 双服务模板（Nuxt BFF + Go API）

新增文件，不改动现有生产部署配置（nginx / systemd / 雷池 / 端口）。

## 拓扑

```
浏览器 ──► nginx ──► 127.0.0.1:3000  Nuxt SSR (Node/Nitro)
                          └─ /api/v1/** ──► 127.0.0.1:8080 Go API（固定 origin）
浏览器 ──► 网关流量 /v1/messages、/v1/responses、/v1/chat/completions、WebSocket
            仍按现状直连 Go，不经过 Node
```

- Node 只承接**管理 API**（`/api/v1/**`）与页面渲染。
- 网关流量、SSE 长连接、WebSocket 升级不改语义，继续直连 Go。
- `/api/v1/**` 一律 `Cache-Control: no-store`（BFF 强制，不依赖 routeRules）。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `NUXT_BACKEND_ORIGIN` | Go API 固定 origin，例如 `http://127.0.0.1:8080`。只允许 http/https，禁止带账号密码。 |
| `NUXT_SESSION_PASSWORD` | 会话密封密钥（≥32 字节随机串）。为空时 BFF 拒绝对外转发（503），会话探针返回匿名 `configured:false`。 |
| `PORT` / `HOST` | 默认 `3000` / `127.0.0.1`，只给本机 nginx 用。联调 `4320`，预览 `4317`。 |
| `NUXT_SESSION_COOKIE_SECURE` | `auto`（默认）/`true`/`false`。`auto` 只在直连对端是 loopback/私网且 `x-forwarded-proto=https` 时加 Secure。 |
| `NUXT_BACKEND_TIMEOUT_MS` | 等到上游响应头的上限，默认 `180000`。只限首字节，之后的流不会被切断。 |

> `NUXT_SESSION_PASSWORD` 为空时：**公共接口照常代理（匿名）**，页面可以正常 SSR；只有登录/2FA/注册/刷新这些"要发 token"的接口返回 503，避免把 token 发进 payload。**正式环境必须配置**，否则用户无法登录。

## 会话契约

- 登录 / 2FA / 注册 / 刷新成功后，BFF 把 Go 的 token 对封进 `sub2_session`（HttpOnly、SameSite=Lax、HTTPS 下 Secure），**响应体里不再出现可用 token**：
  - `access_token` / `refresh_token` 置为空字符串（不是删字段，旧客户端写进 localStorage 是 `''`，请求拦截器因此不发送 Authorization）；
  - `data.session_managed: true`；
  - `data.csrf_token`：本次会话的 CSRF 值（公开值，给前端塞进运行期上下文）；
  - `user` / `expires_in` / `token_type` / `requires_2fa` / `temp_token` 原样保留。
- CSRF 字段位置：**`data.csrf_token`**（登录 / 刷新响应 `data` 下，与 `session_managed` 同级）；`GET /api/v1/auth/session` 也返回同名字段。
- 写操作请求头：**`X-Sub2api-Csrf`**（客户端 adapter 已用此名）；旧的 `X-CSRF-Token` 仍兼容。
- `GET /api/v1/auth/session`（BFF 自有，不转发）：`{ authenticated, session_managed, configured, csrf_token, expires_at }`。未配置密码时返回 `configured:false` 的匿名结构而不是 503，公共页可正常渲染。
- `GET /api/v1/auth/me` 等只读接口由 cookie 会话鉴权，SSR 里用 `useRequestFetch()` 把 cookie/origin 带进来即可。
- 401 时 BFF 用会话里的 refresh token 静默续期并重放一次。
  - 同一进程内按 refresh token 单飞：并行 401 只花一次滚动 token。
  - 续期失败**不清 cookie**，401 原样返回；多实例/多标签竞争丢掉的那一方不会把用户踢下线，只有显式 logout 才清。

## CSRF / 防开放代理

- 路径：只接受 `/api/v1/**`，拒绝 `..`、`%2f`、`%5c`、`//`、反斜杠、控制字符、绝对 URL、query 混进路径。
- 方法：GET/HEAD/POST/PUT/PATCH/DELETE。
- 只发往 `NUXT_BACKEND_ORIGIN`；请求头走白名单，会话 cookie 由 BFF 管理，无会话时保留 Bearer 兼容；`Host` / 转发头不进上游。
- `X-Forwarded-For` / `X-Real-IP` 仅当直连对端是 loopback/私网时才采信上游链，防止客户端伪造 IP 绕过 Go 的限流。
- cookie 会话下的写操作：必须带 `X-CSRF-Token`（等于会话里的 CSRF 值）且 `Origin`/`Referer` 与 `Host` 同源；登录/2FA/注册/刷新只需同源校验；Bearer 客户端不走 CSRF（无环境凭证）。
- 上游 `Set-Cookie` 一律丢弃，BFF 只写自己的会话 cookie。

## 联调端口

- Nuxt dev / 联调：`PORT=4320`。
- 纯 mock upstream 预览：`PORT=4317`，`NUXT_BACKEND_ORIGIN` 指向本地 mock，不接生产 Go。
- 生产模板仍按 `3000` 写，接入时改 `PORT` 与 nginx upstream 即可。

## systemd

`sub2api-ssr.service` 为模板：复制到 `/etc/systemd/system/`，改 `WorkingDirectory` / `User`，环境变量放 `EnvironmentFile`（权限 0600）。启动：

```
sudo systemctl daemon-reload
sudo systemctl enable --now sub2api-ssr
curl -fsS http://127.0.0.1:3000/health/frontend
```

## nginx

`nginx-ssr.example.conf` 只是参考片段：静态资源走 Node，`/api/v1/` 走 Node（BFF），网关路径继续按现有方式直连 Go。它与现网 `sites-enabled` 不冲突，未接入前不要替换现有配置文件。

## 版本契约与发布打包

页面和 Go API 必须来自同一个 commit。构建链在两端各写一份 `frontend-build.json`（版本、commit、每个文件的 sha256、`manifestHash`），Go 二进制因为 `-tags=embed` 会把 SPA 那份原样嵌进去；`release.json` 再把 SSR 产物和二进制绑在一起。

```bash
pnpm --dir frontend run build          # legacy SPA + 清单（Go 回滚用）
pnpm --dir frontend run build:ssr      # Nuxt SSR（Node 24.22.x）+ 清单
make release-ssr                       # seal → verify → stage
```

细节（清单字段、二进制内嵌校验、CI 的两道 commit 校验、Docker 构建）见 [RELEASE.md](./RELEASE.md)。

## 回滚

- 停 `sub2api-ssr`，把 nginx 指回原来的静态入口即可；Go 未改动。
- 会话 cookie 只影响新前端：清 `sub2_session` 即可回到未登录态。
