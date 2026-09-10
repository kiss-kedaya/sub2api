export default {
  cfAllowlist: {
    title: 'Access allowlist',
    intro: 'Every ¥100 of successful recharge unlocks one public IP. Cloudflare will skip challenges for that IP. Origin overload 502s are not fixed by this list.',
    recharged: 'Total recharged',
    needRecharge: 'Recharge to ¥{amount} to submit an IP',
    slots: '{used} / {max} slots used',
    detectedIP: 'Current IP',
    unknownIP: 'Unknown',
    ipLabel: 'IP to allow',
    submit: 'Submit to Cloudflare',
    submitting: 'Submitting...',
    submitOk: 'Added to allowlist',
    submitFailed: 'Submit failed',
    loadFailed: 'Failed to load',
    notConfigured: 'Cloudflare is not configured yet.',
    current: 'Bound IPs',
    empty: 'No IPs yet',
    removed: 'Removed'
  }
}
