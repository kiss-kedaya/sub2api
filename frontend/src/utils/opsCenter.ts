import type { AccountOpsEvent, AccountOpsThresholdAccount } from '@/api/admin/accountOps'

export interface BalanceTotal {
  unit: string
  total: number
  accounts: number
}

/**
 * Group upstream balances by currency unit. Accounts without a numeric
 * balance are skipped instead of being counted as zero.
 */
export function summarizeBalances(accounts: AccountOpsThresholdAccount[]): BalanceTotal[] {
  const totals = new Map<string, BalanceTotal>()
  for (const account of accounts) {
    if (typeof account.balance !== 'number' || !Number.isFinite(account.balance)) continue
    const unit = String(account.unit || '').trim().toUpperCase() || 'USD'
    const entry = totals.get(unit) ?? { unit, total: 0, accounts: 0 }
    entry.total += account.balance
    entry.accounts += 1
    totals.set(unit, entry)
  }
  return Array.from(totals.values()).sort((a, b) => b.total - a.total)
}

export type AttentionSeverity = 'critical' | 'warning'

export interface AttentionItem {
  key: string
  severity: AttentionSeverity
  title: string
  detail: string
  accountId: number
  accountName: string
  href: string
}

const EVENT_LABELS: Record<string, string> = {
  balance_low: '余额不足',
  weekly_quota: '周额度告急',
  balance_threshold: '余额低于阈值',
  quota_threshold: '额度低于阈值',
}

export function maxUsedPercent(account: AccountOpsThresholdAccount): number | null {
  let max: number | null = null
  for (const window of account.usage_windows ?? []) {
    if (typeof window.used_percent !== 'number' || !Number.isFinite(window.used_percent)) continue
    max = max === null ? window.used_percent : Math.max(max, window.used_percent)
  }
  return max
}

function eventValue(event: AccountOpsEvent): string {
  const details = event.details
  if (!details) return ''
  if (typeof details.balance === 'number') {
    return `${details.balance} ${details.unit ?? ''}`.trim()
  }
  if (typeof details.used_percent === 'number') {
    return `${details.used_percent}% · ${details.window ?? ''}`.trim()
  }
  return ''
}

function severityRank(severity: AttentionSeverity): number {
  return severity === 'critical' ? 0 : 1
}

/**
 * Build the "needs attention" queue from notification failures and account
 * balance/quota snapshots. One row per account and failure category so a
 * flapping alert does not flood the list.
 */
export function buildAttentionItems(
  accounts: AccountOpsThresholdAccount[],
  events: AccountOpsEvent[],
  limit = 8,
): AttentionItem[] {
  const items: AttentionItem[] = []
  const seen = new Set<string>()
  const push = (item: AttentionItem) => {
    if (seen.has(item.key)) return
    seen.add(item.key)
    items.push(item)
  }

  for (const event of events) {
    if (!['pending', 'sending', 'failed'].includes(event.state)) continue
    push({
      key: `event:${event.id ?? `${event.account_id}:${event.kind}:${event.first_seen}`}`,
      severity: event.state === 'failed' ? 'critical' : 'warning',
      title: EVENT_LABELS[event.kind] ?? event.kind,
      detail: [event.group_name || event.account_name, eventValue(event)].filter(Boolean).join(' · '),
      accountId: event.account_id,
      accountName: event.account_name,
      href: '/admin/account-ops',
    })
  }

  for (const account of accounts) {
    const base = {
      accountId: account.account_id,
      accountName: account.account_name,
      href: '/admin/account-ops',
    }
    if (account.balance_status === 'failed') {
      push({
        ...base,
        key: `balance-failed:${account.account_id}`,
        severity: 'critical',
        title: '余额读取失败',
        detail: `${account.account_name} · ${account.platform}`,
      })
    } else if (account.balance_status === 'stale') {
      push({
        ...base,
        key: `balance-stale:${account.account_id}`,
        severity: 'warning',
        title: '余额数据过期',
        detail: `${account.account_name} · ${account.platform}`,
      })
    }
    const used = maxUsedPercent(account)
    if (used !== null && used >= 90) {
      push({
        ...base,
        key: `quota:${account.account_id}`,
        severity: 'warning',
        title: '额度即将用尽',
        detail: `${account.account_name} · 已用 ${Math.round(used)}%`,
      })
    }
  }

  return items
    .sort((a, b) => severityRank(a.severity) - severityRank(b.severity))
    .slice(0, Math.max(0, limit))
}
