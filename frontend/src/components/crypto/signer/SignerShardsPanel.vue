<template>
  <UPageCard variant="subtle" :ui="{ container: 'gap-y-3' }" class="signer-shards">
    <div class="flex items-start justify-between gap-4">
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-highlighted">{{ t('admin.crypto.signer.title') }}</h2>
        <p class="mt-1 text-sm text-muted">{{ t('admin.crypto.signer.description') }}</p>
      </div>
      <UButton
        color="neutral"
        variant="outline"
        size="sm"
        class="shrink-0"
        data-testid="signer-shards-refresh"
        :disabled="loading"
        @click="refresh"
      >{{ loading ? t('common.loading') : t('admin.crypto.signer.refresh') }}</UButton>
    </div>

    <p v-if="loading && !status" role="status" class="text-sm text-muted">{{ t('admin.crypto.signer.loading') }}</p>
    <UAlert v-if="loadError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="loadError" />

    <template v-if="status">
      <div class="flex flex-wrap gap-2">
        <UBadge
          :color="status.configured ? 'success' : 'warning'"
          variant="subtle"
          icon="i-lucide-shield-check"
        >{{ t(status.configured ? 'admin.crypto.signer.configuredTitle' : 'admin.crypto.signer.notConfiguredTitle') }}</UBadge>
        <UBadge v-if="status.configured" :color="status.locked ? 'error' : 'success'" variant="subtle" icon="i-lucide-lock">
          {{ t(status.locked ? 'admin.crypto.signer.locked' : 'admin.crypto.signer.unlocked') }}
        </UBadge>
        <UBadge v-if="status.configured" color="neutral" variant="subtle">
          {{ status.scheme ?? 'shamir-gf256' }} · {{ status.threshold ?? 2 }}-of-{{ status.total ?? 3 }}
        </UBadge>
        <UBadge v-if="status.configured" :color="status.auto_enabled ? 'warning' : 'neutral'" variant="subtle">
          {{ t(status.auto_enabled ? 'admin.crypto.signer.autoOn' : 'admin.crypto.signer.autoOff') }}
        </UBadge>
        <UBadge color="neutral" variant="outline" data-testid="signer-shards-backend">{{ backendLabel }}</UBadge>
      </div>

      <UAlert
        v-if="!status.configured"
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :title="t('admin.crypto.signer.notConfiguredTitle')"
        :description="t('admin.crypto.signer.notConfiguredHint')"
      />
      <template v-else>
        <p class="text-sm text-muted">{{ t('admin.crypto.signer.configuredHint') }}</p>
        <UAlert
          v-if="status.locked"
          color="warning"
          variant="subtle"
          icon="i-lucide-lock"
          :title="t('admin.crypto.signer.locked')"
          :description="t('admin.crypto.signer.lockedHint')"
          :actions="[{ label: t('admin.crypto.signer.goUnlock'), color: 'warning', variant: 'solid', onClick: () => emit('open-sweep') }]"
        />
        <dl class="grid gap-2 text-sm sm:grid-cols-[9rem_minmax(0,1fr)]">
          <dt class="text-muted">{{ t('admin.crypto.signer.vaultVersion') }}</dt>
          <dd class="text-default">{{ status.vault_version }}</dd>
          <dt class="text-muted">{{ t('admin.crypto.signer.createdAt') }}</dt>
          <dd class="text-default">{{ formatTime(status.created_at) }}</dd>
          <template v-if="status.dk_commitment">
            <dt class="text-muted">{{ t('admin.crypto.signer.commitment') }}</dt>
            <dd class="min-w-0">
              <p class="font-mono text-xs break-all text-default">{{ status.dk_commitment }}</p>
              <p class="mt-1 text-xs text-muted">{{ t('admin.crypto.signer.commitmentHint') }}</p>
            </dd>
          </template>
        </dl>
      </template>

      <UAlert
        v-if="!cryptoReady"
        color="error"
        variant="subtle"
        icon="i-lucide-shield-alert"
        :title="t('admin.crypto.signer.insecureTitle')"
        :description="t(cryptoUnavailableKey || 'admin.crypto.signer.noAes')"
      />

      <div class="flex flex-wrap items-center gap-2">
        <UButton
          v-if="!status.configured"
          icon="i-lucide-split"
          data-testid="signer-generate"
          :disabled="!cryptoReady || loading"
          @click="openWizard('setup')"
        >{{ t('admin.crypto.signer.generateAction') }}</UButton>
        <template v-else>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-badge-check"
            data-testid="signer-verify"
            :disabled="!cryptoReady"
            @click="verifyOpen = true"
          >{{ t('admin.crypto.signer.verifyAction') }}</UButton>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            data-testid="signer-resplit"
            :disabled="!cryptoReady || !status.dk_commitment"
            @click="openWizard('resplit')"
          >{{ t('admin.crypto.signer.resplitAction') }}</UButton>
        </template>
      </div>
      <p class="text-xs leading-relaxed text-muted">{{ t('admin.crypto.signer.noStorageNotice') }}</p>
    </template>

    <SignerShardSetupWizard
      :show="wizardOpen"
      :mode="wizardMode"
      :notice="notice"
      :status="status"
      :crypto="crypto"
      :crypto-unavailable="cryptoUnavailable"
      @close="wizardOpen = false"
      @completed="onWizardCompleted"
    />
    <ShardUnlockDialog
      :show="verifyOpen"
      mode="verify"
      :crypto="crypto"
      :locked="status?.locked ?? false"
      @close="verifyOpen = false"
    />
  </UPageCard>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ShardUnlockDialog from '@/components/crypto/signer/ShardUnlockDialog.vue'
import SignerShardSetupWizard from '@/components/crypto/signer/SignerShardSetupWizard.vue'
import {
  getSignerRiskNotice,
  getSignerShardStatus,
  type SignerRiskNotice,
  type SignerShardStatus
} from '@/api/admin/cryptoSigner'
import {
  detectSignerCrypto,
  detectSignerCryptoSupport,
  type SignerCrypto,
  type SignerCryptoUnavailableCode
} from '@/utils/signer/cryptoProvider'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

const emit = defineEmits<{
  (event: 'change', status: SignerShardStatus): void
  (event: 'open-sweep'): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const status = ref<SignerShardStatus | null>(null)
const notice = ref<SignerRiskNotice | null>(null)
const loading = ref(false)
const loadError = ref('')
const wizardOpen = ref(false)
const wizardMode = ref<'setup' | 'resplit'>('setup')
const verifyOpen = ref(false)

const crypto = ref<SignerCrypto | null>(null)
const cryptoUnavailable = ref<SignerCryptoUnavailableCode | null>(null)
const cryptoReady = computed(() => crypto.value !== null)

const cryptoUnavailableKey = computed(() => (cryptoUnavailable.value === 'insecure-context'
  ? 'admin.crypto.signer.insecureContext'
  : cryptoUnavailable.value === 'no-csprng'
    ? 'admin.crypto.signer.noCsprng'
    : 'admin.crypto.signer.noAes'))

const backendLabel = computed(() => (crypto.value?.backend === 'noble'
  ? t('admin.crypto.signer.noble')
  : t('admin.crypto.signer.webcrypto')))

function probeCrypto(): void {
  const support = detectSignerCryptoSupport()
  if (!support.supported) {
    cryptoUnavailable.value = support.reason
    crypto.value = null
    return
  }
  try {
    crypto.value = detectSignerCrypto()
    cryptoUnavailable.value = null
  } catch {
    crypto.value = null
    cryptoUnavailable.value = support.reason ?? 'no-aes-gcm'
  }
}

async function refresh(): Promise<void> {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const [nextStatus, nextNotice] = await Promise.all([
      getSignerShardStatus(),
      getSignerRiskNotice().catch(() => null)
    ])
    status.value = nextStatus
    if (nextNotice) notice.value = nextNotice
    emit('change', nextStatus)
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, t('admin.crypto.signer.loadFailed'))
  } finally {
    loading.value = false
  }
}

function openWizard(mode: 'setup' | 'resplit'): void {
  if (!cryptoReady.value) return
  wizardMode.value = mode
  wizardOpen.value = true
}

async function onWizardCompleted(next: SignerShardStatus): Promise<void> {
  status.value = next
  emit('change', next)
  appStore.showSuccess(t('admin.crypto.signer.wizard.doneTitle'))
  await refresh()
}

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

onMounted(() => {
  probeCrypto()
  void refresh()
})
</script>
