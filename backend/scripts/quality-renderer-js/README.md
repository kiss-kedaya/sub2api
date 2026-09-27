# 隔离容器渲染器（C″ 方案）

让 **JS 驱动的动画作品**也能被质检判定：在隔离容器内执行不可信 JS，再用虚拟时钟把动画定位到四个时间点逐帧截图。

## 为什么需要它

内进程渲染器（`backend/scripts/quality-renderer/`）拒绝 `<script>`，所以动画写在脚本里的作品永远落到 `unknown`，当不了判据。实测最近 15 条 unknown 里，6 条（约 40%）的动画 100% 在 `<script>` 里（有的连一个 `@keyframes`、一个 `<animate>` 都没有）。

manxue.ai 的做法是**允许内联 JS 但在沙箱里执行**（`iframe sandbox=allow-scripts` + CSP `script-src 'unsafe-inline'; connect-src 'none'`）。本方案沿用同样的思路，但把隔离边界从「浏览器内」上移到「容器」。

## 已验证（2026-09-27，旧机 host-a）

| 验证项 | 结果 |
|---|---|
| rAF 驱动的动画四帧捕获 | **通过**，`times 0 / 0.69 / 1.53 / 2.37` |
| 帧间确有运动 | **通过**，`changed vs f0: 22951 / 22956 / 22923` 像素，四帧 sha 全不同 |
| 作品访问外网被拦 | **通过**，作品的 `fetch` 失败走 catch（圆圈渲染成绿色而非红色） |
| 容器出站全断 | **通过**，`1.1.1.1:443` 与 `8.8.8.8:53` 均 `Network is unreachable` |

## 用法

```bash
# 读 stdin 的 HTML，写 stdout 的四帧 JSON
bash run.sh < artwork.html > frames.json
```

环境变量：`QR_DIR`（渲染器目录）、`QR_IMAGE`、`QR_CHROME`、`QR_NETWORK`、`QR_TIMEOUT`。

## 隔离边界

**边界是容器，不是浏览器沙箱。** 容器内的 Chromium 无法启用 setuid sandbox（需要容器不提供的 userns 配置），所以 `chromiumSandbox: false` + `--no-sandbox` 是刻意的，**不是内进程渲染器的回退**——浏览器沙箱被容器沙箱替代，而不是被移除。

`run.sh` 施加的边界：

| 手段 | 作用 |
|---|---|
| `--network` internal bridge | 无出站路由（已验证） |
| `--cap-drop ALL --cap-add SYS_ADMIN` | 只留 Chromium 启动必需的那一个 |
| `--security-opt no-new-privileges` | 禁止提权 |
| `--memory 1g --cpus 1 --pids-limit 512` | 限制爆炸半径 |
| `--rm` | 一次性，跑完不留痕 |
| 只读挂载渲染器目录 | 作品改不了脚本 |
| 路由层 `route.abort()` + 无 JS 外链 | 双保险 |

**作品 JS 只允许在这个边界内跑。不得在放宽任何一条标志的情况下运行。**

## 关键实现细节（踩过的坑）

1. **`addInitScript` 对 `goto()` 加载的新文档会丢**，必须用 `setContent()` 加载作品。用 `goto` 时时钟句柄直接不存在。
2. **不能用 `--tmpfs /tmp`**：会让容器内的 Chromium 挂死（`exit 124`）。输出目录用 bind mount。
3. **容器内 Chromium 只接受 root 运行**：`--user 1001` 必挂，已逐项排除网络、`/dev/shm`、HOME、后台服务等因素。这是本镜像的特性，不是权限配置问题。
4. **`--network none` 也会让 Chromium 挂死**：要用 `--internal` bridge，而不是完全断网。
5. **`docker run` 必须加 `-i`**，否则 stdin 传不进去（渲染器报 `empty input`）。

## 虚拟时钟（`clock.mjs`）

改写自 manxue-ai 的公开预览控制器（Apache-2.0）。劫持这些时间源，让作品自己的时钟失效：

- `requestAnimationFrame` / `cancelAnimationFrame`
- `setTimeout` / `setInterval` / `clearTimeout` / `clearInterval`
- `performance.now` / `Date.now`
- Web Animations（`Animation.prototype.play/reverse`、`Element.prototype.animate`）
- SMIL（`SVGSVGElement.prototype.pauseAnimations/unpauseAnimations`）

对外只暴露 `window.__plateauClock`（不可写、不可枚举）：`seek(ms)` 绝对定位 SMIL/Web Animations，`advance(ms)` 以 16ms 步进让 rAF/定时器跑完。

`advance` 里回调走**微任务队列**而不是真实帧，所以跑 3 秒动画只要微秒级，不需要等 3 秒墙钟。步进结束后先清 `pumpQueue` 再关 playback，否则收尾动作本身会重新武装作品的回调（这个顺序踩过坑）。

## 接入状态（2026-09-28）

- **已接入 runner**：`assessScheduledVisualQuality` → `renderScheduledVisualFramesWithFallback`：
  先跑内进程渲染器（拒脚本），失败后改走本容器路径。两条都失败仍是
  `unknown`（"four-frame rendering unavailable"），绝不当 degraded。
- Go 调用配置：
  - `SUB2API_QUALITY_RENDERER_JS_SCRIPT`：`run.sh` 的绝对路径（设置了才启用容器路径）
  - `SUB2API_QUALITY_RENDERER_JS_RUNNER`：启动器，默认 `/bin/bash`；服务用户没有 docker 权限时
    设为 `/usr/bin/sudo`，并在 sudoers 里只放行这一个脚本（NOPASSWD，最小授权）
- 并发：容器路径串行（同时最多 1 个容器）；Go 侧超时 260s > run.sh 内部默认 240s，
  让 run.sh 先自行收尾，避免留下孤儿容器。

## 待完成

- 用真实的历史 unknown 样本回归（那 6 条 JS 驱动的作品）。
