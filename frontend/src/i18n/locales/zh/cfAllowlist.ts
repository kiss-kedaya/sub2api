export default {
  cfAllowlist: {
    title: '访问白名单',
    intro: '累计充值每满 100 元可绑定 1 个公网 IP。绑定后 Cloudflare 会跳过对该 IP 的质询和防火墙拦截。源站过载时仍可能 502，那是服务器忙，不是这个名单能解决的。',
    recharged: '累计充值',
    needRecharge: '再充值到 ¥{amount} 即可提交白名单 IP',
    slots: '已用 {used} / {max} 个名额',
    detectedIP: '当前访问 IP',
    unknownIP: '未能识别',
    ipLabel: '要放行的 IP',
    submit: '提交到 Cloudflare',
    submitting: '提交中...',
    submitOk: '已加入白名单',
    submitFailed: '提交失败',
    loadFailed: '加载失败',
    notConfigured: '管理员还没接好 Cloudflare 密钥，先联系站长。',
    current: '已绑定',
    empty: '还没有绑定 IP',
    removed: '已移除'
  }
}
