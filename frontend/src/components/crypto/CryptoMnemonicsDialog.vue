<template>
  <BaseDialog :show="show" :title="t('admin.crypto.mnemonicsTitle')" width="wide" @close="close">
    <div class="crypto-mnemonics text-sm">
      <Transition name="mnemonic-panel" mode="out-in">
        <div v-if="!hasMnemonics" key="request" class="crypto-mnemonics__intro space-y-4">
          <UAlert v-if="expiredNotice" role="status" class="crypto-mnemonics__expired" color="success" variant="subtle" icon="i-lucide-circle-check" :title="t('admin.crypto.mnemonicsExpired')" />
          <UAlert color="warning" variant="subtle" icon="i-lucide-lock" :title="t('admin.crypto.mnemonicsSensitive')" :description="t('admin.crypto.mnemonicsWarning')" />

          <div class="crypto-mnemonics__risk-map grid gap-4 sm:grid-cols-2" role="group" :aria-label="t('admin.crypto.mnemonicsScope')">
            <div class="flex gap-3">
              <UBadge color="warning" variant="outline" class="h-6 shrink-0 font-mono">01</UBadge>
              <div class="min-w-0">
                <strong class="block font-medium text-highlighted">{{ t('admin.crypto.mnemonicsAllUsers') }}</strong>
                <p class="mt-1 text-xs leading-relaxed text-muted">{{ t('admin.crypto.mnemonicsDepositWarning') }}</p>
              </div>
            </div>
            <div class="flex gap-3">
              <UBadge color="neutral" variant="outline" class="h-6 shrink-0 font-mono">02</UBadge>
              <div class="min-w-0">
                <strong class="block font-medium text-highlighted">{{ t('admin.crypto.mnemonicsGasOnly') }}</strong>
                <p class="mt-1 text-xs leading-relaxed text-muted">{{ t('admin.crypto.mnemonicsHotWarning') }}</p>
              </div>
            </div>
          </div>

          <USeparator />
          <form id="crypto-mnemonics-form" class="crypto-mnemonics__form space-y-3" autocomplete="off" @submit.prevent="requestExport">
            <UAlert v-if="operationIntentError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="operationIntentError" />
            <UCheckbox v-model="productionConfirmed" data-testid="crypto-mnemonics-confirm" :disabled="busy" :label="t('admin.crypto.mnemonicsProductionConfirm')" />
          </form>
        </div>

        <div v-else key="results" class="crypto-mnemonics__results space-y-4" aria-live="off">
          <div class="crypto-mnemonics__session flex items-center gap-3 text-xs text-muted tabular-nums" role="timer" aria-live="off">
            <UIcon name="i-lucide-shield-check" class="size-4 shrink-0" />
            <span>{{ t('admin.crypto.mnemonicsAutoClear', { seconds: remainingSeconds }) }}</span>
            <UProgress :model-value="remainingSeconds" :max="DISPLAY_SECONDS" size="xs" class="w-20 shrink-0" aria-hidden="true" />
          </div>
          <p class="text-xs leading-relaxed text-muted">{{ t('admin.crypto.mnemonicsResultHint') }}</p>

          <section
            v-for="(seed, seedIndex) in seeds"
            :key="seed.kind"
            class="crypto-mnemonics__seed space-y-3 rounded-lg p-4 ring ring-default"
            :class="[`crypto-mnemonics__seed--${seed.kind}`, { 'crypto-mnemonics__seed--revealed': seed.visible }]"
            :aria-labelledby="`crypto-${seed.kind}-seed-title`"
          >
            <div class="flex items-start justify-between gap-3 max-sm:flex-col">
              <div class="flex min-w-0 items-start gap-3">
                <UBadge :color="seed.kind === 'deposit' ? 'warning' : 'neutral'" variant="outline" class="h-6 shrink-0 font-mono">{{ String(seedIndex + 1).padStart(2, '0') }}</UBadge>
                <div class="min-w-0">
                  <h4 :id="`crypto-${seed.kind}-seed-title`" class="font-semibold text-highlighted">{{ t(seed.title) }}</h4>
                  <p class="mt-1 text-xs leading-relaxed text-muted">{{ t(seed.warning) }}</p>
                </div>
              </div>
              <UBadge :color="seed.kind === 'deposit' ? 'warning' : 'neutral'" variant="subtle" class="shrink-0">{{ t(seed.kindLabel) }}</UBadge>
            </div>
            <div class="flex flex-wrap items-center justify-between gap-3">
              <UButton
                color="neutral"
                variant="outline"
                size="sm"
                :icon="seed.visible ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                :aria-controls="`crypto-${seed.kind}-seed-part-1 crypto-${seed.kind}-seed-part-2`"
                :aria-pressed="seed.visible"
                @click="toggleVisible(seed.kind)"
              >{{ t(seed.visible ? 'admin.crypto.mnemonicsHide' : 'admin.crypto.mnemonicsShow') }}</UButton>
              <span class="flex items-center gap-2 text-xs text-muted tabular-nums" :aria-label="t('admin.crypto.mnemonicsPartProgress', { copied: copiedCount(seed.kind), total: 2 })">
                <UProgress :model-value="copiedCount(seed.kind)" :max="2" size="xs" class="w-12" aria-hidden="true" />
                {{ t('admin.crypto.mnemonicsPartProgress', { copied: copiedCount(seed.kind), total: 2 }) }}
              </span>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <section
                v-for="segment in mnemonicParts(seed.value)"
                :key="`${seed.kind}-part-${segment.part}`"
                class="crypto-mnemonics__part space-y-2 rounded-md bg-elevated/50 p-3"
                :class="{ 'crypto-mnemonics__part--copied': isCopied(seed.kind, segment.part) }"
                :aria-label="partLabel(seed.kind, segment)"
              >
                <header class="flex items-center justify-between gap-2">
                  <div class="grid min-w-0 gap-0.5">
                    <span class="text-xs font-semibold text-highlighted">{{ t('admin.crypto.mnemonicsPart', { part: segment.part }) }}</span>
                    <span class="text-[11px] text-muted tabular-nums">{{ t('admin.crypto.mnemonicsWordRange', { start: wordNumber(segment.start), end: wordNumber(segment.start + segment.words.length - 1) }) }}</span>
                  </div>
                  <UButton
                    :color="isCopied(seed.kind, segment.part) ? 'success' : 'neutral'"
                    variant="outline"
                    size="xs"
                    class="crypto-mnemonics__part-copy shrink-0"
                    :icon="isCopied(seed.kind, segment.part) ? 'i-lucide-check' : 'i-lucide-clipboard'"
                    :data-copy-part="`${seed.kind}-${segment.part}`"
                    :aria-label="t('admin.crypto.mnemonicsCopyPart', { wallet: t(seed.kind === 'deposit' ? 'admin.crypto.depositMnemonicShort' : 'admin.crypto.hotMnemonicShort'), part: segment.part })"
                    @click="copyMnemonic(seed.kind, segment.part)"
                  >{{ t(isCopied(seed.kind, segment.part) ? 'admin.crypto.mnemonicsCopiedAction' : 'admin.crypto.mnemonicsCopyPartAction') }}</UButton>
                </header>
                <ol
                  :id="`crypto-${seed.kind}-seed-part-${segment.part}`"
                  class="crypto-mnemonics__words"
                  :class="{ 'crypto-mnemonics__words--masked': !seed.visible }"
                  :aria-label="seed.visible ? partLabel(seed.kind, segment) : undefined"
                >
                  <template v-if="seed.visible">
                    <li v-for="(word, index) in segment.words" :key="`${seed.kind}-${segment.part}-${index}`">
                      <span>{{ wordNumber(segment.start + index) }}</span><strong>{{ word }}</strong>
                    </li>
                  </template>
                  <template v-else>
                    <li v-for="index in segment.words.length" :key="`${seed.kind}-${segment.part}-mask-${index}`">
                      <span>{{ wordNumber(segment.start + index - 1) }}</span><strong aria-hidden="true">••••</strong>
                    </li>
                  </template>
                </ol>
              </section>
            </div>
          </section>
          <p v-if="copyNotice" role="status" class="crypto-mnemonics__notice flex items-start gap-2 text-xs" :class="copyFailed ? 'text-error' : 'text-success'">
            <UIcon :name="copyFailed ? 'i-lucide-circle-alert' : 'i-lucide-circle-check'" class="mt-px size-4 shrink-0" />
            {{ copyNotice }}
          </p>
        </div>
      </Transition>
    </div>

    <template #footer>
      <div class="crypto-mnemonics__footer flex flex-wrap justify-end gap-2">
        <UButton color="neutral" variant="subtle" data-testid="crypto-mnemonics-cancel" :disabled="busy" @click="close">{{ t('common.cancel') }}</UButton>
        <UButton v-if="!hasMnemonics" form="crypto-mnemonics-form" type="submit" color="error" :icon="busy ? undefined : 'i-lucide-lock'" :disabled="busy || !productionConfirmed">
          {{ busy ? t('common.processing') : t('admin.crypto.mnemonicsExportAction') }}
        </UButton>
        <UButton v-else data-testid="crypto-mnemonics-close" @click="close">{{ t('common.close') }}</UButton>
      </div>
    </template>
    <CryptoSecurityDialog ref="securityDialog" />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CryptoSecurityDialog from './CryptoSecurityDialog.vue'
import { exportWalletMnemonics, reportWalletOperationIntent } from '@/api/admin/crypto'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

const DISPLAY_SECONDS = 60
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const securityDialog = ref<InstanceType<typeof CryptoSecurityDialog> | null>(null)
const productionConfirmed = ref(false)
const depositMnemonic = ref('')
const hotMnemonic = ref('')
const depositVisible = ref(false)
const hotVisible = ref(false)
const copyNotice = ref('')
const copyFailed = ref(false)
const copiedParts = ref<Set<string>>(new Set())
const remainingSeconds = ref(0)
const expiredNotice = ref(false)
const busy = ref(false)
const operationIntentError = ref('')
const hasMnemonics = computed(() => Boolean(depositMnemonic.value && hotMnemonic.value))
// Both seeds render from one block; deposit first, as before.
const seeds = computed(() => [
  { kind: 'deposit' as const, value: depositMnemonic.value, visible: depositVisible.value, title: 'admin.crypto.mnemonicsDepositTitle', warning: 'admin.crypto.mnemonicsDepositWarning', kindLabel: 'admin.crypto.mnemonicsAllUsers' },
  { kind: 'hot' as const, value: hotMnemonic.value, visible: hotVisible.value, title: 'admin.crypto.hotMnemonic', warning: 'admin.crypto.mnemonicsHotWarning', kindLabel: 'admin.crypto.mnemonicsGasOnly' }
])
function toggleVisible(kind: 'deposit' | 'hot') {
  if (kind === 'deposit') depositVisible.value = !depositVisible.value
  else hotVisible.value = !hotVisible.value
}
let generation = 0
let displayTimer: ReturnType<typeof setInterval> | undefined
let copyTimer: ReturnType<typeof setTimeout> | undefined

function clearDisplayTimer() {
  if (displayTimer) clearInterval(displayTimer)
  displayTimer = undefined
}

function clearCopyTimer() {
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = undefined
}

function clearSecrets() {
  clearDisplayTimer()
  clearCopyTimer()
  depositMnemonic.value = ''
  hotMnemonic.value = ''
  depositVisible.value = false
  hotVisible.value = false
  productionConfirmed.value = false
  copyNotice.value = ''
  copyFailed.value = false
  copiedParts.value = new Set()
  remainingSeconds.value = 0
  expiredNotice.value = false
  operationIntentError.value = ''
}

function expireDisplayedSeeds() {
  clearDisplayTimer()
  clearCopyTimer()
  depositMnemonic.value = ''
  hotMnemonic.value = ''
  depositVisible.value = false
  hotVisible.value = false
  copiedParts.value = new Set()
  copyNotice.value = ''
  copyFailed.value = false
  remainingSeconds.value = 0
  expiredNotice.value = true
}

function startDisplayTimer() {
  clearDisplayTimer()
  const expiresAt = Date.now() + DISPLAY_SECONDS * 1000
  remainingSeconds.value = DISPLAY_SECONDS
  displayTimer = setInterval(() => {
    const seconds = Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000))
    remainingSeconds.value = seconds
    if (seconds === 0) expireDisplayedSeeds()
  }, 1000)
}

function words(value: string): string[] {
  return value.trim().split(/\s+/).filter(Boolean)
}

function wordNumber(index: number): string {
  return String(index + 1).padStart(2, '0')
}

interface MnemonicPart {
  part: 1 | 2
  start: number
  words: string[]
}

function mnemonicParts(value: string): MnemonicPart[] {
  const seedWords = words(value)
  const midpoint = Math.ceil(seedWords.length / 2)
  const segments: MnemonicPart[] = [
    { part: 1, start: 0, words: seedWords.slice(0, midpoint) },
    { part: 2, start: midpoint, words: seedWords.slice(midpoint) }
  ]
  return segments.filter((segment) => segment.words.length > 0)
}

function copyKey(kind: 'deposit' | 'hot', part: number): string {
  return `${kind}:${part}`
}

function isCopied(kind: 'deposit' | 'hot', part: number): boolean {
  return copiedParts.value.has(copyKey(kind, part))
}

function copiedCount(kind: 'deposit' | 'hot'): number {
  return Number(isCopied(kind, 1)) + Number(isCopied(kind, 2))
}

function partLabel(kind: 'deposit' | 'hot', segment: MnemonicPart): string {
  return t('admin.crypto.mnemonicsPartLabel', {
    wallet: t(kind === 'deposit' ? 'admin.crypto.depositMnemonicShort' : 'admin.crypto.hotMnemonicShort'),
    part: segment.part,
    start: wordNumber(segment.start),
    end: wordNumber(segment.start + segment.words.length - 1)
  })
}

async function copyMnemonic(kind: 'deposit' | 'hot', part: 1 | 2) {
  const value = kind === 'deposit' ? depositMnemonic.value : hotMnemonic.value
  const segment = mnemonicParts(value).find((item) => item.part === part)
  if (!segment) return
  const current = generation
  const active = () => current === generation && props.show && hasMnemonics.value
  clearCopyTimer()
  try {
    await navigator.clipboard.writeText(segment.words.join(' '))
    if (!active()) return
    copyFailed.value = false
    copyNotice.value = t('admin.crypto.mnemonicsCopiedPart', {
      wallet: t(kind === 'deposit' ? 'admin.crypto.depositMnemonicShort' : 'admin.crypto.hotMnemonicShort'),
      part
    })
    copiedParts.value = new Set(copiedParts.value).add(copyKey(kind, part))
  } catch {
    if (!active()) return
    copyFailed.value = true
    copyNotice.value = t('admin.crypto.mnemonicsClipboardFailed')
  }
  copyTimer = setTimeout(() => {
    copyNotice.value = ''
  }, 3500)
}

async function requestExport() {
  if (busy.value || !productionConfirmed.value) return
  busy.value = true
  copyNotice.value = ''
  const current = generation
  try {
    operationIntentError.value = ''
    try {
      await reportWalletOperationIntent('export_mnemonics')
    } catch {
      if (current === generation && props.show) {
        operationIntentError.value = t('admin.crypto.walletOperationNotifyFailed')
        appStore.showError(operationIntentError.value)
      }
      return
    }
    if (current !== generation || !props.show) return
    await securityDialog.value?.verify(t('admin.crypto.mnemonicsRiskOperation'), async (verification) => {
      let result: Awaited<ReturnType<typeof exportWalletMnemonics>> | undefined
      try {
        result = await exportWalletMnemonics(verification)
        if (current !== generation || !props.show) return
        if (!result.deposit_mnemonic?.trim() || !result.hot_mnemonic?.trim()) throw new Error(t('admin.crypto.mnemonicsInvalidResponse'))
        depositMnemonic.value = result.deposit_mnemonic.trim()
        hotMnemonic.value = result.hot_mnemonic.trim()
        startDisplayTimer()
      } finally {
        if (result) {
          result.deposit_mnemonic = ''
          result.hot_mnemonic = ''
        }
      }
    })
  } catch (error) {
    if (current === generation) appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    if (current === generation) busy.value = false
  }
}

function close() {
  generation++
  clearSecrets()
  busy.value = false
  emit('close')
}

watch(() => props.show, (isOpen) => {
  if (!isOpen) securityDialog.value?.forceClose()
  generation++
  clearSecrets()
  busy.value = false
})

onUnmounted(() => { generation++; clearSecrets() })
</script>

<style scoped>
/* Word grid of a revealed or masked mnemonic half: plain cells, no decoration. */
.crypto-mnemonics__words { display: grid; grid-template-columns: repeat(auto-fit, minmax(4.75rem, 1fr)); gap: .25rem .5rem; margin: 0; padding: 0; list-style: none; }
.crypto-mnemonics__words li { display: flex; align-items: baseline; gap: .375rem; min-width: 0; padding: .25rem 0; border-bottom: 1px solid var(--ui-border); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: .8125rem; line-height: 1.4; }
.crypto-mnemonics__words li > span { color: var(--ui-text-dimmed); font-size: .625rem; }
.crypto-mnemonics__words li > strong { min-width: 0; overflow-wrap: anywhere; font-weight: 600; color: var(--ui-text-highlighted); }
.crypto-mnemonics__words--masked li > strong { color: var(--ui-text-dimmed); letter-spacing: .08em; user-select: none; }
.mnemonic-panel-enter-active, .mnemonic-panel-leave-active { transition: opacity 150ms ease; }
.mnemonic-panel-enter-from, .mnemonic-panel-leave-to { opacity: 0; }
@media (prefers-reduced-motion: reduce) { .mnemonic-panel-enter-active, .mnemonic-panel-leave-active { transition: none; } }
</style>
