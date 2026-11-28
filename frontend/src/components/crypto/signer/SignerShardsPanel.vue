<template>
  <UPageCard variant="subtle" :ui="{ container: 'gap-y-3' }" class="signer-shards">
    <div class="min-w-0">
      <h2 class="text-base font-semibold text-highlighted">{{ t('admin.crypto.signer.title') }}</h2>
      <p class="mt-1 text-sm text-muted">{{ t('admin.crypto.signer.description') }}</p>
    </div>

    <p v-if="loading && !status" role="status" class="text-sm text-muted">{{ t('admin.crypto.signer.loading') }}</p>
    <UAlert v-if="loadError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="loadError" />

    <template v-if="status">
      <div class="flex flex-wrap items-center gap-2">
        <UBadge
          :color="status.configured ? 'success' : 'warning'"
          variant="subtle"
          icon="i-lucide-shield-check"
        >{{ t(status.configured ? 'admin.crypto.signer.configuredTitle' : 'admin.crypto.signer.notConfiguredTitle') }}</UBadge>
        <template v-if="status.configured">
          <UBadge
            :color="lockState.tone"
            variant="subtle"
            :icon="lockState.icon"
            data-testid="signer-lock-state"
            :data-state="lockState.id"
          >{{ t(lockState.labelKey) }}</UBadge>
          <template v-if="windowActive">
            <UBadge
              :color="countdownTone"
              variant="subtle"
              icon="i-lucide-timer"
              class="tabular-nums"
              data-testid="signer-unlock-countdown"
              :data-tone="countdownTone"
            >{{ t('admin.crypto.signer.unlockWindow', { time: unlockRemainingLabel }) }}</UBadge>
            <span v-if="countdownTone === 'warning'" class="text-xs text-warning" data-testid="signer-countdown-soon">{{ t('admin.crypto.signer.unlockWindowEnding') }}</span>
          </template>
        </template>
      </div>

      <!-- Secondary facts (scheme, auto-sweep share, crypto backend) stay out of the
           primary status row so they never read like an action item. -->
      <p v-if="status.configured" class="text-xs leading-relaxed text-muted" data-testid="signer-secondary-status">
        {{ secondaryStatusLabel }}
      </p>

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
          v-if="windowExpired"
          color="warning"
          variant="subtle"
          icon="i-lucide-timer-off"
          :title="t('admin.crypto.signer.lockedExpired')"
          :description="t('admin.crypto.signer.windowExpiredHint')"
          :actions="[{ label: t('admin.crypto.signer.reunlockAction'), color: 'warning', variant: 'solid', onClick: () => emit('open-sweep') }]"
          data-testid="signer-window-expired"
        />
        <UAlert
          v-else-if="status.locked"
          color="warning"
          variant="subtle"
          icon="i-lucide-lock"
          :title="t('admin.crypto.signer.locked')"
          :description="t('admin.crypto.signer.lockedHint')"
          :actions="[{ label: t('admin.crypto.signer.goUnlock'), color: 'warning', variant: 'solid', onClick: () => emit('open-sweep') }]"
          data-testid="signer-locked-notice"
        />
        <UAlert
          v-else-if="unlocked"
          color="success"
          variant="subtle"
          icon="i-lucide-timer"
          :title="t('admin.crypto.signer.unlocked')"
          :description="t('admin.crypto.signer.unlockedHint')"
          data-testid="signer-unlocked-notice"
        />
      </template>

      <!-- Empty state: guide the operator through the setup instead of a wall of
           warning badges and disabled controls. -->
      <div v-if="!status.configured" class="rounded-lg border border-default p-3" data-testid="signer-setup-steps">
        <p class="text-sm font-medium text-highlighted">{{ t('admin.crypto.signer.setupStepsTitle') }}</p>
        <ol class="mt-2 grid gap-1.5 text-sm text-muted">
          <li v-for="(step, index) in setupSteps" :key="step" class="flex gap-2">
            <span class="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full bg-primary/10 text-[10px] font-semibold text-primary-700 dark:text-primary-300">{{ index + 1 }}</span>
            <span>{{ t(step) }}</span>
          </li>
        </ol>
      </div>

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
          <!-- Locked/expired: unlocking is the primary next step. While a window is
               open the same button is a quiet secondary action, so it never competes
               with 执行归集 as the page's main action. -->
          <UButton
            :color="effectivelyLocked ? 'warning' : 'neutral'"
            :variant="effectivelyLocked ? 'solid' : 'outline'"
            icon="i-lucide-badge-check"
            data-testid="signer-verify"
            :data-locked="effectivelyLocked ? 'true' : 'false'"
            :class="effectivelyLocked ? 'signer-verify--primary' : 'signer-verify--secondary'"
            :disabled="!cryptoReady"
            @click="verifyOpen = true"
          >{{ t(effectivelyLocked ? 'admin.crypto.signer.verifyAction' : 'admin.crypto.signer.reunlockAction') }}</UButton>
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

      <!-- Advanced details are collapsed by default; the public commitment is a
           verification aid, not something the overview needs to shout. -->
      <div v-if="status.configured" class="border-t border-default pt-3">
        <UButton
          color="neutral"
          variant="ghost"
          size="xs"
          trailing-icon="i-lucide-chevron-down"
          :ui="{ trailingIcon: advancedOpen ? 'rotate-180 transition-transform' : 'transition-transform' }"
          :aria-expanded="advancedOpen ? 'true' : 'false'"
          data-testid="signer-advanced-toggle"
          @click="advancedOpen = !advancedOpen"
        >{{ t('admin.crypto.signer.advanced') }}</UButton>
        <dl v-if="advancedOpen" class="mt-2 grid gap-2 text-sm sm:grid-cols-[9rem_minmax(0,1fr)]" data-testid="signer-advanced">
          <dt class="text-muted">{{ t('admin.crypto.signer.vaultVersion') }}</dt>
          <dd class="tabular-nums text-default">{{ status.vault_version }}</dd>
          <dt class="text-muted">{{ t('admin.crypto.signer.createdAt') }}</dt>
          <dd class="tabular-nums text-default">{{ formatTime(status.created_at) }}</dd>
          <template v-if="status.dk_commitment">
            <dt class="text-muted">{{ t('admin.crypto.signer.commitment') }}</dt>
            <dd class="min-w-0">
              <p class="font-mono text-xs wrap-anywhere text-default" data-testid="signer-commitment">{{ status.dk_commitment }}</p>
              <p class="mt-1 text-xs text-muted">{{ t('admin.crypto.signer.commitmentHint') }}</p>
            </dd>
          </template>
        </dl>
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
      @unlocked="onUnlocked"
    />
  </UPageCard>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
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
  /** A verify+unlock succeeded: the parent should refresh the sweep gate. */
  (event: 'unlocked', status: SignerShardStatus): void
  /** The bounded unlock window ran out on this page: refresh the gate once. */
  (event: 'expired'): void
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
const advancedOpen = ref(false)

const crypto = ref<SignerCrypto | null>(null)
const cryptoUnavailable = ref<SignerCryptoUnavailableCode | null>(null)
const cryptoReady = computed(() => crypto.value !== null)

// The data key stays resident for a bounded window on the server. Rendering the
// remaining time (not a locally invented 15:00) is what keeps the sweep gate
// and this badge in agreement once the operator unlocks.
const unlockClock = ref(Date.now())
let unlockTicker: ReturnType<typeof setInterval> | null = null
/** Guards the one-shot expiry handling so a stale read cannot retrigger it. */
const expiryHandled = ref(false)
const unlockedUntilMs = computed(() => {
  const raw = status.value?.unlocked_until
  if (!raw) return 0
  const parsed = Date.parse(raw)
  return Number.isFinite(parsed) ? parsed : 0
})
const unlockRemainingSeconds = computed(() => {
  if (!unlockedUntilMs.value) return 0
  return Math.max(0, Math.ceil((unlockedUntilMs.value - unlockClock.value) / 1000))
})
// The backend still reports `locked:false` for a moment after the lease ends:
// only the deadline says the window is really over, and that is what the badge
// and the sweep gate must follow.
const windowExpired = computed(() => {
  if (!status.value || status.value.locked) return false
  if (!unlockedUntilMs.value) return false
  return unlockRemainingSeconds.value <= 0
})
const effectivelyLocked = computed(() => !!status.value && (status.value.locked || windowExpired.value))
const unlocked = computed(() => !!status.value && status.value.configured && !effectivelyLocked.value)
const windowActive = computed(() => unlocked.value && unlockedUntilMs.value > 0 && unlockRemainingSeconds.value > 0)
const unlockRemainingLabel = computed(() => {
  const total = unlockRemainingSeconds.value
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
})
const countdownTone = computed<string>(() => (unlockRemainingSeconds.value <= 60 && unlockRemainingSeconds.value > 0 ? 'warning' : 'success'))
const lockState = computed<{ id: 'expired' | 'locked' | 'unlocked'; tone: 'error' | 'success'; icon: string; labelKey: string }>(() => {
  if (windowExpired.value) return { id: 'expired', tone: 'error', icon: 'i-lucide-timer-off', labelKey: 'admin.crypto.signer.lockedExpired' }
  if (status.value?.locked) return { id: 'locked', tone: 'error', icon: 'i-lucide-lock', labelKey: 'admin.crypto.signer.locked' }
  return { id: 'unlocked', tone: 'success', icon: 'i-lucide-lock-open', labelKey: 'admin.crypto.signer.unlocked' }
})
const secondaryStatusLabel = computed(() => {
  if (!status.value) return ''
  const parts: string[] = []
  if (status.value.configured) {
    parts.push(`${status.value.scheme ?? 'shamir-gf256'} · ${status.value.threshold ?? 2}-of-${status.value.total ?? 3}`)
    parts.push(t(status.value.auto_enabled ? 'admin.crypto.signer.autoOn' : 'admin.crypto.signer.autoOff'))
  }
  parts.push(`${t('admin.crypto.signer.backendLabel')}: ${backendLabel.value}`)
  return parts.join(' · ')
})
const setupSteps = [
  'admin.crypto.signer.setupStep1',
  'admin.crypto.signer.setupStep2',
  'admin.crypto.signer.setupStep3'
] as const

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

// Verify+unlock succeeded: publish the unlocked status at once so the sweep
// page can enable its run button without a manual refresh, then re-read the
// authoritative status (the lease may already be a few seconds shorter).
async function onUnlocked(next: SignerShardStatus): Promise<void> {
  status.value = next
  expiryHandled.value = false
  emit('change', next)
  emit('unlocked', next)
  appStore.showSuccess(t('admin.crypto.signer.unlockDone'))
  await refresh()
}

watch(unlockRemainingSeconds, (remaining, previous) => {
  // The lease just ran out: re-read the signer exactly once so the panel flips
  // back to the locked state instead of showing a frozen "0:00" window. No
  // polling loop is started; the normal mount/refresh paths keep it current.
  if (previous > 0 && remaining === 0) {
    if (expiryHandled.value) return
    expiryHandled.value = true
    appStore.showWarning(t('admin.crypto.signer.windowExpiredToast'))
    emit('expired')
    void refresh()
    return
  }
  if (remaining > 0) expiryHandled.value = false
})

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

onMounted(() => {
  probeCrypto()
  void refresh()
  unlockTicker = setInterval(() => { unlockClock.value = Date.now() }, 1000)
})

onUnmounted(() => {
  if (unlockTicker) clearInterval(unlockTicker)
  unlockTicker = null
})

// The overview merges every status refresh into one button; the card exposes
// this so the page refresh can reuse the same loader.
defineExpose({ refresh })
</script>
