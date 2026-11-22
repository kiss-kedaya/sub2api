<template>
  <BaseDialog
    :show="show"
    :title="mode === 'resplit' ? t('admin.crypto.signer.wizard.titleResplit') : t('admin.crypto.signer.wizard.titleSetup')"
    width="wide"
    :close-on-escape="!busy"
    :show-close-button="!busy"
    @close="requestClose"
  >
    <div class="signer-wizard space-y-4 text-sm" data-testid="signer-wizard">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <UBadge color="neutral" variant="subtle" data-testid="signer-wizard-step">{{ stepLabel }}</UBadge>
        <span class="text-xs text-muted">{{ t('admin.crypto.signer.backendLabel') }}: {{ backendLabel }}</span>
      </div>

      <!-- Blocking: environment cannot do the crypto at all. -->
      <UAlert
        v-if="cryptoUnavailableKey"
        color="error"
        variant="subtle"
        icon="i-lucide-shield-alert"
        :title="t('admin.crypto.signer.insecureTitle')"
        :description="t(cryptoUnavailableKey)"
        data-testid="signer-wizard-unavailable"
      />

      <p v-else-if="generating" role="status" class="flex items-center gap-2 text-muted" data-testid="signer-wizard-generating">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
        {{ t('admin.crypto.signer.wizard.generating') }}
      </p>

      <template v-else>
        <!-- Step: re-split first needs the old data key from two operator shares. -->
        <section v-if="step === 'recover'" class="space-y-4" data-testid="signer-wizard-recover">
          <div class="space-y-1">
            <h3 class="font-semibold text-highlighted">{{ t('admin.crypto.signer.wizard.recoverTitle') }}</h3>
            <p class="text-xs leading-relaxed text-muted">{{ t('admin.crypto.signer.wizard.recoverHint') }}</p>
            <p class="text-xs text-error">{{ t('admin.crypto.signer.wizard.resplitNotice') }}</p>
          </div>
          <ShareInputPanel
            v-model="oldShareB"
            :crypto="crypto"
            :label="t('admin.crypto.signer.wizard.recoverShareB')"
            input-id="signer-wizard-old-b"
            :disabled="busy"
          />
          <ShareInputPanel
            v-model="oldShareC"
            :crypto="crypto"
            :label="t('admin.crypto.signer.wizard.recoverShareC')"
            input-id="signer-wizard-old-c"
            :disabled="busy"
          />
          <UAlert v-if="recoverError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="recoverError" />
          <div class="flex justify-end">
            <UButton :disabled="busy || !oldShareB.trim() || !oldShareC.trim()" data-testid="signer-wizard-recover-action" @click="recoverOldKey">
              {{ busy ? t('common.loading') : t('admin.crypto.signer.wizard.recoverAction') }}
            </UButton>
          </div>
        </section>

        <!-- Step: fresh body TOTP (never the 5-minute grant). -->
        <section v-else-if="step === 'auth'" class="space-y-4" data-testid="signer-wizard-auth">
          <div class="space-y-1">
            <h3 class="font-semibold text-highlighted">{{ t('admin.crypto.signer.wizard.authTitle') }}</h3>
            <p class="text-xs leading-relaxed text-muted">{{ t('admin.crypto.signer.wizard.authHint') }}</p>
          </div>
          <TotpCodeInput
            id="signer-wizard-totp"
            v-model="totpCode"
            :label="t('admin.crypto.signer.wizard.authTitle')"
            :invalid="totpInvalid"
            :disabled="busy"
            :autofocus="false"
            @complete="onTotpComplete"
          />
          <UAlert v-if="authError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="authError" />
          <UAlert v-else-if="submitError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="submitError" />
          <div class="flex justify-end">
            <UButton :disabled="busy || totpCode.length !== 6" data-testid="signer-wizard-auth-next" @click="openRisk">
              {{ t('admin.crypto.signer.wizard.authAction') }}
            </UButton>
          </div>
        </section>

        <!-- Step: the one-time shares. Both are shown here and never again. -->
        <section v-else-if="step === 'reveal'" class="space-y-4" data-testid="signer-wizard-reveal">
          <UAlert color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('admin.crypto.signer.wizard.revealTitle')" :description="t('admin.crypto.signer.wizard.revealWarning')" />
          <ShardRevealCard
            v-if="shareB"
            :share-text="shareB"
            shard-slot="B"
            :title="t('admin.crypto.signer.wizard.revealB')"
            :description="t('admin.crypto.signer.wizard.revealWarning')"
            :created-at="generatedAt"
          />
          <ShardRevealCard
            v-if="shareC"
            :share-text="shareC"
            shard-slot="C"
            :title="t('admin.crypto.signer.wizard.revealC')"
            :description="t('admin.crypto.signer.wizard.revealWarning')"
            :created-at="generatedAt"
          />
          <UCheckbox v-model="acknowledged" :disabled="busy" :label="t('admin.crypto.signer.wizard.revealAck')" data-testid="signer-wizard-ack" />
          <div class="flex justify-end">
            <UButton :disabled="busy || !acknowledged" data-testid="signer-wizard-reveal-next" @click="goConfirm">
              {{ t('admin.crypto.signer.wizard.revealNext') }}
            </UButton>
          </div>
        </section>

        <!-- Step: type one share back; submission only happens after it matches. -->
        <section v-else-if="step === 'confirm'" class="space-y-4" data-testid="signer-wizard-confirm">
          <UAlert color="warning" variant="subtle" icon="i-lucide-clipboard-check" :title="t('admin.crypto.signer.wizard.confirmTitle')" :description="t('admin.crypto.signer.wizard.confirmHint')" />
          <ShareInputPanel
            v-model="backfillText"
            :crypto="crypto"
            :invalid="!!submitError"
            input-id="signer-wizard-backfill"
            :disabled="busy"
          />
          <div class="space-y-2">
            <label for="signer-wizard-submit-totp" class="block font-medium text-default">{{ t('admin.crypto.signer.wizard.submitTotpLabel') }}</label>
            <TotpCodeInput
              id="signer-wizard-submit-totp"
              v-model="submitTotpCode"
              :label="t('admin.crypto.signer.wizard.submitTotpLabel')"
              :invalid="!!submitError"
              :disabled="busy"
            />
            <p class="text-xs text-muted">{{ t('admin.crypto.signer.wizard.submitTotpHint') }}</p>
          </div>
          <UAlert v-if="submitError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="submitError" />
          <div class="flex justify-end">
            <UButton :disabled="busy || !backfillText.trim() || submitTotpCode.length !== 6" data-testid="signer-wizard-submit" @click="submit">
              {{ busy ? t('admin.crypto.signer.wizard.submitting') : t('admin.crypto.signer.wizard.confirmAction') }}
            </UButton>
          </div>
        </section>

        <!-- Step: done; the shares are already wiped. -->
        <section v-else-if="step === 'done'" class="space-y-4" data-testid="signer-wizard-done">
          <UAlert color="success" variant="subtle" icon="i-lucide-circle-check" :title="t('admin.crypto.signer.wizard.doneTitle')" :description="t('admin.crypto.signer.wizard.doneHint')" />
          <dl class="grid gap-2 text-sm sm:grid-cols-[10rem_minmax(0,1fr)]">
            <dt class="text-muted">{{ t('admin.crypto.signer.vaultVersion') }}</dt>
            <dd class="text-default">{{ doneStatus?.vault_version ?? '—' }}</dd>
            <dt class="text-muted">{{ t('admin.crypto.signer.scheme') }}</dt>
            <dd class="text-default">{{ doneStatus?.scheme ?? 'shamir-gf256' }} · {{ doneStatus?.threshold ?? 2 }}-of-{{ doneStatus?.total ?? 3 }}</dd>
            <dt class="text-muted">{{ t('admin.crypto.signer.createdAt') }}</dt>
            <dd class="text-default">{{ doneStatus?.created_at ?? generatedAt }}</dd>
          </dl>
          <p class="text-xs text-muted">{{ t('admin.crypto.signer.wizard.doneNotice') }}</p>
          <div class="flex justify-end">
            <UButton data-testid="signer-wizard-done-close" @click="closeNow">{{ t('admin.crypto.signer.wizard.doneClose') }}</UButton>
          </div>
        </section>
      </template>
    </div>
  </BaseDialog>

  <SignerRiskDialog
    :show="riskOpen"
    :phrase="riskPhrase"
    :points="riskPoints"
    :busy="generating"
    @confirm="onRiskConfirmed"
    @close="riskOpen = false"
  />

  <BaseDialog :show="discardOpen" :title="t('admin.crypto.signer.wizard.closeTitle')" @close="discardOpen = false">
    <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.crypto.signer.wizard.closeWarning')" />
    <template #footer>
      <UButton color="neutral" variant="subtle" data-testid="signer-wizard-stay" @click="discardOpen = false">{{ t('admin.crypto.signer.wizard.closeStay') }}</UButton>
      <UButton color="error" data-testid="signer-wizard-discard" @click="discardAndClose">{{ t('admin.crypto.signer.wizard.closeConfirm') }}</UButton>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import ShareInputPanel from '@/components/crypto/signer/ShareInputPanel.vue'
import ShardRevealCard from '@/components/crypto/signer/ShardRevealCard.vue'
import SignerRiskDialog from '@/components/crypto/signer/SignerRiskDialog.vue'
import {
  buildSignerSetupPayload,
  generateSignerShardSet,
  matchesShownShare,
  recoverDataKeyFromShares,
  shareTextForSlot,
  wipeSignerShardSet,
  type SignerShardSet
} from '@/utils/signer/material'
import { signerErrorI18nKey } from '@/utils/signer/errors'
import type { SignerCrypto, SignerCryptoUnavailableCode } from '@/utils/signer/cryptoProvider'
import type { SignerRiskNotice, SignerShardStatus } from '@/api/admin/cryptoSigner'
import { resplitSignerShards, setupSignerShards } from '@/api/admin/cryptoSigner'

const RISK_PHRASE_FALLBACK = {
  zh: '我已离线保存分片，丢失无法找回',
  en: 'I have saved my shards and understand they cannot be recovered'
}

type WizardStep = 'recover' | 'auth' | 'reveal' | 'confirm' | 'done'

const props = withDefaults(defineProps<{
  show: boolean
  mode?: 'setup' | 'resplit'
  notice?: SignerRiskNotice | null
  status?: SignerShardStatus | null
  crypto?: SignerCrypto | null
  cryptoUnavailable?: SignerCryptoUnavailableCode | null
}>(), {
  mode: 'setup',
  notice: null,
  status: null,
  crypto: null,
  cryptoUnavailable: null
})

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'completed', status: SignerShardStatus): void
}>()

const { t, locale } = useI18n()

/** Tolerate both a string locale (spec mocks) and the usual Ref<string>. */
function currentLanguage(): 'zh' | 'en' {
  const raw = locale as unknown
  const value = typeof raw === 'string' ? raw : (raw as { value?: unknown } | null)?.value
  return String(value ?? '').toLowerCase().startsWith('en') ? 'en' : 'zh'
}
const step = ref<WizardStep>('auth')
const totpCode = ref('')
// Writing down two shares easily outlives a 30s TOTP, so the commit step asks
// for a fresh code; the first code only gates the start of the flow.
const submitTotpCode = ref('')
const riskOpen = ref(false)
const generating = ref(false)
const busy = ref(false)
const acknowledged = ref(false)
const backfillText = ref('')
const shardSet = ref<SignerShardSet | null>(null)
const generatedAt = ref('')
const confirmedPhrase = ref('')
const submitError = ref('')
const authError = ref('')
const totpInvalid = ref(false)
const recoverError = ref('')
const doneStatus = ref<SignerShardStatus | null>(null)
const discardOpen = ref(false)
const oldShareB = ref('')
const oldShareC = ref('')
const recoveredKey = ref<Uint8Array | null>(null)

const language = computed<'zh' | 'en'>(() => currentLanguage())

const cryptoUnavailableKey = computed(() => {
  if (props.crypto) return ''
  return props.cryptoUnavailable === 'insecure-context'
    ? 'admin.crypto.signer.insecureContext'
    : props.cryptoUnavailable === 'no-csprng'
      ? 'admin.crypto.signer.noCsprng'
      : 'admin.crypto.signer.noAes'
})

const backendLabel = computed(() => (props.crypto?.backend === 'noble'
  ? t('admin.crypto.signer.noble')
  : t('admin.crypto.signer.webcrypto')))

const phraseFromNotice = computed(() => props.notice?.phrases?.[language.value] ?? '')
const riskPhrase = computed(() => phraseFromNotice.value || RISK_PHRASE_FALLBACK[language.value])
const riskPoints = computed(() => {
  const points = props.notice?.points?.[language.value]
  return Array.isArray(points) && points.length ? points : []
})

const shareB = computed(() => (shardSet.value ? shareTextForSlot(shardSet.value, 'B') : ''))
const shareC = computed(() => (shardSet.value ? shareTextForSlot(shardSet.value, 'C') : ''))

const stepOrder = computed<WizardStep[]>(() => (props.mode === 'resplit'
  ? ['recover', 'auth', 'reveal', 'confirm', 'done']
  : ['auth', 'reveal', 'confirm', 'done']))

const stepLabel = computed(() => {
  const order = stepOrder.value
  return t('admin.crypto.signer.wizard.progress', {
    current: Math.max(1, order.indexOf(step.value) + 1),
    total: order.length
  })
})

function resetState(): void {
  wipeSignerShardSet(shardSet.value)
  shardSet.value = null
  step.value = props.mode === 'resplit' ? 'recover' : 'auth'
  totpCode.value = ''
  submitTotpCode.value = ''
  riskOpen.value = false
  generating.value = false
  busy.value = false
  acknowledged.value = false
  backfillText.value = ''
  generatedAt.value = ''
  confirmedPhrase.value = ''
  submitError.value = ''
  authError.value = ''
  totpInvalid.value = false
  recoverError.value = ''
  doneStatus.value = null
  discardOpen.value = false
  oldShareB.value = ''
  oldShareC.value = ''
  if (recoveredKey.value) recoveredKey.value.fill(0)
  recoveredKey.value = null
}

watch(() => props.show, show => {
  if (show) resetState()
}, { immediate: true })

function onTotpComplete(): void {
  totpInvalid.value = false
  authError.value = ''
}

function openRisk(): void {
  if (totpCode.value.length !== 6) {
    authError.value = t('admin.crypto.signer.wizard.authRequired')
    return
  }
  if (!props.crypto) return
  authError.value = ''
  riskOpen.value = true
}

async function onRiskConfirmed(phrase: string): Promise<void> {
  riskOpen.value = false
  confirmedPhrase.value = phrase
  if (!props.crypto) return
  generating.value = true
  try {
    shardSet.value = await generateSignerShardSet(props.crypto, recoveredKey.value ? { dataKey: recoveredKey.value } : {})
    generatedAt.value = new Date().toLocaleString()
    step.value = 'reveal'
  } catch (error) {
    submitError.value = signerErrorI18nKey(error) ?? 'admin.crypto.signer.errors.generic'
  } finally {
    generating.value = false
  }
}

function goConfirm(): void {
  backfillText.value = ''
  submitTotpCode.value = ''
  submitError.value = ''
  step.value = 'confirm'
}

async function submit(): Promise<void> {
  if (!props.crypto || !shardSet.value || busy.value) return
  submitError.value = ''
  const typed = backfillText.value
  const matched = await matchesShownShare(props.crypto, typed, [shareB.value, shareC.value])
  if (matched === null) {
    submitError.value = t('admin.crypto.signer.wizard.confirmMismatch')
    return
  }
  busy.value = true
  let succeeded = false
  try {
    const payload = await buildSignerSetupPayload(props.crypto, {
      dataKey: shardSet.value.dataKey,
      shareA: shareTextForSlot(shardSet.value, 'A'),
      confirmShare: typed,
      commitment: shardSet.value.commitment,
      totpCode: submitTotpCode.value,
      riskPhrase: confirmedPhrase.value || riskPhrase.value
    })
    const status = props.mode === 'resplit'
      ? await resplitSignerShards(payload)
      : await setupSignerShards(payload)
    doneStatus.value = status
    step.value = 'done'
    succeeded = true
    emit('completed', status)
  } catch (error) {
    submitError.value = signerErrorI18nKey(error) ?? t('admin.crypto.signer.wizard.submitFailed')
    // A rejected code is already spent; ask for the next one on retry.
    submitTotpCode.value = ''
  } finally {
    busy.value = false
    // Keep the one-time material on screen when submission failed so the
    // operator can retry; wipe it the moment the server accepted the setup.
    if (succeeded) {
      wipeSignerShardSet(shardSet.value)
      shardSet.value = null
    }
    if (recoveredKey.value) recoveredKey.value.fill(0)
    recoveredKey.value = null
  }
}

async function recoverOldKey(): Promise<void> {
  if (!props.crypto || busy.value) return
  recoverError.value = ''
  if (oldShareB.value.trim() === oldShareC.value.trim()) {
    recoverError.value = t('admin.crypto.signer.wizard.recoverSame')
    return
  }
  const commitment = props.status?.dk_commitment
  if (!commitment) {
    recoverError.value = t('admin.crypto.signer.errors.notConfigured')
    return
  }
  busy.value = true
  try {
    recoveredKey.value = await recoverDataKeyFromShares(props.crypto, [oldShareB.value, oldShareC.value], commitment)
    step.value = 'auth'
    oldShareB.value = ''
    oldShareC.value = ''
  } catch (error) {
    recoverError.value = signerErrorI18nKey(error) ?? t('admin.crypto.signer.wizard.recoverMismatch')
    recoveredKey.value = null
  } finally {
    busy.value = false
  }
}

function hasUnsavedShares(): boolean {
  return step.value === 'reveal' || step.value === 'confirm'
}

function requestClose(): void {
  if (busy.value || generating.value) return
  if (hasUnsavedShares()) {
    discardOpen.value = true
    return
  }
  closeNow()
}

function discardAndClose(): void {
  discardOpen.value = false
  closeNow()
}

function closeNow(): void {
  if (busy.value) return
  wipeSignerShardSet(shardSet.value)
  shardSet.value = null
  if (recoveredKey.value) recoveredKey.value.fill(0)
  recoveredKey.value = null
  backfillText.value = ''
  totpCode.value = ''
  submitTotpCode.value = ''
  emit('close')
}

onBeforeUnmount(() => {
  wipeSignerShardSet(shardSet.value)
  if (recoveredKey.value) recoveredKey.value.fill(0)
})

defineExpose({ step, submit, recoverOldKey, requestClose, totpCode, submitTotpCode, backfillText, acknowledged })
</script>
