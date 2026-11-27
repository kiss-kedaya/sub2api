<template>
  <div class="space-y-6">
    <!-- Title + shield: the card answers "which account is being verified" first. -->
    <div class="text-center">
      <div class="icon-orb-primary mx-auto mb-3 flex size-12 items-center justify-center" aria-hidden="true">
        <UIcon name="i-lucide-shield-check" class="size-6" />
      </div>
      <h2 class="auth-heading">
        {{ t('auth.verifyYourEmail') }}
      </h2>
      <p class="auth-sub">
        {{ t('auth.sendCodeDesc') }}
        <span class="font-medium text-highlighted">{{ email }}</span>
      </p>
    </div>

    <!-- No Data Warning -->
    <div
      v-if="!hasRegisterData"
      class="rounded-xl border border-amber-200 bg-amber-50 p-4 dark:border-amber-800/50 dark:bg-amber-900/20"
    >
      <div class="flex items-start gap-3">
        <div class="flex-shrink-0">
          <Icon name="exclamationCircle" size="md" class="text-amber-500" />
        </div>
        <div class="text-sm text-amber-700 dark:text-amber-400">
          <p class="font-medium">{{ t('auth.sessionExpired') }}</p>
          <p class="mt-1">{{ t('auth.sessionExpiredDesc') }}</p>
        </div>
      </div>
    </div>

    <!-- Verification Form -->
    <component
      :is="embedded ? 'div' : 'form'"
      v-else
      class="space-y-5"
      @submit.prevent="handleVerify"
    >
      <!-- Verification Code Input: six segmented cells, auto-advance, auto-verify. -->
      <VerificationCodeInput
        v-model="verifyCode"
        input-id="code"
        :length="6"
        :disabled="isLoading"
        :invalid="Boolean(errors.code)"
        :label="t('auth.verificationCode')"
        :hint="t('auth.verificationCodeHint')"
        :auto-submit="canAutoSubmitCode"
        autocomplete="one-time-code"
        @complete="onCodeComplete"
        @submit="handleVerify"
      >
        <template #footer>
          <div class="flex flex-wrap items-center justify-center gap-x-1.5 text-xs text-muted">
            <span>{{ t('auth.didntReceiveCode') }}</span>
            <UButton
              v-if="countdown > 0"
              color="neutral"
              variant="link"
              size="xs"
              disabled
              :label="t('auth.resendCountdown', { countdown })"
              data-testid="resend-countdown"
            />
            <UButton
              v-else
              color="primary"
              variant="link"
              size="xs"
              :loading="isSendingCode"
              :disabled="isSendingCode || (turnstileEnabled && showResendTurnstile && !resendTurnstileToken)"
              :label="resendLabel"
              data-testid="resend-code"
              @click="handleResendCode"
            />
          </div>
        </template>
      </VerificationCodeInput>

      <!-- Code Status -->
      <div
        v-if="codeSent"
        class="rounded-xl border border-green-200 bg-green-50 p-4 dark:border-green-800/50 dark:bg-green-900/20"
      >
        <div class="flex items-start gap-3">
          <div class="flex-shrink-0">
            <Icon name="checkCircle" size="md" class="text-green-500" />
          </div>
          <p class="text-sm text-green-700 dark:text-green-400">
            {{ t('auth.codeSentSuccess') }}
          </p>
        </div>
      </div>

      <!-- Turnstile Widget for Resend -->
      <div v-if="actionCaptchaEnabled || (turnstileEnabled && showResendTurnstile)">
        <TurnstileWidget
          ref="turnstileRef"
          :site-key="turnstileSiteKey"
          :turnstile-enabled="turnstileEnabled"
          :turnstile-site-key="turnstileSiteKey"
          :tencent-enabled="tencentCaptchaEnabled"
          :tencent-app-id="tencentCaptchaAppId"
          :tencent-region="tencentCaptchaRegion"
          :aliyun-enabled="aliyunCaptchaEnabled"
          :aliyun-scene-id="aliyunCaptchaSceneId"
          :aliyun-prefix="aliyunCaptchaPrefix"
          :aliyun-region="aliyunCaptchaRegion"
          @verify="onTurnstileVerify"
          @expire="onTurnstileExpire"
          @error="onTurnstileError"
        />
      </div>

      <div v-if="pendingOAuthCreateCaptchaEnabled" class="space-y-2">
        <TurnstileWidget
          ref="createAccountTurnstileRef"
          :site-key="turnstileSiteKey"
          :turnstile-enabled="turnstileEnabled"
          :turnstile-site-key="turnstileSiteKey"
          :tencent-enabled="tencentCaptchaEnabled"
          :tencent-app-id="tencentCaptchaAppId"
          :tencent-region="tencentCaptchaRegion"
          :aliyun-enabled="aliyunCaptchaEnabled"
          :aliyun-scene-id="aliyunCaptchaSceneId"
          :aliyun-prefix="aliyunCaptchaPrefix"
          :aliyun-region="aliyunCaptchaRegion"
          @verify="onCreateAccountTurnstileVerify"
          @expire="onCreateAccountTurnstileExpire"
          @error="onCreateAccountTurnstileError"
        />
      </div>

      <!-- Submit Button (the code field also submits itself once all 6 digits are in) -->
      <UButton
        type="submit"
        block
        size="lg"
        icon="i-lucide-check-circle"
        :loading="isLoading"
        :disabled="isLoading || !verifyCode || (pendingOAuthCreateTurnstileRequired && !createAccountTurnstileToken)"
        :label="isLoading ? t('auth.verifying') : t('auth.verifyAndCreate')"
        data-testid="email-verify-submit"
      />
    </component>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/CaptchaChallenge.vue'
import VerificationCodeInput from '@/components/auth/VerificationCodeInput.vue'
import { useAuthStore, useAppStore } from '@/stores'
import {
  persistOAuthTokenContext,
  getPublicSettings,
  isOAuthLoginCompletion,
  type PendingOAuthSendVerifyCodeResponse,
  sendPendingOAuthVerifyCode,
  sendVerifyCode,
} from '@/api/auth'
import { apiClient } from '@/api/client'
import { buildAuthErrorMessage } from '@/utils/authError'
import { extractApiErrorCode } from '@/utils/apiError'
import {
  formatRegistrationEmailSuffixWhitelistForMessage,
  isRegistrationEmailFormatValid,
  isRegistrationEmailAlias,
  isRegistrationEmailSuffixAllowed,
  normalizeRegistrationEmailSuffixWhitelist
} from '@/utils/registrationEmailPolicy'
import {
  clearAllAffiliateReferralCodes,
  loadAffiliateReferralCode,
  oauthAffiliatePayload
} from '@/utils/oauthAffiliate'
import { sanitizeRedirectPath } from '@/utils/url'

const { t, locale } = useI18n()

// ==================== Router & Stores ====================

const router = useRouter()
const authStore = useAuthStore()
const appStore = useAppStore()

const props = withDefaults(
  defineProps<{
    /** Rendered inside the registration card instead of the standalone route. */
    embedded?: boolean
    /** Bumped by the host when the pending registration data changed and the code must be resent. */
    restartKey?: number
  }>(),
  { embedded: false, restartKey: 0 }
)

// ==================== State ====================

const isLoading = ref<boolean>(false)
const isSendingCode = ref<boolean>(false)
const errorMessage = ref<string>('')
const codeSent = ref<boolean>(false)
const verifyCode = ref<string>('')
const countdown = ref<number>(0)
let countdownTimer: ReturnType<typeof setInterval> | null = null

// Registration data from sessionStorage
type PendingAuthTokenField = 'pending_auth_token' | 'pending_oauth_token'
type PendingAuthSessionSummary = {
  token: string
  token_field: PendingAuthTokenField
  provider: string
  redirect?: string
}
type PendingOAuthCreateAccountResponse = {
  auth_result?: string
  access_token: string
  refresh_token?: string
  expires_in?: number
  token_type?: string
  provider?: string
  redirect?: string
}

const email = ref<string>('')
const password = ref<string>('')
const initialTurnstileToken = ref<string>('')
const initialTencentCaptchaRandstr = ref<string>('')
const promoCode = ref<string>('')
const invitationCode = ref<string>('')
const affCode = ref<string>('')
const pendingAuthToken = ref<string>('')
const pendingAuthTokenField = ref<PendingAuthTokenField>('pending_auth_token')
const pendingProvider = ref<string>('')
const pendingRedirect = ref<string>('')
const pendingAdoptionDecision = ref<{
  adoptDisplayName?: boolean
  adoptAvatar?: boolean
} | null>(null)
const hasRegisterData = ref<boolean>(false)

// Public settings
const turnstileEnabled = ref<boolean>(false)
const turnstileSiteKey = ref<string>('')
const tencentCaptchaEnabled = ref<boolean>(false)
const tencentCaptchaAppId = ref<string>('')
const tencentCaptchaRegion = ref<string>('cn')
const aliyunCaptchaEnabled = ref<boolean>(false)
const aliyunCaptchaSceneId = ref<string>('')
const aliyunCaptchaPrefix = ref<string>('')
const aliyunCaptchaRegion = ref<string>('cn')
const siteName = ref<string>('Sub2API')
const registrationEmailSuffixWhitelist = ref<string[]>([])
// 域名限量注册开关：开启时非白名单域名可注册 1 个账户（由后端判定），前端不做白名单预检。
const emailDomainQuotaEnabled = ref<boolean>(false)
const emailAliasBlockEnabled = ref<boolean>(false)

// Turnstile for resend
const turnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const createAccountTurnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const resendTurnstileToken = ref<string>('')
const resendTencentCaptchaRandstr = ref<string>('')
const createAccountTurnstileToken = ref<string>('')
const createAccountTencentCaptchaRandstr = ref<string>('')
const showResendTurnstile = ref<boolean>(false)
const aliyunCaptchaReady = computed(
  () =>
    aliyunCaptchaEnabled.value &&
    Boolean(aliyunCaptchaSceneId.value) &&
    Boolean(aliyunCaptchaPrefix.value)
)
// 动作触发式验证码（腾讯/阿里云）：重发验证码、创建账号时弹窗验证
const actionCaptchaEnabled = computed(
  () =>
    (tencentCaptchaEnabled.value && Boolean(tencentCaptchaAppId.value)) ||
    aliyunCaptchaReady.value
)
const captchaEnabled = computed(
  () =>
    (turnstileEnabled.value && Boolean(turnstileSiteKey.value)) || actionCaptchaEnabled.value
)

const errors = ref({
  code: '',
  turnstile: ''
})

const validationToastMessage = computed(
  () => errors.value.code || errors.value.turnstile || ''
)
const pendingOAuthCreateTurnstileRequired = computed(
  () => isPendingOAuthFlow() && turnstileEnabled.value
)
const pendingOAuthCreateCaptchaEnabled = computed(
  () => isPendingOAuthFlow() && captchaEnabled.value
)

// The segmented field verifies as soon as the sixth digit lands, but only when
// verification can run without popping a captcha challenge the user did not ask
// for: a pending-OAuth create-account captcha is acquired from the submit action.
const pendingCreateActionCaptcha = computed(() => isPendingOAuthFlow() && actionCaptchaEnabled.value)
const canAutoSubmitCode = computed(
  () =>
    !isLoading.value &&
    !pendingCreateActionCaptcha.value &&
    !(pendingOAuthCreateTurnstileRequired.value && !createAccountTurnstileToken.value)
)

// Resend label depends on the captcha mode: an action captcha needs one click
// first, the staged Turnstile needs its widget completed.
const resendLabel = computed(() => {
  if (isSendingCode.value) return t('auth.sendingCode')
  if (captchaEnabled.value && !showResendTurnstile.value) return t('auth.clickToResend')
  return t('auth.resendCode')
})

function onCodeComplete(): void {
  // Typing again after a failed verify clears the stale "code required" state.
  errors.value.code = ''
}

watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// ==================== Lifecycle ====================

async function loadRegistrationState(): Promise<void> {
  const activePendingSession = authStore.pendingAuthSession as PendingAuthSessionSummary | null

  // Load registration data from sessionStorage
  const registerDataStr = sessionStorage.getItem('register_data')
  if (registerDataStr) {
    try {
      const registerData = JSON.parse(registerDataStr)
      email.value = registerData.email || ''
      password.value = registerData.password || ''
      initialTurnstileToken.value =
        registerData.tencent_captcha_ticket || registerData.turnstile_token || ''
      initialTencentCaptchaRandstr.value = registerData.tencent_captcha_randstr || ''
      promoCode.value = registerData.promo_code || ''
      invitationCode.value = registerData.invitation_code || ''
      affCode.value = registerData.aff_code || loadAffiliateReferralCode()
      pendingAuthToken.value = registerData.pending_auth_token || activePendingSession?.token || ''
      pendingAuthTokenField.value =
        registerData.pending_auth_token_field ||
        activePendingSession?.token_field ||
        'pending_auth_token'
      pendingProvider.value =
        registerData.pending_provider || activePendingSession?.provider || ''
      pendingRedirect.value =
        registerData.pending_redirect || activePendingSession?.redirect || ''
      pendingAdoptionDecision.value = registerData.pending_adoption_decision
        ? {
            adoptDisplayName: registerData.pending_adoption_decision.adopt_display_name === true,
            adoptAvatar: registerData.pending_adoption_decision.adopt_avatar === true
          }
        : null
      hasRegisterData.value = !!(email.value && password.value)
    } catch {
      hasRegisterData.value = false
    }
  } else if (activePendingSession) {
    pendingAuthToken.value = activePendingSession.token
    pendingAuthTokenField.value = activePendingSession.token_field
    pendingProvider.value = activePendingSession.provider
    pendingRedirect.value = activePendingSession.redirect || ''
  }
}

async function loadPublicSettings(): Promise<void> {
  try {
    const settings = await getPublicSettings()
    turnstileEnabled.value = settings.turnstile_enabled
    turnstileSiteKey.value = settings.turnstile_site_key || ''
    tencentCaptchaEnabled.value = settings.tencent_captcha_enabled === true
    tencentCaptchaAppId.value = settings.tencent_captcha_app_id || ''
    tencentCaptchaRegion.value = settings.tencent_captcha_region || 'cn'
    aliyunCaptchaEnabled.value = settings.aliyun_captcha_enabled === true
    aliyunCaptchaSceneId.value = settings.aliyun_captcha_scene_id || ''
    aliyunCaptchaPrefix.value = settings.aliyun_captcha_prefix || ''
    aliyunCaptchaRegion.value = settings.aliyun_captcha_region || 'cn'
    siteName.value = settings.site_name || 'Sub2API'
    registrationEmailSuffixWhitelist.value = normalizeRegistrationEmailSuffixWhitelist(
      settings.registration_email_suffix_whitelist || []
    )
    emailDomainQuotaEnabled.value = settings.registration_email_domain_quota_enabled === true
    emailAliasBlockEnabled.value = settings.registration_email_alias_block_enabled === true
  } catch (error) {
    console.error('Failed to load public settings:', error)
  }
}

async function startVerification(): Promise<void> {
  await loadRegistrationState()
  await loadPublicSettings()
  if (hasRegisterData.value) {
    await sendCode()
  }
}

onMounted(startVerification)

// Embedded in RegisterView: the host bumps restartKey after the pending registration
// data changed (email/password/...), so resend the code for the new mailbox.
watch(
  () => props.restartKey,
  async (value, previous) => {
    if (value === previous) return
    stopCountdown()
    codeSent.value = false
    verifyCode.value = ''
    errors.value.code = ''
    errorMessage.value = ''
    await startVerification()
  }
)

onUnmounted(() => {
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
})

// ==================== Countdown ====================

function startCountdown(seconds: number): void {
  countdown.value = seconds

  if (countdownTimer) {
    clearInterval(countdownTimer)
  }

  countdownTimer = setInterval(() => {
    if (countdown.value > 0) {
      countdown.value--
    } else {
      if (countdownTimer) {
        clearInterval(countdownTimer)
        countdownTimer = null
      }
    }
  }, 1000)
}

function stopCountdown(): void {
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
  countdown.value = 0
}

// ==================== Turnstile Handlers ====================

function onTurnstileVerify(token: string, randstr = ''): void {
  resendTurnstileToken.value = token
  resendTencentCaptchaRandstr.value = randstr
  errors.value.turnstile = ''
}

function onTurnstileExpire(): void {
  resendTurnstileToken.value = ''
  resendTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileExpired')
}

function onTurnstileError(): void {
  resendTurnstileToken.value = ''
  resendTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileFailed')
}

function onCreateAccountTurnstileVerify(token: string, randstr = ''): void {
  createAccountTurnstileToken.value = token
  createAccountTencentCaptchaRandstr.value = randstr
  errors.value.turnstile = ''
}

function onCreateAccountTurnstileExpire(): void {
  createAccountTurnstileToken.value = ''
  createAccountTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileExpired')
}

function onCreateAccountTurnstileError(): void {
  createAccountTurnstileToken.value = ''
  createAccountTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileFailed')
}

function resetCreateAccountTurnstile(): void {
  createAccountTurnstileToken.value = ''
  createAccountTencentCaptchaRandstr.value = ''
  createAccountTurnstileRef.value?.reset()
}

async function acquireResendActionProof(): Promise<boolean> {
  if (!actionCaptchaEnabled.value) return true

  const proof = await turnstileRef.value?.verifyAction()
  if (!proof) return false

  resendTurnstileToken.value = proof.token
  resendTencentCaptchaRandstr.value = proof.randstr
  return true
}

async function acquireCreateAccountActionProof(): Promise<boolean> {
  if (!isPendingOAuthFlow() || !actionCaptchaEnabled.value) return true

  const proof = await createAccountTurnstileRef.value?.verifyAction()
  if (!proof) return false

  createAccountTurnstileToken.value = proof.token
  createAccountTencentCaptchaRandstr.value = proof.randstr
  return true
}

function isPendingOAuthFlow(): boolean {
  return Boolean(pendingProvider.value.trim())
}

// 域名限量注册开启时交由后端按额度判定；pending OAuth / 换绑流程沿用后端策略，前端不预检。
function shouldBypassRegistrationEmailPolicy(): boolean {
  return (
    emailDomainQuotaEnabled.value || isPendingOAuthFlow() || Boolean(pendingAuthToken.value.trim())
  )
}

function resolvePendingOAuthCallbackRoute(provider: string): string {
  switch (provider.trim().toLowerCase()) {
    case 'linuxdo':
      return '/auth/linuxdo/callback'
    case 'oidc':
      return '/auth/oidc/callback'
    case 'wechat':
      return '/auth/wechat/callback'
    default:
      return '/auth/callback'
  }
}

function isPendingOAuthSessionResponse(data: PendingOAuthCreateAccountResponse): boolean {
  return data.auth_result === 'pending_session'
}

function getPendingOAuthSendCodeSessionResponse(
  data: PendingOAuthSendVerifyCodeResponse,
): PendingOAuthSendVerifyCodeResponse | null {
  return data.auth_result === 'pending_session' ? data : null
}

function persistPendingOAuthSession(provider: string, redirect?: string): void {
  authStore.setPendingAuthSession({
    token: pendingAuthToken.value,
    token_field: pendingAuthTokenField.value,
    provider: provider.trim() || pendingProvider.value.trim(),
    redirect: redirect || pendingRedirect.value || undefined,
  })
}

// ==================== Send Code ====================

async function sendCode(): Promise<void> {
  isSendingCode.value = true
  errorMessage.value = ''
  let requestSucceeded = false
  let captchaProofUsed = false

  try {
    if (!isPendingOAuthFlow() && !pendingAuthToken.value.trim() && emailAliasBlockEnabled.value && isRegistrationEmailAlias(email.value)) {
      errorMessage.value = t('auth.emailAliasNotAllowed')
      appStore.showError(errorMessage.value)
      return
    }

    if (!isPendingOAuthFlow() && !pendingAuthToken.value.trim() && emailAliasBlockEnabled.value && isRegistrationEmailAlias(email.value)) {
    errorMessage.value = t('auth.emailAliasNotAllowed')
    appStore.showError(errorMessage.value)
    return
  }

  if (!shouldBypassRegistrationEmailPolicy() && !isRegistrationEmailFormatValid(email.value)) {
      errorMessage.value = t('auth.emailFormatInvalid')
      appStore.showError(errorMessage.value)
      return
    }

    if (!shouldBypassRegistrationEmailPolicy() && !isRegistrationEmailSuffixAllowed(email.value, registrationEmailSuffixWhitelist.value)) {
      errorMessage.value = buildEmailSuffixNotAllowedMessage()
      appStore.showError(errorMessage.value)
      return
    }

    const requestPayload = {
      email: email.value,
      [pendingAuthTokenField.value]: pendingAuthToken.value || undefined,
      // 优先使用重发时新获取的 token（因为初始 token 可能已被使用）
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value
          ? resendTurnstileToken.value || initialTurnstileToken.value || undefined
          : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value
        ? resendTurnstileToken.value || initialTurnstileToken.value || undefined
        : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? resendTencentCaptchaRandstr.value || initialTencentCaptchaRandstr.value || undefined
        : undefined
    } as Parameters<typeof sendVerifyCode>[0]
    captchaProofUsed = Boolean(
      requestPayload.turnstile_token || requestPayload.tencent_captcha_ticket
    )
    const response = isPendingOAuthFlow()
      ? await sendPendingOAuthVerifyCode(requestPayload)
      : await sendVerifyCode(requestPayload)
    requestSucceeded = true

    const pendingSendCodeSession = isPendingOAuthFlow()
      ? getPendingOAuthSendCodeSessionResponse(response as PendingOAuthSendVerifyCodeResponse)
      : null
    if (pendingSendCodeSession) {
      sessionStorage.removeItem('register_data')
      persistPendingOAuthSession(
        pendingSendCodeSession.provider || pendingProvider.value,
        pendingSendCodeSession.redirect,
      )
      await router.push(
        resolvePendingOAuthCallbackRoute(pendingSendCodeSession.provider || pendingProvider.value),
      )
      return
    }

    codeSent.value = true
    startCountdown(response.countdown)

    showResendTurnstile.value = false
  } catch (error: unknown) {
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.sendCodeFailed'))

    appStore.showError(errorMessage.value)
  } finally {
    if (captchaProofUsed) {
      clearStoredCaptchaProof()
      initialTurnstileToken.value = ''
      initialTencentCaptchaRandstr.value = ''
      resendTurnstileToken.value = ''
      resendTencentCaptchaRandstr.value = ''
      turnstileRef.value?.reset()
      if (!requestSucceeded && turnstileEnabled.value) {
        showResendTurnstile.value = true
      }
    }
    isSendingCode.value = false
  }
}

function clearStoredCaptchaProof(): void {
  const registerDataStr = sessionStorage.getItem('register_data')
  if (!registerDataStr) return

  try {
    const registerData = JSON.parse(registerDataStr) as Record<string, unknown>
    delete registerData.turnstile_token
    delete registerData.tencent_captcha_ticket
    delete registerData.tencent_captcha_randstr
    sessionStorage.setItem('register_data', JSON.stringify(registerData))
  } catch {
    // Invalid registration state is handled by the existing onMounted parser.
  }
}

// ==================== Handlers ====================

async function handleResendCode(): Promise<void> {
  // Turnstile stays staged; Tencent is acquired from this action.
  if (turnstileEnabled.value && !showResendTurnstile.value) {
    showResendTurnstile.value = true
    return
  }

  if (turnstileEnabled.value && !resendTurnstileToken.value) {
    errors.value.turnstile = t('auth.completeVerification')
    return
  }

  if (!(await acquireResendActionProof())) {
    return
  }

  await sendCode()
}

function validateForm(): boolean {
  errors.value.code = ''

  if (!verifyCode.value.trim()) {
    errors.value.code = t('auth.codeRequired')
    return false
  }

  if (!/^\d{6}$/.test(verifyCode.value.trim())) {
    errors.value.code = t('auth.invalidCode')
    return false
  }

  return true
}

async function handleVerify(): Promise<void> {
  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  if (!shouldBypassRegistrationEmailPolicy() && !isRegistrationEmailFormatValid(email.value)) {
    errorMessage.value = t('auth.emailFormatInvalid')
    appStore.showError(errorMessage.value)
    return
  }

  if (!shouldBypassRegistrationEmailPolicy() && !isRegistrationEmailSuffixAllowed(email.value, registrationEmailSuffixWhitelist.value)) {
    errorMessage.value = buildEmailSuffixNotAllowedMessage()
    appStore.showError(errorMessage.value)
    return
  }

  if (!(await acquireCreateAccountActionProof())) {
    return
  }

  isLoading.value = true

  try {
    if (isPendingOAuthFlow()) {
      const payload: Record<string, unknown> = {
        email: email.value,
        password: password.value,
        verify_code: verifyCode.value.trim(),
        ...((turnstileEnabled.value || aliyunCaptchaEnabled.value) &&
        createAccountTurnstileToken.value
          ? { turnstile_token: createAccountTurnstileToken.value }
          : {}),
        ...(tencentCaptchaEnabled.value && createAccountTurnstileToken.value
          ? {
              tencent_captcha_ticket: createAccountTurnstileToken.value,
              tencent_captcha_randstr: createAccountTencentCaptchaRandstr.value
          }
          : {}),
        ...oauthAffiliatePayload(affCode.value || loadAffiliateReferralCode()),
      }
      if (invitationCode.value) {
        payload.invitation_code = invitationCode.value
      }
      if (pendingAdoptionDecision.value?.adoptDisplayName !== undefined) {
        payload.adopt_display_name = pendingAdoptionDecision.value.adoptDisplayName
      }
      if (pendingAdoptionDecision.value?.adoptAvatar !== undefined) {
        payload.adopt_avatar = pendingAdoptionDecision.value.adoptAvatar
      }

      const { data } = await apiClient.post<PendingOAuthCreateAccountResponse>(
        '/auth/oauth/pending/create-account',
        payload
      )
      if (isPendingOAuthSessionResponse(data)) {
        sessionStorage.removeItem('register_data')
        persistPendingOAuthSession(data.provider || pendingProvider.value, data.redirect)
        await router.push(resolvePendingOAuthCallbackRoute(data.provider || pendingProvider.value))
        return
      }
      if (!isOAuthLoginCompletion(data)) {
        throw new Error(t('auth.verifyFailed'))
      }

      persistOAuthTokenContext(data)
      await authStore.setToken(data.access_token)
      authStore.clearPendingAuthSession?.()
    } else {
      // Register with verification code
      await authStore.register({
        email: email.value,
        password: password.value,
        verify_code: verifyCode.value.trim(),
        turnstile_token:
          turnstileEnabled.value || aliyunCaptchaEnabled.value
            ? initialTurnstileToken.value || undefined
            : undefined,
        tencent_captcha_ticket: tencentCaptchaEnabled.value ? initialTurnstileToken.value || undefined : undefined,
        tencent_captcha_randstr: tencentCaptchaEnabled.value ? initialTencentCaptchaRandstr.value || undefined : undefined,
        promo_code: promoCode.value || undefined,
        invitation_code: invitationCode.value || undefined,
        ...(affCode.value ? { aff_code: affCode.value } : {})
      })
    }

    // Clear session data
    sessionStorage.removeItem('register_data')
    clearAllAffiliateReferralCodes()

    // Show success toast
    appStore.showSuccess(t('auth.accountCreatedSuccess', { siteName: siteName.value }))

    // Redirect to intended page or dashboard
    await router.push(sanitizeRedirectPath(pendingRedirect.value))
  } catch (error: unknown) {
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.verifyFailed'))

    appStore.showError(errorMessage.value)
  } finally {
    initialTurnstileToken.value = ''
    initialTencentCaptchaRandstr.value = ''
    if (pendingOAuthCreateCaptchaEnabled.value) {
      resetCreateAccountTurnstile()
    }
    isLoading.value = false
  }
}

// The host card owns the outer <form>, so Enter/submit delegates to this method.
defineExpose({ submit: handleVerify })

function buildEmailSuffixNotAllowedMessage(): string {
  const normalizedWhitelist = normalizeRegistrationEmailSuffixWhitelist(
    registrationEmailSuffixWhitelist.value
  )
  if (normalizedWhitelist.length === 0) {
    return t('auth.emailSuffixNotAllowed')
  }
  const separator = String(locale.value || '').toLowerCase().startsWith('zh') ? '、' : ', '
  return t('auth.emailSuffixNotAllowedWithAllowed', {
    suffixes: formatRegistrationEmailSuffixWhitelistForMessage(normalizedWhitelist, {
      separator,
      more: (count) => t('auth.emailSuffixAllowedMore', { count })
    })
  })
}

function buildRegistrationErrorMessage(error: unknown, fallback: string): string {
  if (extractApiErrorCode(error) === 'EMAIL_FORMAT_INVALID') {
    return t('auth.emailFormatInvalid')
  }
  if (extractApiErrorCode(error) === 'EMAIL_DOMAIN_REGISTRATION_LIMIT') {
    return t('auth.emailDomainRegistrationLimit')
  }
  if (extractApiErrorCode(error) === 'EMAIL_BLOCKED') {
    return t('auth.errors.EMAIL_BLOCKED')
  }
  return buildAuthErrorMessage(error, { fallback })
}
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.3s ease, transform 0.3s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
