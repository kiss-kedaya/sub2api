# Responses / Chat 工具调用排查（2026-09-20）

## 结论与证据

- 正式目录：`F:/GO/sub2-official-work`，基线 `0439faba4`。检查期间生产仍为 0.1.334；本记录中的代码修复尚未发布。
- 指定用户同一把 Key 在上海时间 16:00 前后为 Responses 入站，16:02 后为 Chat 入站，上游均为 Responses。实际入站在中间件读取 URL 时记录；界面直接展示两个字段，没有交换标签。
- 用户网关显示 Responses 入站、Chat 出站，本站显示 Chat 入站、Responses 出站，两段记录可以同时正确。不能据此交换标签或全局修改账号协议。
- 管理员自有测试 Key 经 `https://kedaya.ai` 分别请求 Responses / Chat，均返回 200 和 `exec_command` 工具调用，耗时约 3.65 / 2.99 秒。未使用反馈用户的余额，也未执行生成的命令。
- 公网 Responses 请求 ID：`c1bc2455-b1d5-49db-841e-20e03b2d2e22`；Chat 请求 ID：`2d36d49d-cbba-496a-bc25-66daf8fe9db9`。这是旧版正常完整增量流的对照，不能冒充新修复线上验证。
- 该用户另有 `gpt-5.5` 不受当前分组账号支持的 404；与工具转换缺陷分开，不擅自增加模型映射或更改计费模型。

## 已复现并修复

1. Chat 指定函数的 `tool_choice.function.name` 原样传给 Responses，没有展平为 `tool_choice.name`。修复命名函数和 `allowed_tools` 选择策略，保留 auto / none / required 与已有扁平格式。
2. Chat 的 developer 消息进入默认分支变成 user。修复后保留 developer 指令角色。
3. Responses 流式工具调用只识别 added / arguments delta，忽略 output_item.done 与终态 output 中完整的工具快照。结果为 200 + stop，但缺少工具调用或完整参数。新增快照恢复，补齐缺失参数后输出 tool_calls，按已发送增量去重，并拒绝重复终态重放。
4. 缓冲非流式路径同样接收 output_item.done，终态 output 为空时仍保留工具调用。工具 ID 和结果关联在第二轮保持一致。
5. 终态省略 reasoning 项导致 output 索引变化时，按 call_id 对齐已存在的工具，避免重复发送。

没有该用户失败请求的原始上游事件，不能断言第 3 项就是其唯一根因。本站无法还原在更早一层网关已被丢弃的工具定义、命名空间或工具结果。原生 Responses 对接可以减少这层损耗。

## 验证

- 新增失败测试先在旧代码打红：命名工具、allowed_tools、developer 角色、仅 item.done、仅 completed.output、不完整 delta、重复终态和缓冲快照。
- apicompat 全包与 race 检查通过。
- 真实网关服务 + 模拟上游 SSE 的双轮测试通过：Chat 请求转 Responses，终态工具转 Chat，再由外层桥转 Responses；exec_command 的名称、call_id、JSON 参数和下一轮工具结果均保留。
- ChatCompletions、Responses/Chat、Codex transform、原生 Anthropic 相关 service 定向测试通过。
- handler 端点回归通过；未修改日志端点标签、服务器配置或生产数据库。

## 后续上线边界

代码修复位于正式工作树，未提交、打版、上线。此前前端和充值页工作也在同一工作树；发布时必须明确纳入范围，不能把本地预览当成线上已生效。继续沿用两机金丝雀、旧实例保留及平台错误回退策略。

## 官方对照复核

2026-09-20 通过 GitHub API 和 git fetch 检查 Wei-Shaw/sub2api。最新 Release 为 v0.2.7（2026-09-19 发布），本次固定审查 main 提交 `d2e319b2a17006122cd2d53d828c44a0bf21bd9b`（上海时间 2026-09-20 16:37）。没有合并上游主分支或覆盖工作树。

- 官方 main 的 `chatcompletions_to_responses.go` 与本地修复前 HEAD blob 完全相同：`f72b5faf6d4c5584e276637366d128376f7d9c87`。指定工具格式和 developer 降级来自现有上游实现。
- [PR #7267](https://github.com/Wei-Shaw/sub2api/pull/7267) 正在修复相同的指定函数工具选择格式，截至检查仍 OPEN、未合并。可见检查只有 CLA 成功及 CLA lock 跳过，不能称为完整测试 CI 全绿。
- 官方 main 仍忽略 output_item.done 中的工具快照，completed.output 也不恢复工具调用。在独立源码快照上，通过 Go overlay 注入本地相同回归，命名工具/allowed_tools、developer、终态工具快照及缓冲快照全部复现失败。这些为预期失败的验证，没有修改官方源码。
- 官方 [PR #7345](https://github.com/Wei-Shaw/sub2api/pull/7345) 已合并，修复 Responses 转 Anthropic 工具顶层联合 schema；[PR #7313](https://github.com/Wei-Shaw/sub2api/pull/7313) 已合并，修复 DeepSeek Chat 回退的 reasoning_content。当前二开没有对应完整实现，但它们不能直接解释指定用户 OpenAI Chat 到 Responses 这条链路，暂未引入本轮修复。
- 官方 [PR #6925](https://github.com/Wei-Shaw/sub2api/pull/6925) 提醒终态数组可能重排。追加复现后发现本轮初稿虽能按 call_id 对齐旧调用，但新调用仍可能与旧的 streamed output_index 撞位，导致第二个终端工具丢失。已使用独立的内部终态索引，并保持对客户端输出的工具索引连续；新增混合新旧工具回归通过。
- 最终 apicompat 全包 race 与相关 service 回归通过。生产仍未更新，官方检查也未改动服务器配置或数据库。
