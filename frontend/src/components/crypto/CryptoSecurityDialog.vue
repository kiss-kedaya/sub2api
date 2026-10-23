<template>
  <BaseDialog
    :show="show"
    :title="t('admin.crypto.securityTitle')"
    :close-on-escape="!busy"
    :show-close-button="!busy"
    @close="cancel"
  >
    <div class="crypto-security">
      <p class="crypto-security__operation">{{ label }}</p>
      <p class="crypto-security__hint">{{ t('admin.crypto.securityPerRequest') }}</p>
      <p v-if="stage === 'loading'" role="status">{{ t('common.loading') }}</p>
      <form v-else-if="stage === 'enroll'" id="crypto-security-form" autocomplete="off" @submit.prevent="setup">
        <p>{{ t('admin.crypto.securitySetupIntro') }}</p>
      </form>
      <form v-else-if="stage === 'setup' || stage === 'verify'" id="crypto-security-form" autocomplete="off" @submit.prevent="submitCode">
        <template v-if="stage === 'setup'">
          <p>{{ t('admin.crypto.securityScan') }}</p>
          <img v-if="qrImage" :src="qrImage" :alt="t('admin.crypto.securityQrAlt')" width="224" height="224" class="crypto-security__qr" />
          <p v-if="qrFailed" role="status">{{ t('admin.crypto.securityQrFailed') }}</p>
          <label for="crypto-security-key">{{ t('admin.crypto.securityOfflineKey') }}</label>
          <input id="crypto-security-key" :value="secret" class="input crypto-security__key" readonly autocomplete="off" spellcheck="false" />
          <p class="crypto-security__hint">{{ t('admin.crypto.securityKeyHint') }}</p>
          <p role="timer">{{ t('admin.crypto.securityCountdown', { seconds: countdown }) }}</p>
        </template>
        <label for="crypto-security-code">{{ t('admin.crypto.securityCode') }}</label>
        <TotpCodeInput
          id="crypto-security-code"
          ref="codeInput"
          v-model="code"
          :label="t('admin.crypto.securityCode')"
          :invalid="!!errorMessage"
          aria-describedby="crypto-security-code-hint"
          :disabled="busy"
          required
        />
        <p id="crypto-security-code-hint" class="crypto-security__hint">{{ t('admin.crypto.securityFreshCode') }}</p>
      </form>
      <p v-else-if="stage === 'done'" role="status">{{ t('admin.crypto.securityBound') }}</p>
      <p v-else-if="stage === 'read-only'" role="alert">{{ t('admin.crypto.securityReadOnly') }}</p>
      <p v-else-if="stage === 'expired'" role="alert">{{ t('admin.crypto.securitySetupExpired') }}</p>
      <p v-else-if="stage === 'unavailable'" role="alert">{{ t('admin.crypto.securityUnavailable') }}</p>
      <p v-if="errorMessage" role="alert" class="crypto-security__error">{{ errorMessage }}</p>
    </div>
    <template #footer>
      <div class="crypto-security__actions">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="cancel">{{ t('common.cancel') }}</button>
        <button v-if="stage === 'enroll'" form="crypto-security-form" type="submit" class="btn btn-primary" :disabled="busy">
          {{ t('admin.crypto.securitySetupAction') }}
        </button>
        <button v-if="stage === 'setup' || stage === 'verify'" form="crypto-security-form" type="submit" class="btn btn-danger" :disabled="busy || !validCode">
          {{ stage === 'setup' ? t('admin.crypto.securityBindAction') : t('admin.crypto.securityVerifyAction') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import { cryptoSecurityErrorKey, enableCryptoSecurity, getCryptoSecurityStatus, setupCryptoSecurity } from '@/api/admin/crypto'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const show = ref(false)
const busy = ref(false)
const label = ref('')
const stage = ref<'loading' | 'enroll' | 'setup' | 'verify' | 'done' | 'expired' | 'read-only' | 'unavailable'>('loading')
const secret = ref('')
const setupToken = ref('')
const qrImage = ref('')
const qrFailed = ref(false)
const countdown = ref(0)
const code = ref('')
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)
const errorMessage = ref('')
const validCode = computed(() => /^[0-9]{6}$/.test(code.value))
let callback: ((code: string) => Promise<unknown>) | undefined
let resolveVerification: ((completed: boolean) => void) | undefined
let timer: ReturnType<typeof setInterval> | undefined
let generation = 0

function clearSecrets() {
  code.value = ''
  secret.value = ''
  setupToken.value = ''
  qrImage.value = ''
  countdown.value = 0
  if (timer) clearInterval(timer)
  timer = undefined
}

function settle(completed: boolean) {
  callback = undefined
  resolveVerification?.(completed)
  resolveVerification = undefined
}

function cancel() {
  if (busy.value) return
  generation++
  show.value = false
  clearSecrets()
  errorMessage.value = ''
  label.value = ''
  settle(false)
}

function forceClose() {
  generation++
  show.value = false
  busy.value = false
  clearSecrets()
  errorMessage.value = ''
  label.value = ''
  settle(false)
}

function reportError(error: unknown) {
  const key = cryptoSecurityErrorKey(error)
  errorMessage.value = key ? t(`admin.crypto.${key}`) : extractApiErrorMessage(error, t('common.error'))
}

async function verify(operation: string, action: (code: string) => Promise<unknown>): Promise<boolean> {
  if (show.value) return false
  clearSecrets()
  errorMessage.value = ''
  qrFailed.value = false
  label.value = operation
  stage.value = 'loading'
  show.value = true
  callback = action
  const completed = new Promise<boolean>((resolve) => { resolveVerification = resolve })
  const current = ++generation
  try {
    const status = await getCryptoSecurityStatus()
    if (current !== generation) return completed
    // Fail closed if the feature or setup is unavailable. This is a UI gate;
    // authorization and replay prevention remain the server's responsibility.
    stage.value = !status.feature_enabled ? 'unavailable' : status.enabled ? 'verify' : status.read_only ? 'read-only' : status.can_setup ? 'enroll' : 'unavailable'
    if (stage.value === 'verify') {
      await nextTick()
      codeInput.value?.focus()
    }
  } catch (error) {
    if (current === generation) { stage.value = 'unavailable'; reportError(error) }
  }
  return completed
}

async function setup() {
  if (busy.value || stage.value !== 'enroll') return
  busy.value = true
  errorMessage.value = ''
  const current = generation
  let result: Awaited<ReturnType<typeof setupCryptoSecurity>> | undefined
  let qrCodeUrl = ''
  let image = ''
  try {
    result = await setupCryptoSecurity()
    if (current !== generation) return
    secret.value = result.secret
    setupToken.value = result.setup_token
    qrCodeUrl = result.qr_code_url
    result.secret = ''
    result.setup_token = ''
    result.qr_code_url = ''
    const expiresAt = Date.now() + Math.max(0, result.countdown) * 1000
    const tick = () => {
      countdown.value = Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000))
      if (!countdown.value) { clearSecrets(); stage.value = 'expired' }
    }
    stage.value = 'setup'
    tick()
    if (stage.value !== 'setup') return
    timer = setInterval(tick, 1000)
    // qr_code_url is an otpauth URI, never a remote image or QR service URL.
    try {
      if (!qrCodeUrl.startsWith('otpauth://totp/')) throw new Error('Invalid otpauth URI')
      image = await QRCode.toDataURL(qrCodeUrl, { width: 224, margin: 2 })
      if (current === generation && stage.value === 'setup') qrImage.value = image
    } catch { if (current === generation) qrFailed.value = true }
  } catch (error) {
    if (current === generation) reportError(error)
  } finally {
    if (result) {
      result.secret = ''
      result.setup_token = ''
      result.qr_code_url = ''
    }
    qrCodeUrl = ''
    image = ''
    if (current === generation) busy.value = false
    if (current === generation && stage.value === 'setup') {
      await nextTick()
      codeInput.value?.focus()
    }
  }
}

async function submitCode() {
  if (busy.value || !validCode.value || (stage.value !== 'setup' && stage.value !== 'verify')) return
  busy.value = true
  errorMessage.value = ''
  let submittedCode = code.value
  code.value = ''
  const current = generation
  try {
    if (stage.value === 'setup') {
      const result = await enableCryptoSecurity(submittedCode, setupToken.value)
      if (!result.success) throw new Error(t('common.error'))
      if (current !== generation) return
      clearSecrets()
      stage.value = 'done'
      // Binding consumes its code. Discard the original operation, even if it
      // was a sweep/import. The user must start it again with a NEW code.
      settle(false)
    } else {
      await callback?.(submittedCode)
      if (current !== generation) return
      clearSecrets()
      show.value = false
      settle(true)
    }
  } catch (error) {
    if (current === generation) reportError(error)
  } finally {
    submittedCode = ''
    code.value = ''
    if (current === generation) busy.value = false
    if (current === generation && show.value && (stage.value === 'setup' || stage.value === 'verify')) {
      await nextTick()
      codeInput.value?.focus()
    }
  }
}

onUnmounted(() => { generation++; clearSecrets(); settle(false) })
defineExpose({ verify, cancel, forceClose })
</script>

<style scoped>
.crypto-security { display: grid; gap: 1rem; font-size: .875rem; line-height: 1.7; }
.crypto-security form { display: grid; gap: .75rem; }
.crypto-security__operation { white-space: pre-line; overflow-wrap: anywhere; font-weight: 600; }
.crypto-security__hint { color: var(--signal-muted, #6b7280); font-size: .8125rem; }
.crypto-security__qr { justify-self: center; border-radius: .5rem; }
.crypto-security__key { font-family: ui-monospace, monospace; }
.crypto-security__error { color: #dc2626; }
.crypto-security__actions { display: flex; justify-content: flex-end; gap: .75rem; }
</style>
