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
  onUnmounted(() => { disposed = true; poll?.abort() })

  function stopTitle(run: ScanRun | null): string {
    if (run?.status === 'cancelled') return t('admin.crypto.scanRunCancelled')
    if (run?.stop_reason === 'stalled') return t('admin.crypto.scanRunStalled')
    if (run?.stop_reason === 'deadline') return t('admin.crypto.scanRunDeadline')
    return t('admin.crypto.scanDepositsFailed')
  }

  async function findRunning(): Promise<ScanRun | null> {
    try {
      return (await listScanRuns()).find(run => run.status === 'running') ?? null
    } catch {
      return null
    }
  }

  async function follow(run: ScanRun) {
    if (disposed) return
    poll?.abort()
    const controller = new AbortController()
    poll = controller
    scanning.value = true
    activeScan.value = run
    try {
      const result = await waitForScanRun(run, controller.signal, next => { activeScan.value = next })
      if (result.status === 'completed') activeScan.value = null
      // A stopped scan can still have recorded deposits.
      await refresh(result.status === 'completed')
      if (result.status === 'completed') appStore.showSuccess(t('admin.crypto.scanDepositsDone'))
      else if (result.status === 'failed') appStore.showError(stopTitle(result))
    } catch (error) {
      if (!controller.signal.aborted) appStore.showError(extractApiErrorMessage(error, t('admin.crypto.scanDepositsFailed')))
    } finally {
      if (poll === controller) { poll = null; scanning.value = false }
    }
  }

  async function start(chains?: string[]) {
    if (scanning.value || activeScan.value?.status === 'running' || disposed) return
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
      const running = await findRunning()
      if (disposed) return
      if (running) await follow(running)
      else appStore.showError(extractApiErrorMessage(error, t('admin.crypto.scanDepositsFailed')))
    } finally {
      if (!poll) scanning.value = false
    }
  }

  async function resume() {
    if (poll || scanning.value || discovering || disposed) return
    if (activeScan.value?.status === 'running') {
      void follow(activeScan.value) // It may have finished while the last poll was lost.
      return
    }
    discovering = true
    try {
      const running = await findRunning()
      if (running && !poll && !scanning.value && !disposed) void follow(running)
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
