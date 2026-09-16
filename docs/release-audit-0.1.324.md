# 0.1.324 Grok 工具参数流式修复

日期：2026-09-16。

## 根因

- 线上 Grok custom tool 请求中，正文 984 字符正常分段；工具参数 5717 字符的 `response.function_call_arguments.delta` 被转换器全部写入缓存，直到 `done` 才一次性转换为客户端事件。
- 该请求总耗时约 76878ms，工具参数首个下游事件约 75581ms，直接造成用户看到首字时间接近总耗时。普通文本流不是同一问题。
- 真实上游直连同样只提供一个工具参数 delta，因此网关无法把上游没有提供的片段提前产生；网关能修的是协议转换层不能继续吞掉已经收到的 delta。

## 修复

- `ResponsesClientToolStreamRestorer` 增加对 `{"input":"..."}` 的增量解析，逐片生成 `response.custom_tool_call_input.delta`。
- 支持普通字符、JSON 转义、Unicode `\\uXXXX` 和跨片高低代理项；完成事件只发送尚未下发的尾部，避免重复。
- 新事件通过统一序列号分配，保留客户端 item ID 和既有工具协议；tool_search 仍保持原有对象参数协议。

## 验证

- apicompat/service 工具转换回归通过；覆盖普通文本、引号、反斜杠、中文、无身份字段、item ID 重写和完成事件。
- 真实 323 普通 Chat/Responses 流仍正常；工具请求确认了旧实现的缓存根因。
- 324 部署脚本使用 8092/pprof6071，323 保留在线；按 1% 灰度后全量，平台健康失败自动回退，上游业务错误不触发版本回退。
