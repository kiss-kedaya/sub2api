<template>
  <BaseDialog :show="showResult" :title="t('admin.crypto.privateKeyExportTitle')" @close="close">
    <div class="crypto-private-key">
      <div class="crypto-private-key__field">
        <span class="crypto-private-key__label">{{ t('admin.crypto.address') }}</span>
        <code data-testid="private-key-address">{{ displayedAddress?.address }}</code>
      </div>
      <template v-if="privateKey">
        <div class="crypto-private-key__field">
          <span id="crypto-private-key-label" class="crypto-private-key__label">{{ t('admin.crypto.privateKey') }}</span>
          <pre id="crypto-private-key-value" ref="privateKeyElement" data-testid="private-key-value" aria-labelledby="crypto-private-key-label">{{ visible ? privateKey : '••••••••••••••••••••••••••••••••' }}</pre>
        </div>
        <div class="crypto-private-key__actions">
          <button type="button" class="btn btn-secondary btn-sm" :aria-pressed="visible" aria-controls="crypto-private-key-value" @click="visible = !visible">
            {{ t(visible ? 'admin.crypto.privateKeyHide' : 'admin.crypto.privateKeyShow') }}
          </button>
          <button type="button" class="btn btn-secondary btn-sm" data-testid="private-key-copy" @click="copyPrivateKey">
            {{ t('admin.crypto.privateKeyCopy') }}
          </button>
        </div>
        <p role="timer" class="crypto-private-key__hint">{{ t('admin.crypto.privateKeyAutoClear', { seconds: remainingSeconds }) }}</p>
      </template>
      <p v-else role="status">{{ t('admin.crypto.privateKeyExpired') }}</p>
      <p v-if="displayedAddress?.family === 'evm'" class="crypto-private-key__hint">{{ t('admin.crypto.privateKeyEvmShared') }}</p>
      <p class="crypto-private-key__hint">{{ t('admin.crypto.privateKeyExternalTransferHint') }}</p>
      <p v-if="copyNotice" role="status">{{ copyNotice }}</p>
    </div>
    <template #footer>
      <button type="button" class="btn btn-primary" data-testid="private-key-close" @click="close">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>
  <CryptoSecurityDialog ref="securityDialog" />
</template>

<script setup lang="ts">
import { inject, onBeforeUnmount, onDeactivated, ref, watch } from 'vue'
import { matchedRouteKey, onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CryptoSecurityDialog from './CryptoSecurityDialog.vue'
import { exportAddressPrivateKey, reportWalletOperationIntent, type CryptoPrivateKeyExport, type DepositAddress } from '@/api/admin/crypto'
import { isCryptoGrantRejected } from '@/composables/cryptoGrant'
import { useAppStore } from '@/stores/app'

type ExportAddress = Pick<DepositAddress, 'id' | 'address' | 'family'>

const props = defineProps<{ address: ExportAddress | null }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const securityDialog = ref<InstanceType<typeof CryptoSecurityDialog> | null>(null)
const showResult = ref(false)
const displayedAddress = ref<ExportAddress | null>(null)
const privateKey = ref('')
const privateKeyElement = ref<HTMLElement | null>(null)
const visible = ref(false)
const copyNotice = ref('')
const remainingSeconds = ref(0)
let generation = 0
let displayTimer: ReturnType<typeof setInterval> | undefined
let controller: AbortController | undefined

function clearPrivateKey() {
  // BaseDialog may keep its old slot nodes during a leave transition, while
  // unmounting can cancel Vue's queued render. Erase exposed text immediately.
  if (privateKeyElement.value) privateKeyElement.value.textContent = ''
  if (displayTimer) clearInterval(displayTimer)
  displayTimer = undefined
  privateKey.value = ''
  visible.value = false
  copyNotice.value = ''
  remainingSeconds.value = 0
}

function reset() {
  generation++
  controller?.abort()
  controller = undefined
  securityDialog.value?.forceClose()
  clearPrivateKey()
  displayedAddress.value = null
  showResult.value = false
}

function close() {
  reset()
  emit('close')
}

function startDisplayTimer() {
  const expiresAt = Date.now() + 60_000
  remainingSeconds.value = 60
  displayTimer = setInterval(() => {
    remainingSeconds.value = Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000))
    if (remainingSeconds.value === 0) clearPrivateKey()
  }, 1000)
}

function matchesAddress(result: CryptoPrivateKeyExport, selected: ExportAddress): boolean {
  if (!result || result.address_id !== selected.id || result.family !== selected.family || typeof result.address !== 'string') return false
  const sameAddress = selected.family === 'evm'
    ? result.address.toLowerCase() === selected.address.toLowerCase()
    : result.address === selected.address
  if (!sameAddress || !Number.isSafeInteger(result.derivation_index) || result.derivation_index < 0 || typeof result.private_key !== 'string') return false
  return selected.family === 'evm'
    ? result.format === 'hex' && /^(?:0x)?[0-9a-fA-F]{64}$/.test(result.private_key)
    : result.format === 'base58' && /^[1-9A-HJ-NP-Za-km-z]{64,88}$/.test(result.private_key)
}

async function requestExport(selected: ExportAddress) {
  const current = generation
  const active = () => current === generation && props.address?.id === selected.id &&
    props.address.address === selected.address && props.address.family === selected.family
  try {
    try {
      await reportWalletOperationIntent('export_private_key')
    } catch {
      if (active()) {
        appStore.showError(t('admin.crypto.walletOperationNotifyFailed'))
        close()
      }
      return
    }
    if (!active()) return
    let operationFailed = false
    const completed = await securityDialog.value?.verify(t('admin.crypto.privateKeyExportOperation', { address: selected.address }), async (verification) => {
      if (!active()) return
      let result: CryptoPrivateKeyExport | undefined
      const requestController = new AbortController()
      controller = requestController
      try {
        result = await exportAddressPrivateKey(selected.id, verification, requestController.signal)
        if (!active()) return
        if (!matchesAddress(result, selected)) throw new Error(t('admin.crypto.privateKeyExportFailed'))
        privateKey.value = result.private_key
        displayedAddress.value = selected
        startDisplayTimer()
        showResult.value = true
      } catch (error) {
        // Only a grant rejection may trigger the existing fresh-verification path.
        // Keep API response bodies out of plaintext-export errors and notices.
        if (isCryptoGrantRejected(error)) throw error
        operationFailed = true
        throw new Error(t('admin.crypto.privateKeyExportFailed'))
      } finally {
        if (result) result.private_key = ''
        if (controller === requestController) controller = undefined
      }
    })
    if (active() && !completed && !showResult.value) {
      if (operationFailed) appStore.showError(t('admin.crypto.privateKeyExportFailed'))
      close()
    }
  } catch {
    if (active()) {
      appStore.showError(t('admin.crypto.privateKeyExportFailed'))
      close()
    }
  }
}

async function copyPrivateKey() {
  if (!privateKey.value) return
  const current = generation
  try {
    await navigator.clipboard.writeText(privateKey.value)
    if (current === generation && privateKey.value) copyNotice.value = t('admin.crypto.privateKeyCopied')
  } catch {
    if (current === generation && privateKey.value) copyNotice.value = t('admin.crypto.privateKeyCopyFailed')
  }
}

watch(() => [props.address?.id, props.address?.address, props.address?.family], () => {
  reset()
  const selected = props.address
  if (selected && Number.isSafeInteger(selected.id) && selected.id > 0 && (selected.family === 'evm' || selected.family === 'solana')) {
    void requestExport({ id: selected.id, address: selected.address, family: selected.family })
  }
}, { immediate: true, flush: 'sync' })

// Nested route components share the route record; standalone test mounts do not.
if (inject(matchedRouteKey, undefined)) {
  onBeforeRouteLeave(close)
  onBeforeRouteUpdate(close)
}
onBeforeUnmount(reset)
onDeactivated(close)
</script>

<style scoped>
.crypto-private-key { display: grid; gap: 1rem; font-size: .875rem; line-height: 1.7; }
.crypto-private-key__field { display: grid; gap: .5rem; }
.crypto-private-key__label { font-weight: 600; }
.crypto-private-key code, .crypto-private-key pre { margin: 0; border: 1px solid var(--signal-line, #cbd5d1); border-radius: .5rem; padding: .75rem; background: var(--signal-surface, #fff); color: var(--signal-text, #172522); font-family: ui-monospace, monospace; font-size: .8125rem; white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-all; }
.crypto-private-key__actions { display: flex; flex-wrap: wrap; gap: .75rem; }
.crypto-private-key__hint { color: var(--signal-muted, #6b7280); font-size: .8125rem; }
</style>
