const zh = {
  title: '地区网关路由', description: '按可信客户端 IP 的国家地区，将文本请求发送到已批准网关，继续使用现有鉴权和计费。默认关闭。',
  refresh: '刷新', save: '保存路由', enabled: '启用新请求地区分配', saved: '配置已保存', failed: '地区路由操作失败',
  setup: '节点密钥与可信私网在服务器环境变量配置。页面不接收或展示集群密钥。新网关必须连接同一在线数据库与 Redis。',
  secret: '集群密钥已配置', redis: 'Redis 已连接', private: '可信私网已配置', yes: '是', no: '否',
  nodes: '批准网关', add: '添加', remove: '移除', nodeId: '网关 ID', endpoint: '网关地址', available: '允许分配新请求',
  regions: '国家地区', country: '国家代码', countriesHint: '使用两位大写国家代码，例如 US。网关 ID 用英文逗号分隔。地区仅由服务器查询，不采信浏览器传入地区。',
  ids: '网关 ID 列表', heartbeat: '在线网关', ready: '就绪', version: '版本', empty: '暂无在线网关',
  continuity: '已绑定的 Key 保持原网关，停用新分配不会迁走进行中的对话。绑定网关不可用时明确返回错误，不重放请求。',
  advanced: '今日路由统计', requests: '请求', errors: '错误'
}
const en = {
  title: 'Regional gateway routing', description: 'Route text requests by the trusted client IP country to approved gateways using existing authentication and billing. Disabled by default.',
  refresh: 'Refresh', save: 'Save routing', enabled: 'Enable assignment of new requests', saved: 'Routing configuration saved', failed: 'Regional routing operation failed',
  setup: 'Configure cluster credentials and trusted private networks in server environment variables. This page never accepts or displays a cluster secret. Nodes must use the same online database and Redis.',
  secret: 'Cluster secret configured', redis: 'Redis connected', private: 'Trusted private networks configured', yes: 'Yes', no: 'No',
  nodes: 'Approved gateways', add: 'Add', remove: 'Remove', nodeId: 'Gateway ID', endpoint: 'Gateway origin', available: 'Allow new assignments',
  regions: 'Country regions', country: 'Country code', countriesHint: 'Use two-letter uppercase country codes such as US. Separate gateway IDs with commas. Countries are resolved by the server from trusted IPs.',
  ids: 'Gateway ID list', heartbeat: 'Registered gateways', ready: 'Ready', version: 'Version', empty: 'No registered gateway',
  continuity: 'An assigned key stays on its original gateway. Disabling new assignments preserves existing conversations. An unavailable bound gateway returns an explicit error without replaying the request.',
  advanced: 'Routing totals today', requests: 'Requests', errors: 'Errors'
}
export const serverlessMessages = { zh: { serverless: zh }, en: { serverless: en } }
