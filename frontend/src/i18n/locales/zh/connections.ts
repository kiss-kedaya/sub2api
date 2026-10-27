export default {
  recognizedCredential: "已识别 key；认证被拒绝",
  errorDetail: '查看结构化错误详情', logicalError: '记录的业务错误',
  title: '连接诊断',
  scope: '查询入口代理与应用记录。HTTP 200、写出或 Flush 成功不代表客户端已收到或解析完成；“重连”是客户端行为，不能仅凭服务端日志确认。DNS、TCP 或 TLS 阶段完全未到入口的失败可能没有记录，需结合客户端日志。',
  identityScope: '入口记录可能没有用户身份。排查网络问题时，可清除用户、密钥、模型、账号和分组筛选，再按域名、时间、IP、请求 ID 查找。URL 不包含查询参数或敏感路径令牌。',
  state: '观察结果', source: '记录来源', host: '入口域名（含端口）', requestId: '请求 ID / 客户端 ID / Trace ID',
  details: '连接详情', legacy: '历史记录（字段可能缺失）', unidentified: '未识别用户', bytes: '写出 {count} 字节',
  empty: '没有匹配的已保留记录。这不代表客户端连接正常；历史未采集、未到入口、未覆盖协议、保留期结束或采集失败均可能没有记录。',
  loadError: '连接记录加载失败，请检查时间范围（最多 31 天）并重试。',
  noTimeline: '该来源未采集应用时间线。', page: '第 {page} 页', previous: '上一页', next: '下一页',
  upstreamStatus: '入口观察的后端状态', networkError: '入口网络错误类型',
  sources: { all: '全部来源', edge: '入口代理', application: '应用', nginx: 'Nginx 入口', caddy: 'Caddy 入口' },
  states: {
    all: '全部结果', abnormal: '异常结果', completed: '应用侧完成', edge_completed: '入口响应结束', edge_failed: '入口异常',
    client_disconnected: '下游连接断开', downstream_write_failed: '下游写出失败', failed: '请求失败', incomplete: '流未完成', canceled: '请求被取消', unknown: '结果未确认'
  }
}
