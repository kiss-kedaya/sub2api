# 0.1.322 协议转换后的最小输入策略分类

日期：2026-09-16。基于已全量的321，补齐一个已由生产记录证实的分类缺口。

## 问题与修复

- Chat/Messages转换后的客户端错误仅保留type/message，丢失error.code。321仅读取客户端code，导致同一个input_too_small在Responses中被排除健康计错，在Chat/Messages中仍被计错。
- 生产记录75467690等：客户端400，上游400；客户端无code，原始上游detail精确包含error.code=input_too_small。
- 补丁仅在400/400且客户端code为空时，解析已登记的最后一次上游JSON详情；精确命中才复用现有context_limit分类。
- 不按消息模糊匹配，不扫描此前失败重试；其他400、502、格式错误详情和明确不同客户端code不被隐藏。原始客户端响应与上游诊断保留。
- 不修改流式转换、缓存、账单、余额、数据库结构、服务器资源上限。

## 验证

- 新回归先失败：Chat、Messages预期context_limit，实际invalid_request；修复后通过。
- handler/service/repository定向回归通过。额外覆盖无详情、错误JSON、其他上游code、消息冒充code、上游502、缺上游状态、不同最终客户端code及先前尝试污染。
- 发布及双机灰度证据完成后追加。
