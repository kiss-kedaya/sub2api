<template>
  <BaseDialog :show="show" :title="t('admin.crypto.mnemonicsTitle')" width="wide" @close="close">
    <div class="crypto-mnemonics">
      <Transition name="mnemonic-panel" mode="out-in">
        <div v-if="!hasMnemonics" key="request" class="crypto-mnemonics__intro">
          <p v-if="expiredNotice" role="status" class="crypto-mnemonics__expired">
            <Icon name="checkCircle" size="sm" />
            {{ t('admin.crypto.mnemonicsExpired') }}
          </p>

          <header class="crypto-mnemonics__intro-head">
            <span class="crypto-mnemonics__shield" aria-hidden="true"><Icon name="lock" size="md" /></span>
            <div>
              <p class="crypto-mnemonics__eyebrow">{{ t('admin.crypto.mnemonicsSensitive') }}</p>
              <p class="crypto-mnemonics__lead">{{ t('admin.crypto.mnemonicsWarning') }}</p>
            </div>
          </header>

          <div class="crypto-mnemonics__risk-map" role="group" :aria-label="t('admin.crypto.mnemonicsScope')">
            <div class="crypto-mnemonics__risk-row crypto-mnemonics__risk-row--deposit">
              <span class="crypto-mnemonics__step">01</span>
              <div>
                <strong>{{ t('admin.crypto.mnemonicsAllUsers') }}</strong>
                <p>{{ t('admin.crypto.mnemonicsDepositWarning') }}</p>
              </div>
            </div>
            <div class="crypto-mnemonics__risk-row crypto-mnemonics__risk-row--hot">
              <span class="crypto-mnemonics__step">02</span>
              <div>
                <strong>{{ t('admin.crypto.mnemonicsGasOnly') }}</strong>
                <p>{{ t('admin.crypto.mnemonicsHotWarning') }}</p>
              </div>
            </div>
          </div>

          <form id="crypto-mnemonics-form" class="crypto-mnemonics__form" autocomplete="off" @submit.prevent="requestExport">
            <p v-if="operationIntentError" role="alert" class="text-red-600 dark:text-red-400">{{ operationIntentError }}</p>
            <label class="crypto-mnemonics__confirm">
              <input v-model="productionConfirmed" type="checkbox" :disabled="busy" />
              <span>{{ t('admin.crypto.mnemonicsProductionConfirm') }}</span>
            </label>
          </form>
        </div>

        <div v-else key="results" class="crypto-mnemonics__results" aria-live="off">
          <div class="crypto-mnemonics__session" role="timer" aria-live="off">
            <span class="crypto-mnemonics__session-mark"><Icon name="shield" size="sm" /></span>
            <span>{{ t('admin.crypto.mnemonicsAutoClear', { seconds: remainingSeconds }) }}</span>
            <span class="crypto-mnemonics__session-track" aria-hidden="true">
              <span :style="{ transform: `scaleX(${remainingSeconds / DISPLAY_SECONDS})` }"></span>
            </span>
          </div>
          <p class="crypto-mnemonics__result-note">{{ t('admin.crypto.mnemonicsResultHint') }}</p>

          <section
            class="crypto-mnemonics__seed crypto-mnemonics__seed--deposit"
            :class="{ 'crypto-mnemonics__seed--revealed': depositVisible }"
            aria-labelledby="crypto-deposit-seed-title"
          >
            <div class="crypto-mnemonics__seed-heading">
              <div class="crypto-mnemonics__seed-title">
                <span class="crypto-mnemonics__step">01</span>
                <div>
                  <h4 id="crypto-deposit-seed-title">{{ t('admin.crypto.mnemonicsDepositTitle') }}</h4>
                  <p>{{ t('admin.crypto.mnemonicsDepositWarning') }}</p>
                </div>
              </div>
              <span class="crypto-mnemonics__kind">{{ t('admin.crypto.mnemonicsAllUsers') }}</span>
            </div>
            <div class="crypto-mnemonics__seed-tools">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                aria-controls="crypto-deposit-seed-part-1 crypto-deposit-seed-part-2"
                :aria-pressed="depositVisible"
                @click="depositVisible = !depositVisible"
              >
                <Icon :name="depositVisible ? 'eyeOff' : 'eye'" size="sm" />
                {{ t(depositVisible ? 'admin.crypto.mnemonicsHide' : 'admin.crypto.mnemonicsShow') }}
              </button>
              <span class="crypto-mnemonics__progress" :aria-label="t('admin.crypto.mnemonicsPartProgress', { copied: copiedCount('deposit'), total: 2 })">
                <span class="crypto-mnemonics__progress-track" aria-hidden="true">
                  <span :style="{ transform: `scaleX(${copiedCount('deposit') / 2})` }"></span>
                </span>
                {{ t('admin.crypto.mnemonicsPartProgress', { copied: copiedCount('deposit'), total: 2 }) }}
              </span>
            </div>
            <div class="crypto-mnemonics__parts">
              <section
                v-for="segment in mnemonicParts(depositMnemonic)"
                :key="`deposit-part-${segment.part}`"
                class="crypto-mnemonics__part"
                :class="{ 'crypto-mnemonics__part--copied': isCopied('deposit', segment.part) }"
                :style="{ '--part-order': segment.part - 1 }"
                :aria-label="partLabel('deposit', segment)"
              >
                <header class="crypto-mnemonics__part-heading">
                  <div>
                    <span class="crypto-mnemonics__part-index">{{ t('admin.crypto.mnemonicsPart', { part: segment.part }) }}</span>
                    <span class="crypto-mnemonics__part-range">{{ t('admin.crypto.mnemonicsWordRange', { start: wordNumber(segment.start), end: wordNumber(segment.start + segment.words.length - 1) }) }}</span>
                  </div>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm crypto-mnemonics__part-copy"
                    :data-copy-part="`deposit-${segment.part}`"
                    :aria-label="t('admin.crypto.mnemonicsCopyPart', { wallet: t('admin.crypto.depositMnemonicShort'), part: segment.part })"
                    @click="copyMnemonic('deposit', segment.part)"
                  >
                    <Icon :name="isCopied('deposit', segment.part) ? 'check' : 'clipboard'" size="sm" />
                    {{ t(isCopied('deposit', segment.part) ? 'admin.crypto.mnemonicsCopiedAction' : 'admin.crypto.mnemonicsCopyPartAction') }}
                  </button>
                </header>
                <ol
                  :id="`crypto-deposit-seed-part-${segment.part}`"
                  class="crypto-mnemonics__words"
                  :class="{ 'crypto-mnemonics__words--masked': !depositVisible }"
                  :aria-label="depositVisible ? partLabel('deposit', segment) : undefined"
                >
                  <template v-if="depositVisible">
                    <li v-for="(word, index) in segment.words" :key="`deposit-${segment.part}-${index}`">
                      <span>{{ wordNumber(segment.start + index) }}</span><strong>{{ word }}</strong>
                    </li>
                  </template>
                  <template v-else>
                    <li v-for="index in segment.words.length" :key="`deposit-${segment.part}-mask-${index}`">
                      <span>{{ wordNumber(segment.start + index - 1) }}</span><strong aria-hidden="true">••••</strong>
                    </li>
                  </template>
                </ol>
              </section>
            </div>
          </section>

          <section
            class="crypto-mnemonics__seed crypto-mnemonics__seed--hot"
            :class="{ 'crypto-mnemonics__seed--revealed': hotVisible }"
            aria-labelledby="crypto-hot-seed-title"
          >
            <div class="crypto-mnemonics__seed-heading">
              <div class="crypto-mnemonics__seed-title">
                <span class="crypto-mnemonics__step">02</span>
                <div>
                  <h4 id="crypto-hot-seed-title">{{ t('admin.crypto.hotMnemonic') }}</h4>
                  <p>{{ t('admin.crypto.mnemonicsHotWarning') }}</p>
                </div>
              </div>
              <span class="crypto-mnemonics__kind">{{ t('admin.crypto.mnemonicsGasOnly') }}</span>
            </div>
            <div class="crypto-mnemonics__seed-tools">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                aria-controls="crypto-hot-seed-part-1 crypto-hot-seed-part-2"
                :aria-pressed="hotVisible"
                @click="hotVisible = !hotVisible"
              >
                <Icon :name="hotVisible ? 'eyeOff' : 'eye'" size="sm" />
                {{ t(hotVisible ? 'admin.crypto.mnemonicsHide' : 'admin.crypto.mnemonicsShow') }}
              </button>
              <span class="crypto-mnemonics__progress" :aria-label="t('admin.crypto.mnemonicsPartProgress', { copied: copiedCount('hot'), total: 2 })">
                <span class="crypto-mnemonics__progress-track" aria-hidden="true">
                  <span :style="{ transform: `scaleX(${copiedCount('hot') / 2})` }"></span>
                </span>
                {{ t('admin.crypto.mnemonicsPartProgress', { copied: copiedCount('hot'), total: 2 }) }}
              </span>
            </div>
            <div class="crypto-mnemonics__parts">
              <section
                v-for="segment in mnemonicParts(hotMnemonic)"
                :key="`hot-part-${segment.part}`"
                class="crypto-mnemonics__part"
                :class="{ 'crypto-mnemonics__part--copied': isCopied('hot', segment.part) }"
                :style="{ '--part-order': segment.part - 1 }"
                :aria-label="partLabel('hot', segment)"
              >
                <header class="crypto-mnemonics__part-heading">
                  <div>
                    <span class="crypto-mnemonics__part-index">{{ t('admin.crypto.mnemonicsPart', { part: segment.part }) }}</span>
                    <span class="crypto-mnemonics__part-range">{{ t('admin.crypto.mnemonicsWordRange', { start: wordNumber(segment.start), end: wordNumber(segment.start + segment.words.length - 1) }) }}</span>
                  </div>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm crypto-mnemonics__part-copy"
                    :data-copy-part="`hot-${segment.part}`"
                    :aria-label="t('admin.crypto.mnemonicsCopyPart', { wallet: t('admin.crypto.hotMnemonicShort'), part: segment.part })"
                    @click="copyMnemonic('hot', segment.part)"
                  >
                    <Icon :name="isCopied('hot', segment.part) ? 'check' : 'clipboard'" size="sm" />
                    {{ t(isCopied('hot', segment.part) ? 'admin.crypto.mnemonicsCopiedAction' : 'admin.crypto.mnemonicsCopyPartAction') }}
                  </button>
                </header>
                <ol
                  :id="`crypto-hot-seed-part-${segment.part}`"
                  class="crypto-mnemonics__words"
                  :class="{ 'crypto-mnemonics__words--masked': !hotVisible }"
                  :aria-label="hotVisible ? partLabel('hot', segment) : undefined"
                >
                  <template v-if="hotVisible">
                    <li v-for="(word, index) in segment.words" :key="`hot-${segment.part}-${index}`">
                      <span>{{ wordNumber(segment.start + index) }}</span><strong>{{ word }}</strong>
                    </li>
                  </template>
                  <template v-else>
                    <li v-for="index in segment.words.length" :key="`hot-${segment.part}-mask-${index}`">
                      <span>{{ wordNumber(segment.start + index - 1) }}</span><strong aria-hidden="true">••••</strong>
                    </li>
                  </template>
                </ol>
              </section>
            </div>
          </section>
          <p v-if="copyNotice" role="status" class="crypto-mnemonics__notice">
            <Icon :name="copyFailed ? 'exclamationCircle' : 'checkCircle'" size="sm" />
            {{ copyNotice }}
          </p>
        </div>
      </Transition>
    </div>

    <template #footer>
      <div class="crypto-mnemonics__footer">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.cancel') }}</button>
        <button v-if="!hasMnemonics" form="crypto-mnemonics-form" type="submit" class="btn btn-danger" :disabled="busy || !productionConfirmed">
          <Icon v-if="!busy" name="lock" size="sm" />
          {{ busy ? t('common.processing') : t('admin.crypto.mnemonicsExportAction') }}
        </button>
        <button v-else type="button" class="btn btn-primary" @click="close">{{ t('common.close') }}</button>
      </div>
    </template>
    <CryptoSecurityDialog ref="securityDialog" />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
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
    await securityDialog.value?.verify(t('admin.crypto.mnemonicsRiskOperation'), async (code) => {
      let result: Awaited<ReturnType<typeof exportWalletMnemonics>> | undefined
      try {
        result = await exportWalletMnemonics({ totpCode: code })
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
.crypto-mnemonics {
  --mnemonic-ink: var(--signal-text, #172522);
  --mnemonic-muted: var(--signal-muted, #64716e);
  --mnemonic-line: var(--signal-line, #cbd5d1);
  --mnemonic-surface: var(--signal-surface, #fff);
  --mnemonic-deposit: #b45309;
  --mnemonic-gas: #0f766e;
  display: grid;
  color: var(--mnemonic-ink);
  font-size: .875rem;
  line-height: 1.65;
}
.crypto-mnemonics__intro, .crypto-mnemonics__results { display: grid; gap: 1.1rem; }
:global(.dark .crypto-mnemonics) {
  --mnemonic-ink: #e6efec;
  --mnemonic-muted: #9faeaa;
  --mnemonic-line: #3d4e48;
  --mnemonic-surface: #18241f;
  --mnemonic-deposit: #fbbf24;
  --mnemonic-gas: #5eead4;
}
.crypto-mnemonics__intro-head { display: grid; grid-template-columns: 2.75rem minmax(0, 1fr); align-items: start; gap: .85rem; }
.crypto-mnemonics__shield { display: grid; width: 2.75rem; aspect-ratio: 1; place-items: center; border: 1px solid #b4530940; border-radius: .375rem; background: #b4530910; color: var(--mnemonic-deposit); }
.crypto-mnemonics__eyebrow { margin: 0 0 .2rem; color: var(--mnemonic-deposit); font-size: .72rem; font-weight: 750; }
.crypto-mnemonics__lead { margin: 0; font-weight: 550; }
.crypto-mnemonics__risk-map { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border-block: 1px solid var(--mnemonic-line); }
.crypto-mnemonics__risk-row { display: grid; grid-template-columns: 2.1rem minmax(0, 1fr); gap: .75rem; padding: .9rem .9rem .9rem 0; }
.crypto-mnemonics__risk-row + .crypto-mnemonics__risk-row { border-inline-start: 1px solid var(--mnemonic-line); padding-inline: .9rem 0; }
.crypto-mnemonics__risk-row strong { display: block; font-size: .82rem; }
.crypto-mnemonics__risk-row p { margin: .22rem 0 0; color: var(--mnemonic-muted); font-size: .77rem; line-height: 1.5; }
.crypto-mnemonics__step { display: grid; width: 2rem; height: 2rem; flex: none; place-items: center; border: 1px solid currentColor; border-radius: .25rem; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: .68rem; font-weight: 700; }
.crypto-mnemonics__risk-row--deposit { color: var(--mnemonic-deposit); }
.crypto-mnemonics__risk-row--hot { color: var(--mnemonic-gas); }
.crypto-mnemonics__risk-row p { color: var(--mnemonic-muted); }
.crypto-mnemonics__form { display: grid; gap: .7rem; }
.crypto-mnemonics__form > label:first-child { font-size: .8rem; font-weight: 650; }
.crypto-mnemonics__confirm { display: flex; align-items: flex-start; gap: .65rem; padding-block: .2rem; color: var(--mnemonic-muted); cursor: pointer; }
.crypto-mnemonics__confirm input { margin-top: .28rem; accent-color: var(--mnemonic-deposit); }
.crypto-mnemonics__expired { display: flex; align-items: center; gap: .5rem; margin: 0; padding: .6rem .75rem; border-inline-start: 3px solid var(--mnemonic-gas); background: color-mix(in srgb, var(--mnemonic-gas) 8%, var(--mnemonic-surface)); color: var(--mnemonic-gas); font-size: .8rem; }
.crypto-mnemonics__session { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: .65rem; color: var(--mnemonic-gas); font-size: .8rem; font-variant-numeric: tabular-nums; }
.crypto-mnemonics__session-mark { display: inline-flex; }
.crypto-mnemonics__session-track { width: 5rem; height: .25rem; overflow: hidden; background: color-mix(in srgb, var(--mnemonic-gas) 16%, transparent); }
.crypto-mnemonics__session-track span { display: block; width: 100%; height: 100%; transform-origin: left center; background: var(--mnemonic-gas); transition: transform 500ms linear; }
.crypto-mnemonics__result-note { margin: 0; color: var(--mnemonic-muted); font-size: .8rem; }
.crypto-mnemonics__seed { --seed-accent: var(--mnemonic-deposit); display: grid; gap: .8rem; padding: 1rem; border: 1px solid var(--mnemonic-line); border-inline-start: 3px solid var(--seed-accent); border-radius: .375rem; background: var(--mnemonic-surface); transition: border-color 180ms ease, box-shadow 180ms ease, background-color 180ms ease; }
.crypto-mnemonics__seed--hot { --seed-accent: var(--mnemonic-gas); }
.crypto-mnemonics__seed--revealed { border-color: color-mix(in srgb, var(--seed-accent) 55%, var(--mnemonic-line)); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--seed-accent) 12%, transparent); }
.crypto-mnemonics__seed-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
.crypto-mnemonics__seed-title { display: flex; align-items: flex-start; gap: .7rem; min-width: 0; color: var(--seed-accent); }
.crypto-mnemonics__seed-title > div { min-width: 0; }
.crypto-mnemonics__seed-title h4 { margin: 0; color: var(--mnemonic-ink); font-size: .94rem; font-weight: 700; }
.crypto-mnemonics__seed-title p { max-width: 62ch; margin: .25rem 0 0; color: var(--mnemonic-muted); font-size: .76rem; line-height: 1.5; }
.crypto-mnemonics__kind { flex: none; padding: .2rem .45rem; border: 1px solid color-mix(in srgb, var(--seed-accent) 30%, transparent); color: var(--seed-accent); font-size: .68rem; font-weight: 650; line-height: 1.35; }
.crypto-mnemonics__seed-tools { display: flex; align-items: center; justify-content: space-between; gap: .8rem; }
.crypto-mnemonics__seed-tools button, .crypto-mnemonics__part-copy { display: inline-flex; align-items: center; justify-content: center; gap: .4rem; }
.crypto-mnemonics__progress { display: inline-flex; align-items: center; gap: .5rem; color: var(--mnemonic-muted); font-size: .72rem; font-variant-numeric: tabular-nums; }
.crypto-mnemonics__progress-track { width: 2.8rem; height: .22rem; overflow: hidden; background: color-mix(in srgb, var(--seed-accent) 15%, var(--mnemonic-surface)); }
.crypto-mnemonics__progress-track > span { display: block; width: 100%; height: 100%; transform-origin: left center; background: var(--seed-accent); transition: transform 260ms ease; }
.crypto-mnemonics__parts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .65rem; }
.crypto-mnemonics__part { display: grid; min-width: 0; align-content: start; gap: .6rem; padding: .7rem; border: 1px solid var(--mnemonic-line); border-radius: .25rem; background: color-mix(in srgb, var(--mnemonic-ink) 2%, var(--mnemonic-surface)); animation: mnemonic-part-arrive 360ms both; animation-delay: calc(var(--part-order, 0) * 80ms); transition: border-color 180ms ease, background-color 180ms ease; }
.crypto-mnemonics__part--copied { border-color: color-mix(in srgb, var(--seed-accent) 48%, var(--mnemonic-line)); background: color-mix(in srgb, var(--seed-accent) 5%, var(--mnemonic-surface)); }
.crypto-mnemonics__part-heading { display: flex; align-items: center; justify-content: space-between; gap: .5rem; min-width: 0; }
.crypto-mnemonics__part-heading > div { display: grid; gap: .12rem; min-width: 0; }
.crypto-mnemonics__part-index { color: var(--seed-accent); font-size: .74rem; font-weight: 750; }
.crypto-mnemonics__part-range { color: var(--mnemonic-muted); font-size: .67rem; font-variant-numeric: tabular-nums; }
.crypto-mnemonics__words { display: grid; grid-template-columns: repeat(auto-fit, minmax(4.7rem, 1fr)); gap: .3rem; margin: 0; padding: .5rem; border: 1px solid color-mix(in srgb, var(--mnemonic-line) 74%, transparent); border-radius: .2rem; background: var(--mnemonic-surface); list-style: none; }
.crypto-mnemonics__words li { display: flex; align-items: baseline; min-width: 0; gap: .42rem; padding: .25rem .35rem; border-bottom: 1px solid color-mix(in srgb, var(--mnemonic-line) 55%, transparent); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: .82rem; line-height: 1.4; }
.crypto-mnemonics__words li > span { color: var(--mnemonic-muted); font-size: .63rem; }
.crypto-mnemonics__words li > strong { min-width: 0; overflow-wrap: anywhere; font-weight: 600; }
.crypto-mnemonics__words--masked li > strong { color: var(--mnemonic-muted); letter-spacing: .08em; user-select: none; }
.crypto-mnemonics__footer { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: .6rem; }
.crypto-mnemonics__footer button { display: inline-flex; align-items: center; gap: .4rem; }
.crypto-mnemonics__notice { display: flex; align-items: flex-start; gap: .5rem; margin: 0; color: var(--mnemonic-gas); font-size: .8rem; }
.crypto-mnemonics__notice :deep(svg) { flex: none; margin-top: .1rem; }
.crypto-mnemonics__seed button:focus-visible { outline: 2px solid var(--seed-accent); outline-offset: 2px; }
.crypto-mnemonics__part-copy:active { transform: translateY(1px); }
.mnemonic-panel-enter-active, .mnemonic-panel-leave-active { transition: opacity 150ms ease, transform 150ms ease; }
.mnemonic-panel-enter-from { opacity: 0; transform: translateY(6px); }
.mnemonic-panel-leave-to { opacity: 0; transform: translateY(-4px); }
@keyframes mnemonic-part-arrive { from { opacity: 0; transform: translateY(5px); } to { opacity: 1; transform: translateY(0); } }
@media (max-width: 640px) {
  .crypto-mnemonics__risk-map { grid-template-columns: 1fr; }
  .crypto-mnemonics__risk-row { padding: .75rem 0; }
  .crypto-mnemonics__risk-row + .crypto-mnemonics__risk-row { border-inline-start: 0; border-top: 1px solid var(--mnemonic-line); padding-inline: 0; }
  .crypto-mnemonics__seed-heading { flex-direction: column; gap: .65rem; }
  .crypto-mnemonics__kind { margin-inline-start: 2.7rem; }
  .crypto-mnemonics__parts { grid-template-columns: 1fr; }
  .crypto-mnemonics__part-heading { align-items: flex-start; }
  .crypto-mnemonics__part-copy { flex: none; }
}
@media (prefers-reduced-motion: reduce) {
  .mnemonic-panel-enter-active, .mnemonic-panel-leave-active, .crypto-mnemonics__session-track span, .crypto-mnemonics__progress-track > span, .crypto-mnemonics__seed, .crypto-mnemonics__part { animation: none; transition: none; }
}
</style>
