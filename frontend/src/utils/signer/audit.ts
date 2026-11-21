/** Audit summary shown after an unlocked sweep, built from the plan + run. */

export interface SweepAuditInfo {
  addresses: number
  planned: number
  swept: number
  failed: number
  amountUsd: number | null
  txHashes: string[]
  status: string
  dryRun: boolean
}

export interface SweepAuditPlanLike {
  TotalUSD?: number
  Entries?: Array<{ Address?: string }> | null
}

export interface SweepAuditEntryLike {
  sweep_tx_hash?: string
  gas_tx_hash?: string
}

export interface SweepAuditSummaryLike {
  planned?: number
  swept?: number
  failed?: number
  dry_run?: boolean
  entries?: SweepAuditEntryLike[] | null
}

export function buildSweepAuditInfo(input: {
  plan?: SweepAuditPlanLike | null
  summary?: SweepAuditSummaryLike | null
  runError?: string
  finished?: boolean
}): SweepAuditInfo {
  const summary = input.summary ?? null
  const entries = summary?.entries ?? []
  const txHashes: string[] = []
  for (const entry of entries) {
    for (const hash of [entry.sweep_tx_hash, entry.gas_tx_hash]) {
      if (hash && !txHashes.includes(hash)) txHashes.push(hash)
    }
  }
  const planned = summary?.planned ?? input.plan?.Entries?.length ?? 0
  const swept = summary?.swept ?? 0
  const failed = summary?.failed ?? 0
  const amountUsd = typeof input.plan?.TotalUSD === 'number' ? input.plan.TotalUSD : null
  const status = input.runError
    ? 'error'
    : input.finished === false
      ? 'running'
      : failed > 0
        ? 'partial'
        : 'completed'
  return {
    addresses: planned,
    planned,
    swept,
    failed,
    amountUsd,
    txHashes,
    status,
    dryRun: summary?.dry_run === true
  }
}
