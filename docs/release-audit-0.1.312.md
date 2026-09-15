# 0.1.312 线上审查与修复记录

日期：2026-09-15

后续发布与实时验证见 [0.1.313生产验证记录](release-audit-0.1.313.md)。下列采样和“尚未部署”描述属于312审查时点，不代表最新部署状态。

## 当前结论

- 线上 312 仍在运行，8082 active；303 的 8080、8081 进程一直保活。
- Grok 三个入口真实请求均返回 200 和 usage，但可见文本首字仍测到约 12.8--16.3 秒，不能称为首字问题已经彻底解决。修复已先在代码和定向测试中完成，需新版本线上复测。
- 312 的 RSS 约 1.7GB，pprof HeapAlloc 约 1.75GB，HeapIdle 约 2.77GB、HeapReleased 约 2.31GB；内存会回收，当前证据更像高并发在途请求和分配峰值，尚未证明永久泄露。
- goroutine 约 2358，其中约 334 个位于 Responses handler 栈。已确认流生命周期存在阻塞读取风险，补丁必须保留断连后的 usage 排水语义。

## 线上证据

- 真实 Grok 4.6 测试：Responses 首事件约 12.0s、可见文本约 16.25s；Chat 首事件约 6.4s、可见文本约 12.86s；Messages 首事件约 5.5s、可见文本约 12.98s。三次均有 usage 记录。
- 三条 usage（180176655/180177358/180177369）的转发耗时分别约 10.03/3.68/3.47s，均指向 apikey 上游；本地测量与转发耗时并非同一边界，差额尚未定位。同账号极短 Chat 对照：网关 reasoning/text 2.89/5.09s，直连 3.86/4.81s；上游均消耗思考 token。本次未复现网关积攒正文，仅一组样本不足以解释全部长尾。
- 账号 29157 实测返回 HTTP 200、text/plain、29 字节普通文本，非 JSON/SSE、无 usage。合法 JSON+错误 SSE 头的修复不能覆盖这类实际协议违约；应在响应尚未提交时切换候选账号，不能伪造 usage 或返回成功。
- 08:50 前重新结构化统计 rollout 日志，8082 全窗口 HTTP 502=245、503=57。全量后 08:15:35--08:20:35 五分钟为 502=29、503=1，期间健康探针均 200。不能将这些 API 错误等同于 nginx 连接故障，仍需逐项归因。此前“0”是统计脚本先截取 upstream 再反查前置 status 的错误。
- 日志中的 413（Grok payload too large）、Responses `parse response: invalid json response`、流 `missing terminal event` 均有真实样本，不能用 `/health` 通过替代功能正常。
- CPU pprof 热点包括 `gjson.parseSquash` 约 19.8%、`gjson.ValidBytes` 约 6.8%、Responses 工具 schema 清洗约 10%；这是优化方向，未经基准和语义测试不得直接重构。

## 已合入代码

- `e5b2a0bb9`：非流式响应按实际 body 形状判断 JSON/SSE。合法 JSON 不再因错误 `text/event-stream` 头被误转；SSE 转 JSON 后响应头为 JSON；补充 usage、Content-Type、取消读取测试。
- `0916918be`：Grok Chat、Messages 经 Responses 转换时不再把 response.created 计作首字，沿用 semantic/visible 设置；Grok Responses 在终止帧边界、原生 Chat 在 [DONE] 后收尾，避免继续等 EOF；测试核对终止 usage 和提前 flush。这不代表上游推理时间缩短。
- 以上变更只在正式仓库完成，尚未部署到生产 312。
- `a829270a8`：断连后排水总预算取现有 StreamDataIntervalTimeout，未设时 180s；handler 返回时关闭 body、等待 scanner 退出。取消后 1.7s 才到达的终止 usage 测试通过，配置 1s 的挂起读取测试按期释放。没有改全局 detached 计费上下文。
- `227172cb1`、`0a2b9e8bd`：复用最终 SSE payload 已解析的 frame，避免输出/首字判定重复扫描。三判定基准 760--1186ns 降到 322--324ns，672B/6 次分配降到 112B/1 次分配；不能把局部基准幅度当成整体服务性能提升。
- 整合后的定向 race 通过。扩大 Grok race 时发现旧 quota 测试重置全局 singleflight 与上一测试后台任务竞态，正在修测试隔离。全后端 unit 仅三个 pg_dump 测试因 Windows PATH 缺 sh 失败；补上已安装 Git sh 后三个测试通过，未改生产 backup 逻辑。
- `2a4ccc780` 已修复 quota 测试隔离，扩大 Grok/stream/nonstream/flush 的 race 全部通过；`2149a3deb` 保留取消后 EOF、读错误、failed 的已收用量，避免错误返回为 failover 后被上层丢弃。`1a415607e` 将非流式协议违约转为请求内切换，账号暂封/禁用/限流 mock 调用均为0。

## 内存与生命周期审查

- 请求体 benchmark 显示 69MiB body 约 144,707,070 B/op、81 allocs/op；处理期间新旧 body 短暂并存，但完成后未证明仍被引用。因此不能把累计分配或 RSS 高峰直接叫泄露。
- 09:01 存活堆约 1.38GB，请求体约 791MB，占57%；09:12 RSS约3.2GB。早先1.7GB是较早采样，不能据此断言稳定或泄露已修好。必须结合在途请求与新版本长期回收走势继续确认。
- Responses SSE 的 detached upstream context 是计费/收尾设计，不能简单改成客户端取消即丢弃，否则会造成 usage 对账缺口。
- 正确修复要求：handler 返回必关 `resp.Body`；扫描器必须在超时或有限排水结束；客户端断开允许有限窗口收 terminal usage，超时后关闭 body；新增测试验证两条路径。

## 发布与回滚纪律

- 真实 303 基线为 `498f5333e32d12bc509770b38ae0d93f8b212d2c`；312 发布提交为 `4a3d70f34a00a35537efd0d0000aa2f9cb472fa3`。PR #46、#47 已合并，上轮 Linux CI、前端 2113 项测试及构建通过。
- 备份位于数据库主机 `/root/sub2api-release-backups/20260914T233715Z-before-0.1.312.dump`，约 22GB，TOC 可读；SHA256 为 `6b59da5569d3030e12dd228a73a2c16c9ee361e5c282b9d6a61d09cf21991665`。
- 生产数据库未改，迁移保持 `validate`；发布前备份已校验，禁止用伪造迁移记录修复 checksum。
- rollout 只改 nginx 权重并 graceful reload；303 旧进程不停止、不下线，已连接流量不掐断。上线新版本仍须先 99/1，再观察错误、可见首字、usage 和 pprof。
- 健康检查只证明进程可响应。必须同时核对真实模型出字、usage、状态码分布、上游归因和日志。
- systemd 中 `EnvironmentFile` 会覆盖前面的 `Environment=`；独立版本 override 必须最后加载，避免端口被覆盖导致 502/503。
- 312 guard 在切换阶段前拒绝外部配置变化；watch 仅在运行窗口内针对健康和旧 PID 异常回滚，两轮观察结束后并非永久后台守护。此前文档对此描述过强。

## 后续验收门槛

1. 合入生命周期补丁前，先通过断连 usage 排水、挂起 body 超时释放、race 测试。
2. 构建新版本并保留 312 与 303；新版本只接 1% 流量，连续观察公网健康、Grok 可见首字、非流式 JSON 和 5xx。
3. 指标无回归后才切全量；出现公网健康异常、5xx 激增、usage 不完整或内存持续单调增长，立即恢复 nginx 备份并保留旧进程。
