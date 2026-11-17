import { computed, onUnmounted, ref, type InjectionKey } from 'vue'
import { useI18n } from 'vue-i18n'
import { cancelScanRun, listScanRuns, startCryptoScan, waitForScanRun, type CryptoVerification, type ScanRun } from '@/api/admin/crypto'
import { extractApiErrorMessage } from '@/utils/apiError'

export const cryptoScanKey: InjectionKey<ReturnType<typeof useCryptoScan>> = Symbol('cryptoScan')

// One controller serves both entry points, including the existing payment-proof
// path for cancellation. Leaving the view stops polling, never the server run.
export function useCryptoScan(
  verify: (label: string, callback: (proof: CryptoVerification) => Promise<unknown>) => Promise<boolean>,
  refresh: (completed: boolean) => Promise<void>,
  appStore: { showSuccess(message: string): void; showError(message: string): void }
) {
  const { t } = useI18n()
  const scanning = ref(false)
  const activeScan = ref<ScanRun | null>(null)
  const cancelling = ref(false)
  let poll: AbortController | null = null
  let discovering = false
  let disposed = false
  let needsDiscovery = false
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let retryAttempt = 0
  // Retry reads only, with a bounded budget per outage. Never create a run here.
  const retryDelays = [1000, 2000, 4000]
  onUnmounted(() => {
    disposed = true
    poll?.abort()
    clearTimeout(retryTimer)
  })

  function retry() {
    if (disposed || retryTimer !== undefined || retryAttempt >= retryDelays.length) return
    retryTimer = setTimeout(() => {
      retryTimer = undefined
      void resume(true)
    }, retryDelays[retryAttempt++]!)
  }

  function stopTitle(run: ScanRun | null): string {
    if (run?.status === 'cancelled') return t('admin.crypto.scanRunCancelled')
    if (run?.stop_reason === 'stalled') return t('admin.crypto.scanRunStalled')
    if (run?.stop_reason === 'deadline') return t('admin.crypto.scanRunDeadline')
    return t('admin.crypto.scanDepositsFailed')
  }

  async function findRunning(): Promise<ScanRun | null> {
    return (await listScanRuns()).find(run => run.status === 'running') ?? null
  }

  async function follow(run: ScanRun) {
    if (disposed) return
    poll?.abort()
    const controller = new AbortController()
    poll = controller
    scanning.value = true
    activeScan.value = run
    let missing = false
    try {
      const result = await waitForScanRun(run, controller.signal, next => {
        if (disposed || controller.signal.aborted) return
        activeScan.value = next
        if (next !== run) retryAttempt = 0
      })
      if (disposed || controller.signal.aborted) return
      if (result.status === 'completed') activeScan.value = null
      // A stopped scan can still have recorded deposits.
      await refresh(result.status === 'completed')
      if (disposed) return
      if (result.status === 'completed') appStore.showSuccess(t('admin.crypto.scanDepositsDone'))
      else if (result.status === 'failed') appStore.showError(stopTitle(result))
    } catch (error) {
      if (disposed || controller.signal.aborted) return
      if (activeScan.value?.status === 'running' && (error as { status?: number } | null)?.status === 404) {
        needsDiscovery = true
        missing = true
      } else {
        appStore.showError(extractApiErrorMessage(error, t('admin.crypto.scanDepositsFailed')))
        if (activeScan.value?.status === 'running') retry()
      }
    } finally {
      if (poll === controller) { poll = null; scanning.value = false }
      if (missing) void resume(true)
    }
  }

  async function start(chains?: string[]) {
    if (scanning.value || discovering || activeScan.value?.status === 'running' || disposed) return
    clearTimeout(retryTimer)
    retryTimer = undefined
    retryAttempt = 0
    needsDiscovery = false
    scanning.value = true
    activeScan.value = null
    try {
      const run = await startCryptoScan(0, undefined, chains)
      if (disposed) return
      if (run) await follow(run)
      else {
        await refresh(true)
        appStore.showSuccess(t('admin.crypto.scanDepositsDone'))
      }
    } catch (error) {
      // A 409 or lost start response attaches to the existing run.
      let running: ScanRun | null = null
      try { running = await findRunning() } catch { needsDiscovery = true; retry() }
      if (disposed) return
      if (running) await follow(running)
      else appStore.showError(extractApiErrorMessage(error, t('admin.crypto.scanDepositsFailed')))
    } finally {
      if (!poll) scanning.value = false
    }
  }

  async function resume(retrying = false) {
    if (poll || scanning.value || discovering || retryTimer !== undefined || disposed) return
    if (!retrying) retryAttempt = 0
    if (activeScan.value?.status === 'running' && !needsDiscovery) {
      void follow(activeScan.value) // It may have finished while the last poll was lost.
      return
    }
    discovering = true
    try {
      const running = await findRunning()
      if (poll || scanning.value || disposed) return
      needsDiscovery = false
      if (running) void follow(running)
      else if (activeScan.value?.status === 'running') activeScan.value = null
    } catch {
      // A failed list read cannot prove that a vanished run is no longer active.
      retry()
    } finally {
      discovering = false
    }
  }

  async function cancel() {
    const run = activeScan.value
    if (!run || run.status !== 'running' || cancelling.value) return
    cancelling.value = true
    try {
      await verify(t('admin.crypto.scanRunCancelVerify'), async proof => { await cancelScanRun(run.id, proof) })
    } finally {
      cancelling.value = false
    }
  }

  // A lost progress response is not evidence that the server released the run.
  const running = computed(() => activeScan.value?.status === 'running' || (scanning.value && !activeScan.value))
  const alertTitle = computed(() => running.value ? t('admin.crypto.scanDepositsRunning') : stopTitle(activeScan.value))
  const alertColor = computed(() => running.value ? 'info' : activeScan.value?.status === 'cancelled' ? 'warning' : 'error')
  const scopeLine = computed(() => {
    const scope = activeScan.value?.chain_scope
    if (!scope) return '' // Older servers do not report a scope.
    return scope.length ? t('admin.crypto.scanRunScope', { chains: scope.join(', ') }) : t('admin.crypto.scanRunScopeAll')
  })
  const progressLine = computed(() => {
    const run = activeScan.value
    if (!run || (run.rounds === 0 && run.busy_retries === 0)) return ''
    const parts = [t('admin.crypto.scanRunRounds', { count: run.rounds })]
    if (run.busy_retries > 0) parts.push(t('admin.crypto.scanRunWaited', { count: run.busy_retries }))
    return parts.join(' · ')
  })
  const chainLines = computed(() => (activeScan.value?.chains ?? []).map(chain => chain.head_known
    ? t('admin.crypto.scanRunChain', { chain: chain.chain, cursor: chain.cursor, head: chain.confirmed_head, remaining: chain.remaining })
    : t('admin.crypto.scanRunChainPending', { chain: chain.chain, cursor: chain.cursor })))

  return { scanning, activeScan, cancelling, start, resume, cancel, running, alertTitle, alertColor, scopeLine, progressLine, chainLines }
}
