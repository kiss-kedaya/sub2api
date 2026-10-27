// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
export default {
  identityFailed: '用户候选加载失败，请重新搜索。',
  loadingRecords: '正在加载请求明细…', recordsFailed: '请求明细加载失败，请重试。',
  queueSummary: '内存排队 {queued}/{capacity} 条 · 处理中 {processing} 条 · 本次启动后丢弃 {dropped} 条',
  storageUnavailable: '流量统计存储暂不可用，主业务继续运行；统计可能缺失。',
  trendScopes: { hour: '每柱为该小时上传与下载流量之和，各小时独立统计，不逐时累计。', day: '每柱为该天上传与下载流量之和，各天独立统计，不逐日累计。', month: '每柱为该月上传与下载流量之和，各月独立统计，不逐月累计。' },
  deletedKey: '已删除', keyHistoryHint: '支持按名称或 #ID 搜索，包含已删除密钥。', keysFailed: '密钥候选加载失败，请重新搜索。',
  nodeHealth: '本节点流量记录状态', nodeHealthScope: '仅反映当前节点，计数器在进程重启后归零', healthStatuses: { ok: '正常', delayed: '存在积压', degraded: '需要处理' },
  healthSummary: '待入库 {pending} 条 · 最老积压 {age} 秒 · 隔离 {quarantined} 条 · 本次启动后持久化失败告警 {lost} 次', healthFailed: '暂时无法获取记录状态，请刷新重试。',
  title: '流量记录', scope: '统计已认证 API 请求的 HTTP/SSE 正文读写量，包含失败与取消请求。暂不覆盖 WebSocket、Live/WebRTC、无 API Key 的能力链接下载和外部媒体下载。不含请求头及网络协议开销；响应写入量不代表客户端已完整接收。本期仅统计，不扣费。异步尽力记录，过载、异常退出或存储故障时可能缺失。',
  period: '统计周期', periods: { day: '日', week: '周', month: '月', year: '年' }, date: '周期内日期', apply: '查询',
  previousPeriod: '上一周期', nextPeriod: '下一周期', upload: '上传流量', download: '下载流量', total: '总流量', requests: '请求数',
  since: '当前筛选下首条流量记录：{time}。', notRecorded: '当前筛选下尚无流量记录。', history: '历史未采集流量无法补全。单次请求按开始时间归属，结束后入账，异步统计可能延迟，过载或故障时可能缺失。',
  trend: '流量趋势', trendDetails: '查看趋势明细', bucket: '时间', records: '请求明细', user: '用户', endpoint: '端点 / 请求 ID', result: '结果', partialRead: '请求体未完整读取',
  outcomes: { completed: 'HTTP 处理完成', http_error: 'HTTP 错误', canceled: '请求取消', write_error: '响应写入失败', panic: '处理异常' },
  empty: '所选周期暂无已记录流量', loading: '正在加载流量记录…', failed: '加载流量记录失败，请重试', page: '第 {page} 页', previous: '上一页', next: '下一页'
}
