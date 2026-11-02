<template>
  <section class="wallet-security" aria-labelledby="wallet-security-title">
    <UPageCard variant="subtle" :ui="{ container: 'gap-y-3' }">
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <h2 id="wallet-security-title" class="text-base font-semibold text-highlighted">{{ t('admin.crypto.walletSecurityTitle') }}</h2>
          <p class="wallet-security__hint mt-1 text-sm text-muted">{{ t('admin.crypto.walletSecurityScope') }}</p>
        </div>
        <UButton color="neutral" variant="outline" size="sm" class="shrink-0" data-testid="wallet-security-refresh" :disabled="loading || recovering" @click="refresh">
          {{ loading ? t('common.loading') : t('common.refresh') }}
        </UButton>
      </div>
      <p v-if="!status && loading" role="status" class="text-sm text-muted">{{ t('common.loading') }}</p>
      <UAlert v-if="error" role="alert" class="wallet-security__error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      <template v-if="status">
        <div class="wallet-security__badges flex flex-wrap gap-2">
          <UBadge v-if="status.locked || status.deposits_paused" color="error" variant="subtle" icon="i-lucide-lock">{{ t('admin.crypto.walletSecurityLocked') }}</UBadge>
          <UBadge v-else :color="status.enabled ? status.initialized ? 'success' : 'warning' : 'neutral'" variant="subtle" icon="i-lucide-shield-check">
            {{ t(status.enabled ? status.initialized ? 'admin.crypto.walletSecurityMonitoring' : 'admin.crypto.walletSecurityInitializing' : 'admin.crypto.walletSecurityDisabled') }}
          </UBadge>
          <UBadge v-if="status.deposits_paused" color="error" variant="subtle">{{ t('admin.crypto.walletSecurityDepositsPaused') }}</UBadge>
          <UBadge v-if="status.alert_pending" role="status" color="warning" variant="subtle" icon="i-lucide-bell">{{ t('admin.crypto.walletSecurityAlertPending') }}</UBadge>
        </div>
        <UAlert v-if="status.locked || status.deposits_paused" color="error" variant="subtle" icon="i-lucide-triangle-alert">
          <template #description>
            <dl class="wallet-security__details grid gap-2 text-sm">
              <div class="grid gap-1 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-3"><dt class="text-muted">{{ t('admin.crypto.walletSecurityReason') }}</dt><dd class="wrap-anywhere text-default">{{ reasonText(status.reason) }}</dd></div>
              <div class="grid gap-1 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-3"><dt class="text-muted">{{ t('admin.crypto.walletSecurityDetectedAt') }}</dt><dd class="wrap-anywhere text-default">{{ formatTime(status.locked_at) }}</dd></div>
            </dl>
            <div class="wallet-security__actions mt-3 flex flex-wrap items-center justify-between gap-3">
              <p class="min-w-0 flex-1 basis-72 text-sm text-muted">{{ t('admin.crypto.walletSecurityRecoveryHint') }}</p>
              <UButton color="error" size="sm" data-testid="wallet-security-recover" :disabled="recovering || loading || !!error" @click="requestRecovery">
                {{ recovering ? t('common.loading') : t('admin.crypto.walletSecurityRecoverAction') }}
              </UButton>
            </div>
          </template>
        </UAlert>
      </template>
    </UPageCard>
    <BaseDialog :show="confirmOpen" :title="t('admin.crypto.walletSecurityRecoveryTitle')" @close="closeConfirmation">
      <form id="wallet-security-recover-form" class="space-y-4 text-sm" @submit.prevent="confirmRecovery">
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.crypto.walletSecurityRecoveryWarning')" />
        <UFormField :label="t('admin.crypto.walletSecurityRecoveryConfirmation')">
          <UInput id="wallet-security-confirmation" v-model="confirmation" type="text" autocomplete="off" spellcheck="false" :disabled="recovering" class="w-full" :ui="{ base: 'font-mono' }" />
        </UFormField>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" data-testid="wallet-security-cancel" :disabled="recovering" @click="closeConfirmation">{{ t('common.cancel') }}</UButton>
          <UButton type="submit" form="wallet-security-recover-form" color="error" data-testid="wallet-security-verify" :disabled="confirmation !== 'RECOVER' || recovering">
            {{ t('admin.crypto.walletSecurityVerifyRecovery') }}
          </UButton>
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
    const completed = await securityDialog.value?.verify(t('admin.crypto.walletSecurityRecoveryTitle'), async (verification) => {
      const recovered = validatedStatus(await recoverWalletSecurity(verification))
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

