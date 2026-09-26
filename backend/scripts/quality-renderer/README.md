# SVG 四帧渲染器

独立 Node.js 工具，未接触网关或生产配置。需要 Node.js 20.19.0+，已使用 20.19.2 验证。
本目录执行 `npm ci`；浏览器用 `npx playwright install chromium` 安装。
Linux 系统依赖用管理员执行 `npx playwright install-deps chromium` 安装。
也可设置 `QUALITY_CHROMIUM_EXECUTABLE_PATH` 指定兼容的 Chromium 可执行文件。
Playwright 固定为 1.63.0，完整依赖及校验值见 package-lock.json。

## 调用

直接运行 `node /绝对路径/backend/scripts/quality-renderer/render.mjs`。
stdin 写 UTF-8 HTML 后关闭，最多 1 MiB，必须包含一个 SVG。
stdout 仅输出 JSON 数组，固定四个 `{time:number,png:base64}` 对象。
不要通过 `npm run render` 接协议调用，它会在 stdout 打印 npm 信息。
PNG 为 960x640。time 单位为秒，采样比例 `0 / .23 / .51 / .79`。
取作品中有效 SMIL/CSS 时长的最短值作为周期，有效范围 0.5 到 10 秒，
无有效周期时采用 3 秒。暂停 SMIL/CSS 后设置采样时间；静态作品允许四帧相同。
保留内联及 HTML/SVG 内 style、祖先选择器；截图仅包含 SVG，不包含网页文字。

成功退出 0；错误退出 1，stdout 为空，stderr 只写错误类别，不回显输入。
输出含 JSON/base64 开销最多 8 MiB。读取 stdin 也计入超时。
26.5 秒开始清理，27.5 秒强杀并预留进程树清理时间，30 秒硬截止
（包括 stdout 不被读取的情况）。
独立浏览器守卫通过 IPC 断开检测渲染进程异常退出，并关闭浏览器。

Go 调用配置：`SUB2API_QUALITY_RENDERER_SCRIPT` 必须是 render.mjs 的绝对路径，
`SUB2API_QUALITY_RENDERER_NODE` 默认 node。root 服务使用 runuser 包装以隔离的
普通用户执行 Node，并原样传递参数、stdin/stdout；不可直接以 root 启动脚本。
脚本拒绝 Linux root；本目录不创建用户、不修改服务、不实施生产部署。

## 安全依赖

明确设置 `chromiumSandbox:true`，禁止降级到 `--no-sandbox`。
Linux 普通用户需要内核 user namespace 或有效的 Chromium sandbox 环境。
被动 HTML/SVG 白名单拒绝脚本、事件属性、iframe/object/embed/foreignObject、
表单、模板、危险 URL、危险 SMIL 属性修改。允许 charset 和 name 型 meta；
所有 http-equiv（包括 refresh 和 CSP）均拒绝，输入不能覆盖安全策略。
外加响应头 CSP、禁 JavaScript、离线模式、HTTP/WebSocket 拦截、禁 service worker、禁下载。
外部图片、CSS import、外部字体不加载。系统字体可能导致跨机器像素不同。
控制端 page.evaluate 只运行本工具固定代码，不运行输入提供的脚本。
操作系统还应限制出站、文件权限、CPU/内存及并发；浏览器应及时更新安全补丁。
Windows 强制清理依赖 taskkill.exe，Linux 使用进程组；不覆盖整机掉电或整个进程组
连同守卫同时被 SIGKILL 的情况，部署应由 systemd/cgroup 清理整个任务组兜底。

## 验证

`npm test`：真实 Chromium 四帧像素、CSS/SMIL 时间、安全防护、限额、超时、异常清理。
`node test/batch-render.mjs <样本目录>`：有界并发检查目录内所有 HTML，单独验证指定回归样本。
只输出成功失败计数；本目录 test-output/batch-results.json 保存文件名及错误类别，
不保存样本源码、作品 PNG 或凭据。合成测试截图也在 test-output，该目录不进 git。
