# 316 流式与首字审查

基线：`e0cbce18e4f76b5b2228bb5ebaf7887d1e9f2beb`。仅在
`F:/GO/sub2-review-316-stream-timing` 修改和本地测试。没有连接生产、读取凭据、
修改正式工作树、数据库或配置，也没有 push。生产验证由主线负责。

## 已证实问题与最小修复

| 触发 | 修前 | 修后 |
| --- | --- | --- |
| OpenAI Responses 启用首输出超时保护；第 2 秒完整正文事件触发第一次 flush，下游 flush 阻塞至第 9 秒 | `completeGuardedEvent` 在 flush 返回后计时，首字 9000ms | 完整事件到达时先记录，首字 2000ms；没有改变实际发送、事件边界或超时保护 |
| OpenAI Responses 转 Chat/Messages：第 0 秒 created，第 1 秒空 reasoning item，第 2 秒正文 | 两个入口只为 Grok 使用 TTFT 分类，OpenAI 把 created 记成 0ms | OpenAI 复用现有分类；semantic=1000ms，visible=2000ms；不改变两种分类的定义 |
| 上游只有 completed/done/incomplete 的 response.output 含正文，此前没有正文 delta | Chat/Messages 只有结束事件，正文丢失 | 用既有文本事件发送逻辑在结束前补发正文；已有正文 delta 的流不重复补发 |

第三项只补正文，不重建终止快照中的工具、reasoning，不补已有部分正文的尾差。
这些情况未经本轮专项证明，不扩大修复。首项只有首个被计时事件触发阻塞写出时
才会吞入下游等待；此前已计时的 reasoning/item 事件不受影响。

## 确定性证据

使用 Go `testing/synctest` 虚拟时间与 `io.Pipe`，在终止事件尚未发送时检查实际
ResponseWriter flush 内容。无公网、数据库和真实等待时间阈值依赖。

- 修前新增服务测试出现 18 个失败场景：OpenAI 两转换计时 12 个、阻塞 flush 计时 2 个、终止正文丢失 4 个。
- 修前 apicompat 终止正文测试 9/12 场景失败，已有正文 delta 的 3 个对照通过。
- 修后服务新增 69 个场景通过；apicompat 新增 12 个场景通过。
- 第 2 秒和第 3 秒分别下发 `hel`、`lo`，第 9 秒才发终止：OpenAI/Grok 三条路径均在终止前 flush 两段。
- Chat 请求长度参数覆盖 0、65536、131072；64KiB 静默拒绝保护遇到正文即放行。
- OpenAI 70KiB preamble 在首正文前保持私有，首正文第 2 秒到达即 flush；64KiB 暂存阈值不是等终止的门槛。Windows 此用例走现有内存暂存分支；Linux spool 的 I/O 性能未测。
- 单条 data 行第 2 秒到达、空行第 9 秒到达：没有提前 flush 未完成 SSE 事件。OpenAI guarded 与转换器按完整边界记 9 秒；Grok 原生保留按 data 行记 2 秒的既有行为。
- `event: response.output_text.delta` + 无 JSON type 的 data 正常出字；终止别名 `response.done` 正常完成。
- 仅终止带正文：第 9 秒才有正文，三路径均记 9000ms。没有回填、推测或伪造更早首字。
- 仅 usage 的终止即使 output_tokens=400，semantic 仍是第 9 秒，visible 仍为空；token 数不被当作正文证据。
- 输入、输出、缓存读取 usage 的回归断言保持既有值；正常流正文不重复。

无空行的连续 JSON data、任意 `events` 嵌套或未知 vendor 类型，没有生产原始帧
证据证明其格式或发生率。现有转换器按完整 SSE 帧和顶层 ResponsesStreamEvent
解码，不根据猜测新增展开规则。现有同一 data 内多个完整 JSON 的修复回归已通过。
Grok ping filter 仅暂存 ping 候选帧，正常帧逐行透传；过滤器既有回归通过，本轮未改。

## 验证记录

工作目录均为本工作树的 `backend`。以下命令均退出 0：

```text
go test ./internal/pkg/apicompat -count=1
go test -tags unit ./internal/service -run '^TestStreamTimingReview_' -count=1
go test -tags unit ./internal/service -run '^(TestStreamTimingReview_|TestOpenAIResponseFlush_|TestOpenAINativeFirstOutput|TestOpenAIResponsesTTFT|TestOpenAIVisibleOutputClassification|TestGrokResponsesBillingPingFilter|TestGrokResponsesFlushesVisible|TestGrokChatResponsesBridge|TestForwardAsChatCompletions_|TestHandleChatStreamingResponse_|TestForwardAsMessages_|TestHandleAnthropicStreamingResponse_|TestOpenAIChatSilentRefusal|TestOpenAIStreamingRepairsConcatenated|TestOpenAIStreamingAsyncScannerRepairs)' -count=1
go test -tags unit ./internal/service -run '^(TestForwardAsAnthropic_|TestHandleAnthropicStreamingResponse|TestOpenAISilentRefusal|TestOpenAIChatSilentRefusal)' -count=1
git diff --check
```

apicompat 全包通过，相关服务测试两组分别为 16.973s、3.058s。
未跑后端全量、race 或生产请求；这些本地结果不等于线上根因确证。

## 变更路径与合入风险

- `backend/internal/service/openai_gateway_response_handling.go`：完整事件计时移到下游 flush 前。
- `backend/internal/service/openai_gateway_chat_completions.go`：OpenAI 转 Chat 的 TTFT 分类。
- `backend/internal/service/openai_gateway_messages.go`：OpenAI 转 Messages 的 TTFT 分类。
- `backend/internal/pkg/apicompat/responses_to_chatcompletions.go`：无正文 delta 时读取终止正文并发送。
- `backend/internal/pkg/apicompat/responses_to_anthropic.go`：同上，复用文本 block 生命周期。
- 两个新增测试文件：`openai_gateway_stream_timing_review_test.go`、`responses_terminal_text_test.go`。

实现差异 50 行新增、8 行删除。没有改 usage 解析、缓存语义、计费、选号、配置或
Grok 过滤规则。OpenAI 转换的 first_token_ms 会与旧版 first-data 口径不同；
不能把指标变化直接解释为模型提速。补发终止正文只覆盖此前没有正文 delta 的流。

主线报告 316 长流及连续健康正常，与本地正常流回归一致。本轮未证明少量 Grok
首总差小于 20ms 的记录由网关缓冲导致，也未验证 cache192/模型名与上游 fallback
的关联。不能把本提交描述成这些生产样本的已确认根因。合入并重启新版本后才生效，
不回写历史记录。
