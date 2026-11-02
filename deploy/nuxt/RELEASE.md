# 发布契约：双制品（Go + SSR）与版本校验

一个版本 = **两个必须来自同一 commit 的制品**：

| 制品 | 内容 | 作用 |
| --- | --- | --- |
| Go 二进制 | `-tags=embed`，内嵌 legacy Vite SPA（`backend/internal/web/dist`） | API + 回滚用 UI |
| Nuxt SSR 产物 | `frontend/.output`（Nitro `node-server`） | 正式页面与 BFF |

`release.json` 把两者绑在一起；任何一方和另一个不是同一 commit、或文件被替换，校验都会失败。

## 清单格式

`frontend-build.json`（Vite 写在 `backend/internal/web/dist/`，Nuxt 写在完整的 `frontend/.output/`）：

```json
{
  "schema": 1,
  "product": "sub2api-frontend",
  "mode": "spa | ssr",
  "version": "2.3.0",
  "commit": "<40 位 git sha>",
  "dirty": false,
  "node": "v24.22.x",
  "builtAt": "…",
  "entry": "index.html",
  "files": [{ "path": "assets/index-xxx.js", "sha256": "…", "size": 123 }],
  "manifestHash": "sha256(canonical(version+commit+mode+entry+files))"
}
```

- `manifestHash` 不含 `builtAt`，同一份源码重复构建得到同一个哈希。SSR 清单覆盖 `.output` 的 client、server 和 Nitro runtime，不只覆盖浏览器静态资源。
- Go 的 `//go:embed all:dist` 会把清单原样嵌进二进制，所以在二进制里能直接搜到 `manifestHash` 和 `commit`，无需运行程序即可校验。

`release.json`：

```json
{
  "schema": 1,
  "product": "sub2api",
  "version": "…",
  "commit": "…",
  "frontend": { "mode": "ssr", "manifestHash": "…", "version": "…", "commit": "…", "node": "…" },
  "backend": { "path": "…", "sha256": "…", "size": 123, "embeddedFrontend": { "manifestHash": "…" } }
}
```

## 命令

```bash
# 1) legacy SPA：构建 + 写清单（Makefile `make build` 已包含）
pnpm --dir frontend run build

# 2) 后端嵌入前自检：清单必须来自当前 checkout
pnpm --dir frontend run verify:embed
# 或者构建完成后直接验二进制确实嵌了这份 dist：
node frontend/scripts/build-manifest.mjs verify \
  --dir backend/internal/web/dist --mode spa --binary backend/bin/server

# 3) Nuxt SSR（Node 24.22.x；22 系需 >= 22.22.3）
pnpm --dir frontend run build:ssr
pnpm --dir frontend run verify:ssr

# 4) 绑定成一个发布：seal → verify → stage
node deploy/nuxt/release.mjs seal   --ssr frontend/.output --binary backend/bin/server \
  --spa-dist backend/internal/web/dist --out release.json
node deploy/nuxt/release.mjs verify --release release.json --ssr frontend/.output --binary backend/bin/server
node deploy/nuxt/release.mjs stage  --release release.json --ssr frontend/.output \
  --binary backend/bin/server --out dist/ssr

# 一键（Makefile）：先构建前后端，再执行上面三步
make release-ssr
```

`stage` 会生成 `dist/ssr/{sub2api,.output,release.json,deploy/,SHA256SUMS}`，打包：

```bash
tar -C dist -czf sub2api_<version>_ssr_linux_amd64.tar.gz ssr
```

## CI

- `release.yml`：前端构建改用 **Node 24.22**（Nuxt 4.6.3 要求 `^22.22.3`/`^24.15`，同一版本也能构建 Vite）。构建后校验清单 commit；GoReleaser 之前再对下载回来的 `frontend-dist` 校验一次——**不一致直接失败**，不会产出“后端新、UI 旧”的 release。
- `ssr-release.yml`：`release: published` 后触发，用同一 tag 构建 SSR，下载该 release 的 linux/amd64 二进制，`seal` 出 `release.json`，打成 `sub2api_<version>_ssr_linux_amd64.tar.gz` 上传到同一个 GitHub Release。
- Docker：`deploy/nuxt/Dockerfile` 以 `frontend/` 为上下文（含 `.output`）构建纯运行时镜像；Go 仍单独部署，`NUXT_BACKEND_ORIGIN` 指向它。

## 回滚

- 页面回滚：nginx 指回 Go 的静态入口即可——SPA 就在 Go 二进制里，且与当前 API 同 commit（`verify:embed` 已校验）。
- 版本回滚：`release.json` 里两个 sha 写明当时制品；回到旧 tag 的二进制 + 旧 `.output` 一起换，不要混用。
