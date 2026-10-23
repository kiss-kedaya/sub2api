<template>
  <section class="wallet-security card" :class="{ 'wallet-security--locked': status?.locked || status?.deposits_paused }" aria-labelledby="wallet-security-title">
    <div class="wallet-security__heading">
      <div>
        <h2 id="wallet-security-title">{{ t('admin.crypto.walletSecurityTitle') }}</h2>
        <p class="wallet-security__hint">{{ t('admin.crypto.walletSecurityScope') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || recovering" @click="refresh">
        {{ loading ? t('common.loading') : t('common.refresh') }}
      </button>
    </div>
    <p v-if="!status && loading" role="status" class="wallet-security__hint">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="wallet-security__error">{{ error }}</p>
    <template v-if="status">
      <div class="wallet-security__badges">
        <strong v-if="status.locked || status.deposits_paused" class="wallet-security__danger">{{ t('admin.crypto.walletSecurityLocked') }}</strong>
        <span v-else>{{ t(status.enabled ? status.initialized ? 'admin.crypto.walletSecurityMonitoring' : 'admin.crypto.walletSecurityInitializing' : 'admin.crypto.walletSecurityDisabled') }}</span>
        <span v-if="status.deposits_paused" class="wallet-security__danger">{{ t('admin.crypto.walletSecurityDepositsPaused') }}</span>
        <span v-if="status.alert_pending" role="status">{{ t('admin.crypto.walletSecurityAlertPending') }}</span>
      </div>
      <dl v-if="status.locked || status.deposits_paused" class="wallet-security__details">
        <div><dt>{{ t('admin.crypto.walletSecurityReason') }}</dt><dd>{{ reasonText(status.reason) }}</dd></div>
        <div><dt>{{ t('admin.crypto.walletSecurityDetectedAt') }}</dt><dd>{{ formatTime(status.locked_at) }}</dd></div>
      </dl>
      <div v-if="status.locked || status.deposits_paused" class="wallet-security__actions">
        <p class="wallet-security__hint">{{ t('admin.crypto.walletSecurityRecoveryHint') }}</p>
        <button type="button" class="btn btn-danger btn-sm" :disabled="recovering || loading || !!error" @click="requestRecovery">
          {{ recovering ? t('common.loading') : t('admin.crypto.walletSecurityRecoverAction') }}
        </button>
      </div>
    </template>
    <BaseDialog :show="confirmOpen" :title="t('admin.crypto.walletSecurityRecoveryTitle')" @close="closeConfirmation">
      <form id="wallet-security-recover-form" class="wallet-security__confirmation" @submit.prevent="confirmRecovery">
        <p>{{ t('admin.crypto.walletSecurityRecoveryWarning') }}</p>
        <label for="wallet-security-confirmation">{{ t('admin.crypto.walletSecurityRecoveryConfirmation') }}</label>
        <input id="wallet-security-confirmation" v-model="confirmation" type="text" class="input" autocomplete="off" spellcheck="false" :disabled="recovering" />
      </form>
      <template #footer>
        <div class="wallet-security__actions">
          <button type="button" class="btn btn-secondary" :disabled="recovering" @click="closeConfirmation">{{ t('common.cancel') }}</button>
          <button type="submit" form="wallet-security-recover-form" class="btn btn-danger" :disabled="confirmation !== 'RECOVER' || recovering">
            {{ t('admin.crypto.walletSecurityVerifyRecovery') }}
          </button>
        </div>
      </template>
    </BaseDialog>
    <CryptoSecurityDialog ref="securityDialog" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CryptoSecurityDialog from '@/components/crypto/CryptoSecurityDialog.vue'
import { getWalletSecurity, recoverWalletSecurity, type WalletSecurityStatus } from '@/api/admin/crypto'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const emit = defineEmits<{
  (event: 'change', value: WalletSecurityStatus): void
  (event: 'recovered'): void
}>()
const { t } = useI18n()
const appStore = useAppStore()
const status = ref<WalletSecurityStatus | null>(null)
const error = ref('')
const loading = ref(false)
const recovering = ref(false)
const confirmOpen = ref(false)
const confirmation = ref('')
const securityDialog = ref<InstanceType<typeof CryptoSecurityDialog> | null>(null)
let pollTimer: ReturnType<typeof setInterval> | undefined
let disposed = false

function validatedStatus(value: WalletSecurityStatus): WalletSecurityStatus {
  if (!value || ['enabled', 'initialized', 'locked', 'deposits_paused', 'alert_pending'].some(key => typeof value[key as keyof WalletSecurityStatus] !== 'boolean')) {
    throw new Error(t('admin.crypto.walletSecurityLoadFailed'))
  }
  return value
}

function updateStatus(value: WalletSecurityStatus) {
  status.value = validatedStatus(value)
  error.value = ''
  emit('change', status.value)
}

async function refresh() {
  if (loading.value || recovering.value) return
  loading.value = true
  try {
    const value = await getWalletSecurity()
    if (!disposed) updateStatus(value)
  } catch (err) {
    // Keep a previously observed lock visible. A failed read never clears it.
    if (!disposed) error.value = extractApiErrorMessage(err, t('admin.crypto.walletSecurityLoadFailed'))
  } finally {
    loading.value = false
  }
}

function reasonText(reason?: string): string {
  const reasons: Record<string, string> = {
    deposit_address_changed: 'walletSecurityAddressChanged',
    deposit_pool_changed: 'walletSecurityPoolChanged',
    deposit_address_derivation_mismatch: 'walletSecurityAddressMismatch',
    deposit_pool_derivation_mismatch: 'walletSecurityPoolMismatch',
    security_snapshot_missing: 'walletSecuritySnapshotMissing',
    security_snapshot_authentication_failed: 'walletSecuritySnapshotInvalid',
    unauthorized_token_outflow: 'walletSecurityUnexpectedOutflow'
  }
  const key = reason ? reasons[reason] : undefined
  return t(`admin.crypto.${key || 'walletSecurityUnknownReason'}`)
}

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function requestRecovery() {
  if (!status.value || (!status.value.locked && !status.value.deposits_paused) || recovering.value || error.value) return
  confirmation.value = ''
  confirmOpen.value = true
}

function closeConfirmation() {
  if (recovering.value) return
  confirmOpen.value = false
  confirmation.value = ''
}

async function confirmRecovery() {
  if (confirmation.value !== 'RECOVER' || recovering.value || !status.value || (!status.value.locked && !status.value.deposits_paused)) return
  confirmOpen.value = false
  confirmation.value = ''
  recovering.value = true
  try {
    const completed = await securityDialog.value?.verify(t('admin.crypto.walletSecurityRecoveryTitle'), async (code) => {
      const recovered = validatedStatus(await recoverWalletSecurity({ totpCode: code }))
      if (disposed) return
      updateStatus(recovered)
      if (recovered.locked || recovered.deposits_paused) throw new Error(t('admin.crypto.walletSecurityRecoveryStillLocked'))
    })
    if (completed && !disposed) {
      appStore.showSuccess(t('admin.crypto.walletSecurityRecovered'))
      emit('recovered')
    }
  } finally {
    recovering.value = false
  }
}

onMounted(() => {
  void refresh()
  pollTimer = setInterval(() => { if (!document.hidden) void refresh() }, 15000)
})
onUnmounted(() => {
  disposed = true
  if (pollTimer) clearInterval(pollTimer)
  securityDialog.value?.forceClose()
  confirmation.value = ''
})
</script>

<style scoped>
.wallet-security { padding: 1.25rem; margin-bottom: 1.25rem; border: 1px solid var(--signal-line, #64748b40); }
.wallet-security--locked { border-color: #dc262680; background: var(--signal-surface, #fff); }
.wallet-security__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
.wallet-security__heading > button { flex-shrink: 0; white-space: nowrap; }
.wallet-security h2 { font-size: .9375rem; font-weight: 650; }
.wallet-security__hint { margin-top: .4rem; font-size: .8125rem; line-height: 1.7; color: var(--signal-muted, #64748b); }
.wallet-security__badges { display: flex; gap: 1rem; flex-wrap: wrap; margin-top: .875rem; font-size: .8125rem; }
.wallet-security__danger, .wallet-security__error { color: #dc2626; }
.wallet-security__error { margin-top: .75rem; font-size: .8125rem; }
.wallet-security__details { display: grid; gap: .625rem; margin-top: 1rem; font-size: .8125rem; }
.wallet-security__details div { display: grid; grid-template-columns: 7rem minmax(0, 1fr); gap: .75rem; }
.wallet-security__details dt { color: var(--signal-muted, #64748b); }
.wallet-security__details dd { overflow-wrap: anywhere; }
.wallet-security__actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: .75rem; margin-top: 1rem; }
.wallet-security__actions p { flex: 1 1 18rem; }
.wallet-security__confirmation { display: grid; gap: .875rem; font-size: .875rem; line-height: 1.8; }
@media(max-width: 640px) { .wallet-security__details div { grid-template-columns: minmax(0, 1fr); gap: .25rem; } }
</style>
