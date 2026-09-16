# Responses未完成断流诊断

时间：2026-09-16。用户询问反复出现的stream disconnected before completion: stream closed before response.completed。

## 生产证据

- 固定窗口北京时间10:06:40至10:26:40，共27条精确消息，全部账号28659（api.lukyface.com，openai/apikey）；模型gpt-5.6-sol 25条、gpt-6-astra 2条。26条客户端错误载荷含request_timeout。
- 19条被标internal/platform/gateway，8条标provider。单条response_latency_ms范围2218至900284，不能归纳为本机一个固定超时阈值，也不能直接视作全请求耗时。
- 记录75848444（10:23:16，Chat入口）上游详情明确保存结构化错误code=request_timeout，message就是完整报错文本。说明至少该真实样本的原始错误由上游返回，不是客户端凭空生成。
- 记录75865707（10:27:34，Responses入口）客户端载荷是event:error及code=request_timeout，未记录上游事件数组或详情，反而标platform。记录75861948最终是同样request_timeout，但上游事件只保留先前server_is_overloaded，存在最后错误归因缺口。
- 322当时双机全量、守护verified/armed，进程PID未变、无重启、无panic/OOM。此前稳定高缓gpt-5.5在321、322均复现上游长等待，不宜把所有断流都归咎版本。

## 代码对应

- openai_gateway_response_handling.go普通API-key分支不启用OAuthLike的bare-error失败终止补齐；原生和透传流在输出开始后仅response.failed分支调用recordOpenAIStreamUpstreamError，裸error缺少最后尝试的归因记录。
- ops_error_logger.go先classifyOpsErrorLog，后依据解析到的流错误填UpstreamStatusCode/UpstreamErrorMessage，不能回补已经算出的error_owner，因此部分真实上游SSE错误显示为平台错误。日志中的502还可能是流内错误的推断状态，不代表线上的HTTP头本来就是502。
- 本次是只读诊断及文档记录，没有改应用、重发用户原请求、修改计费、改超时或隐藏真实故障。

## 结论边界

- 这批样本集中于一条上游：流未正常完成，上游返回超时/断流错误，有样本先出现过载。不能仅凭错误字符串断言上游内部究竟是模型服务、代理链路还是其协议转换出错。
- 平台的日志归因缺口已证实；标准失败终止事件的API-key兼容路径还需独立回归验证后修复。补失败事件和归因不能让已中断的生成恢复，也不能伪造response.completed当成功。
- 本次没有宣称修复上游断流或平台归因代码。
