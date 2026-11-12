<template>
  <AuthLayout>
    <div class="space-y-6">
      <!-- Title -->
      <div>
        <h2 class="auth-heading">
          {{ t('auth.createAccount') }}
        </h2>
        <p class="auth-sub">
          {{ t('auth.signUpToStart', { siteName }) }}
        </p>
      </div>

      <!-- Registration Disabled Message -->
      <div
        v-if="!registrationEnabled && settingsLoaded"
        class="rounded-xl border border-amber-200 bg-amber-50 p-4 dark:border-amber-800/50 dark:bg-amber-900/20"
      >
        <div class="flex items-start gap-3">
          <div class="flex-shrink-0">
            <Icon name="exclamationCircle" size="md" class="text-amber-500" />
          </div>
          <p class="text-sm text-amber-700 dark:text-amber-400">
            {{ t('auth.registrationDisabled') }}
          </p>
        </div>
      </div>

      <!-- Registration Form -->
      <form v-else @submit.prevent="handleRegister" class="space-y-5">
        <!-- Step head: "Step N of M" on the left, dotted progress on the right -->
        <div v-if="totalSteps > 1" class="auth-step-head" data-testid="register-step-head">
          <span class="auth-step-label">{{ stepLabel }}</span>
          <span
            aria-hidden="true"
            class="auth-step-dots"
            :style="{ width: `${24 * (totalSteps - 1) + 18}px` }"
          >
            <span
              v-for="dot in totalSteps"
              :key="dot"
              class="auth-step-dot"
              :style="{ left: `${(dot - 1) * 24}px` }"
            ></span>
            <span
              class="auth-step-fill"
              :style="{ transform: `translateX(${stepIndex * 24}px)` }"
            ></span>
          </span>
        </div>

        <!-- Step 1: account. Panes stay mounted so field values survive navigation. -->
        <div class="auth-step-pane" :data-pane-state="paneState('account')">
        <!-- Email Input -->
        <div>
          <label for="email" class="input-label">
            {{ t('auth.emailLabel') }}
          </label>
          <div class="relative">
            <input
              id="email"
              v-model="formData.email"
              type="email"
              maxlength="254"
              required
              autofocus
              autocomplete="email"
              :disabled="registrationActionDisabled"
              class="input"
              :class="{ 'input-error': errors.email }"
              :placeholder="t('auth.emailPlaceholder')"
            />
          </div>
        </div>

        <!-- Password Input -->
        <div>
          <label for="password" class="input-label">
            {{ t('auth.passwordLabel') }}
          </label>
          <div class="relative">
            <input
              id="password"
              v-model="formData.password"
              :type="showPassword ? 'text' : 'password'"
              required
              autocomplete="new-password"
              :disabled="registrationActionDisabled"
              class="input pr-10"
              :class="{ 'input-error': errors.password }"
              :placeholder="t('auth.createPasswordPlaceholder')"
            />
            <button
              type="button"
              :disabled="registrationActionDisabled"
              @click="showPassword = !showPassword"
              class="absolute inset-y-0 right-0 flex items-center pr-3 text-dimmed transition-colors hover:text-default"
            >
              <Icon v-if="showPassword" name="eyeOff" size="md" />
              <Icon v-else name="eye" size="md" />
            </button>
          </div>
          <p class="input-hint">
            {{ t('auth.passwordHint') }}
          </p>
        </div>

        </div>

        <!-- Step 2: invitation / promo, only when one of those fields exists -->
        <div v-if="hasDetailsStep" class="auth-step-pane" :data-pane-state="paneState('details')">
        <!-- Invitation Code Input (Required when enabled) -->
        <div v-if="invitationCodeEnabled">
          <label for="invitation_code" class="input-label">
            {{ t('auth.invitationCodeLabel') }}
          </label>
          <div class="relative">
            <input
              id="invitation_code"
              v-model="formData.invitation_code"
              type="text"
              :disabled="registrationActionDisabled"
              class="input pr-10"
              :class="{
                'border-green-500 focus:border-green-500 focus:ring-green-500': invitationValidation.valid,
                'border-red-500 focus:border-red-500 focus:ring-red-500': invitationValidation.invalid || errors.invitation_code
              }"
              :placeholder="t('auth.invitationCodePlaceholder')"
              @input="handleInvitationCodeInput"
            />
            <!-- Validation indicator -->
            <div v-if="invitationValidating" class="absolute inset-y-0 right-0 flex items-center pr-3.5">
              <svg class="h-4 w-4 animate-spin text-gray-400" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
              </svg>
            </div>
            <div v-else-if="invitationValidation.valid" class="absolute inset-y-0 right-0 flex items-center pr-3.5">
              <Icon name="checkCircle" size="md" class="text-green-500" />
            </div>
            <div v-else-if="invitationValidation.invalid || errors.invitation_code" class="absolute inset-y-0 right-0 flex items-center pr-3.5">
              <Icon name="exclamationCircle" size="md" class="text-red-500" />
            </div>
          </div>
          <!-- Invitation code validation result -->
          <transition name="fade">
            <div v-if="invitationValidation.valid" class="mt-2 flex items-center gap-2 rounded-lg bg-green-50 px-3 py-2 dark:bg-green-900/20">
              <Icon name="checkCircle" size="sm" class="text-green-600 dark:text-green-400" />
              <span class="text-sm text-green-700 dark:text-green-400">
                {{ t('auth.invitationCodeValid') }}
              </span>
            </div>
          </transition>
        </div>

        <!-- Affiliate Invitation Code Input (Optional) -->
        <div v-else-if="affiliateEnabled" data-testid="affiliate-invitation-field">
          <label for="affiliate_code" class="input-label">
            {{ t('auth.invitationCodeLabel') }}
            <span class="ml-1 text-xs font-normal text-gray-400 dark:text-dark-500">({{ t('common.optional') }})</span>
          </label>
          <div class="relative">
            <input
              id="affiliate_code"
              v-model="formData.aff_code"
              type="text"
              :disabled="registrationActionDisabled"
              class="input"
              :placeholder="t('auth.invitationCodePlaceholder')"
            />
          </div>
        </div>

        <!-- Promo Code Input (Optional) -->
        <div v-if="promoCodeEnabled">
          <label for="promo_code" class="input-label">
            {{ t('auth.promoCodeLabel') }}
            <span class="ml-1 text-xs font-normal text-gray-400 dark:text-dark-500">({{ t('common.optional') }})</span>
          </label>
          <div class="relative">
            <input
              id="promo_code"
              v-model="formData.promo_code"
              type="text"
              :disabled="registrationActionDisabled"
              class="input pr-10"
              :class="{
                'border-green-500 focus:border-green-500 focus:ring-green-500': promoValidation.valid,
                'border-red-500 focus:border-red-500 focus:ring-red-500': promoValidation.invalid
              }"
              :placeholder="t('auth.promoCodePlaceholder')"
              @input="handlePromoCodeInput"
            />
            <!-- Validation indicator -->
            <div v-if="promoValidating" class="absolute inset-y-0 right-0 flex items-center pr-3.5">
              <svg class="h-4 w-4 animate-spin text-gray-400" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
              </svg>
            </div>
            <div v-else-if="promoValidation.valid" class="absolute inset-y-0 right-0 flex items-center pr-3.5">
              <Icon name="checkCircle" size="md" class="text-green-500" />
            </div>
            <div v-else-if="promoValidation.invalid" class="absolute inset-y-0 right-0 flex items-center pr-3.5">
              <Icon name="exclamationCircle" size="md" class="text-red-500" />
            </div>
          </div>
          <!-- Promo code validation result -->
          <transition name="fade">
            <div v-if="promoValidation.valid" class="mt-2 flex items-center gap-2 rounded-lg bg-green-50 px-3 py-2 dark:bg-green-900/20">
              <Icon name="gift" size="sm" class="text-green-600 dark:text-green-400" />
              <span class="text-sm text-green-700 dark:text-green-400">
                {{ t('auth.promoCodeValid', { amount: promoValidation.bonusAmount?.toFixed(2) }) }}
              </span>
            </div>
          </transition>
        </div>

        </div>

        <!-- Step 3: verification / agreement, only when one of those is required -->
        <div v-if="hasVerifyStep" class="auth-step-pane" :data-pane-state="paneState('verify')">
        <!-- Turnstile Widget -->
        <div v-if="captchaEnabled" data-testid="registration-turnstile">
          <TurnstileWidget
            ref="turnstileRef"
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

        <LoginAgreementPrompt
          v-if="loginAgreementEnabled"
          :accepted="agreementAccepted"
          :documents="loginAgreementDocuments"
          :mode="loginAgreementMode"
          :updated-at="loginAgreementUpdatedAt"
          :visible="showAgreementModal"
          @accept="acceptLoginAgreement"
          @reject="rejectLoginAgreement"
          @open="showAgreementModal = true"
        />
        </div>

        <!-- Footer: Back pinned left, primary action pinned right -->
        <div class="auth-step-footer">
          <button
            v-if="totalSteps > 1"
            type="button"
            class="auth-step-back"
            :disabled="stepIndex === 0 || isLoading"
            @click="goBack"
          >
            {{ t('auth.back') }}
          </button>
          <span v-else></span>

          <button
            v-if="!isLastStep"
            type="button"
            class="btn btn-primary ml-auto px-5"
            @click="goNext"
          >
            {{ t('auth.continue') }}
          </button>
          <button
            v-else
            type="submit"
            :disabled="registrationActionDisabled || (turnstileEnabled && !turnstileToken)"
            class="btn btn-primary ml-auto px-5"
          >
            <svg
              v-if="isLoading"
              class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            <Icon v-else name="userPlus" size="md" class="mr-2" />
            {{ submitLabel }}
          </button>
        </div>

      </form>

      <div v-if="showOAuthLogin" class="space-y-3 pt-1">
        <div class="pt-2 text-center text-[13px] text-muted">
          {{ t('auth.oauthOrContinue') }}
        </div>

        <EmailOAuthButtons
          :disabled="registrationActionDisabled"
          :aff-code="formData.aff_code"
          :promo-code="formData.promo_code"
          :github-enabled="githubOAuthEnabled"
          :google-enabled="googleOAuthEnabled"
          :show-divider="false"
          @start="handleOAuthStart"
        />

        <LinuxDoOAuthSection
          v-if="linuxdoOAuthEnabled"
          :disabled="registrationActionDisabled"
          :aff-code="formData.aff_code"
          :promo-code="formData.promo_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <WechatOAuthSection
          v-if="wechatOAuthEnabled"
          :disabled="registrationActionDisabled"
          :aff-code="formData.aff_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <OidcOAuthSection
          v-if="oidcOAuthEnabled"
          :disabled="registrationActionDisabled"
          :provider-name="oidcOAuthProviderName"
          :aff-code="formData.aff_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
      </div>
    </div>

    <!-- Footer -->
    <template #footer>
      <p class="text-gray-500 dark:text-dark-400">
        {{ t('auth.alreadyHaveAccount') }}
        <router-link
          to="/login"
          class="font-medium text-primary-600 transition-colors hover:text-primary-500 dark:text-primary-400 dark:hover:text-primary-300"
        >
          {{ t('auth.signIn') }}
        </router-link>
      </p>
    </template>
  </AuthLayout>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, onUnmounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import WechatOAuthSection from '@/components/auth/WechatOAuthSection.vue'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/CaptchaChallenge.vue'
import { useAuthStore, useAppStore } from '@/stores'
import {
  buildOAuthLoginStartURL,
  getPublicSettings,
  isWeChatWebOAuthEnabled,
  startOAuthLogin,
  type OAuthLoginStart,
  validatePromoCode,
  validateInvitationCode
} from '@/api/auth'
import { buildAuthErrorMessage } from '@/utils/authError'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import {
  formatRegistrationEmailSuffixWhitelistForMessage,
  isRegistrationEmailFormatValid,
  isRegistrationEmailAlias,
  isRegistrationEmailSuffixAllowed,
  normalizeRegistrationEmailSuffixWhitelist
} from '@/utils/registrationEmailPolicy'
import {
  clearAffiliateReferralCode,
  loadAffiliateReferralCode,
  resolveAffiliateReferralCode
} from '@/utils/oauthAffiliate'
import type { LoginAgreementDocument } from '@/types'

const { t, locale } = useI18n()
const LOGIN_AGREEMENT_STORAGE_KEY = 'sub2api_login_agreement_consent'

// ==================== Router & Stores ====================

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const appStore = useAppStore()

// ==================== State ====================

const isLoading = ref<boolean>(false)
const settingsLoaded = ref<boolean>(false)
const errorMessage = ref<string>('')
const showPassword = ref<boolean>(false)

// Quiet step flow. Presentation only: the form model, validation rules, error
// copy, navigation targets and i18n keys are untouched.
const stepIndex = ref<number>(0)
const leavingPane = ref<string | null>(null)
let stepSwapTimer: ReturnType<typeof setTimeout> | null = null

// Public settings
const registrationEnabled = ref<boolean>(true)
const emailVerifyEnabled = ref<boolean>(false)
// Public settings are injected into the app store before Vue mounts. Use that
// value for the first render so a disabled promo-code field never flashes
// while the async settings request is still in flight. If injection is
// unavailable, fail closed until the request explicitly enables the field.
const promoCodeEnabled = ref<boolean>(
  appStore.cachedPublicSettings?.promo_code_enabled === true
)
const invitationCodeEnabled = ref<boolean>(false)
const affiliateEnabled = ref<boolean>(false)
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
const linuxdoOAuthEnabled = ref<boolean>(false)
const wechatOAuthEnabled = ref<boolean>(false)
const oidcOAuthEnabled = ref<boolean>(false)
const oidcOAuthProviderName = ref<string>('OIDC')
const githubOAuthEnabled = ref<boolean>(false)
const googleOAuthEnabled = ref<boolean>(false)
const registrationEmailSuffixWhitelist = ref<string[]>([])
// 域名限量注册开关：开启时非白名单域名可注册 1 个账户（由后端判定），前端不做白名单预检。
const emailDomainQuotaEnabled = ref<boolean>(false)
const emailAliasBlockEnabled = ref<boolean>(false)
const loginAgreementEnabled = ref<boolean>(false)
const loginAgreementMode = ref<'modal' | 'checkbox' | string>('modal')
const loginAgreementUpdatedAt = ref<string>('')
const loginAgreementRevision = ref<string>('')
const loginAgreementDocuments = ref<LoginAgreementDocument[]>([])
const agreementAccepted = ref<boolean>(false)
const showAgreementModal = ref<boolean>(false)

// Turnstile
const turnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const turnstileToken = ref<string>('')
const tencentCaptchaRandstr = ref<string>('')
const aliyunCaptchaReady = computed(
  () =>
    aliyunCaptchaEnabled.value &&
    Boolean(aliyunCaptchaSceneId.value) &&
    Boolean(aliyunCaptchaPrefix.value)
)
// 动作触发式验证码（腾讯/阿里云）：提交、OAuth 启动时弹窗验证
const actionCaptchaEnabled = computed(
  () =>
    (tencentCaptchaEnabled.value && Boolean(tencentCaptchaAppId.value)) ||
    aliyunCaptchaReady.value
)
const captchaEnabled = computed(
  () =>
    (turnstileEnabled.value && Boolean(turnstileSiteKey.value)) || actionCaptchaEnabled.value
)

// ==================== Quiet step flow ====================
// Each step only asks for the fields it owns; a step is skipped entirely when it
// would have no field, so the indicator never shows an empty pane.
const hasDetailsStep = computed(
  () => invitationCodeEnabled.value || affiliateEnabled.value || promoCodeEnabled.value
)
const hasVerifyStep = computed(() => captchaEnabled.value || loginAgreementEnabled.value)
const stepKeys = computed<string[]>(() => {
  const keys = ['account']
  if (hasDetailsStep.value) keys.push('details')
  if (hasVerifyStep.value) keys.push('verify')
  return keys
})
const totalSteps = computed(() => stepKeys.value.length)
const currentStepKey = computed(
  () => stepKeys.value[Math.min(stepIndex.value, stepKeys.value.length - 1)] || 'account'
)
const isLastStep = computed(() => stepIndex.value >= stepKeys.value.length - 1)
const stepLabel = computed(() =>
  t('auth.stepProgress', { current: Math.min(stepIndex.value + 1, stepKeys.value.length), total: totalSteps.value })
)
const submitLabel = computed(() =>
  isLoading.value
    ? t('auth.processing')
    : emailVerifyEnabled.value
      ? t('auth.continue')
      : t('auth.createAccount')
)

function paneState(key: string): 'active' | 'leaving' | 'idle' {
  if (key !== currentStepKey.value) return 'idle'
  return leavingPane.value === key ? 'leaving' : 'active'
}

function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  )
}

// 320ms is the exit duration measured on the reference page; the incoming pane
// then runs its own 200ms enter animation (see auth-quiet.css).
const STEP_EXIT_MS = 320

function goToStep(nextIndex: number): void {
  const clamped = Math.max(0, Math.min(nextIndex, stepKeys.value.length - 1))
  if (clamped === stepIndex.value) return
  if (stepSwapTimer) {
    clearTimeout(stepSwapTimer)
    stepSwapTimer = null
  }
  if (prefersReducedMotion()) {
    leavingPane.value = null
    stepIndex.value = clamped
    return
  }
  leavingPane.value = stepKeys.value[stepIndex.value] || null
  stepSwapTimer = setTimeout(() => {
    leavingPane.value = null
    stepIndex.value = clamped
    stepSwapTimer = null
  }, STEP_EXIT_MS)
}

function goNext(): void {
  if (isLastStep.value) return
  const scope = currentStepKey.value === 'account' ? 'account' : 'details'
  if (!validateForm(scope)) return
  goToStep(stepIndex.value + 1)
}

function goBack(): void {
  if (stepIndex.value === 0) return
  goToStep(stepIndex.value - 1)
}

// Promo code validation
const promoValidating = ref<boolean>(false)
const promoValidation = reactive({
  valid: false,
  invalid: false,
  bonusAmount: null as number | null,
  message: ''
})
let promoValidateTimeout: ReturnType<typeof setTimeout> | null = null

// Invitation code validation
const invitationValidating = ref<boolean>(false)
const invitationValidation = reactive({
  valid: false,
  invalid: false,
  message: ''
})
let invitationValidateTimeout: ReturnType<typeof setTimeout> | null = null

const formData = reactive({
  email: '',
  password: '',
  promo_code: '',
  invitation_code: '',
  aff_code: ''
})

const errors = reactive({
  email: '',
  password: '',
  turnstile: '',
  invitation_code: ''
})

const validationToastMessage = computed(() =>
  errors.email ||
  errors.password ||
  (invitationValidation.invalid ? invitationValidation.message : '') ||
  errors.invitation_code ||
  (promoValidation.invalid ? promoValidation.message : '') ||
  errors.turnstile ||
  ''
)

const showOAuthLogin = computed(
  () =>
    linuxdoOAuthEnabled.value ||
    wechatOAuthEnabled.value ||
    oidcOAuthEnabled.value ||
    githubOAuthEnabled.value ||
    googleOAuthEnabled.value
)

const agreementGateActive = computed(
  () => loginAgreementEnabled.value && !agreementAccepted.value
)

const registrationActionDisabled = computed(
  () => isLoading.value || !settingsLoaded.value || agreementGateActive.value
)

watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// Settings arrive after mount; keep the active step inside the rebuilt list.
watch(
  () => stepKeys.value.length,
  (length) => {
    if (stepIndex.value > length - 1) {
      leavingPane.value = null
      stepIndex.value = Math.max(0, length - 1)
    }
  }
)

function syncAffiliateReferralCode(): string {
  const code = resolveAffiliateReferralCode(route.query.aff, route.query.aff_code)
  if (code) {
    formData.aff_code = code
  }
  return code
}

// ==================== Lifecycle ====================

onMounted(async () => {
  syncAffiliateReferralCode()

  try {
    const settings = await getPublicSettings()
    registrationEnabled.value = settings.registration_enabled
    emailVerifyEnabled.value = settings.email_verify_enabled
    promoCodeEnabled.value = settings.promo_code_enabled
    invitationCodeEnabled.value = settings.invitation_code_enabled
    affiliateEnabled.value = settings.affiliate_enabled
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
    linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled
    wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
    oidcOAuthEnabled.value = settings.oidc_oauth_enabled
    oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
    githubOAuthEnabled.value = settings.github_oauth_enabled
    googleOAuthEnabled.value = settings.google_oauth_enabled
    registrationEmailSuffixWhitelist.value = normalizeRegistrationEmailSuffixWhitelist(
      settings.registration_email_suffix_whitelist || []
    )
    emailDomainQuotaEnabled.value = settings.registration_email_domain_quota_enabled === true
    emailAliasBlockEnabled.value = settings.registration_email_alias_block_enabled === true
    applyLoginAgreementSettings(settings)

    // Read promo code from URL parameter only if promo code is enabled
    if (promoCodeEnabled.value) {
      const promoParam = route.query.promo as string
      if (promoParam) {
        formData.promo_code = promoParam
        // Validate the promo code from URL
        await validatePromoCodeDebounced(promoParam)
      }
    }
    syncAffiliateReferralCode()
  } catch (error) {
    console.error('Failed to load public settings:', error)
    loginAgreementEnabled.value = false
    agreementAccepted.value = true
  } finally {
    settingsLoaded.value = true
  }
})

watch(
  () => [route.query.aff, route.query.aff_code],
  () => {
    syncAffiliateReferralCode()
  }
)

onUnmounted(() => {
  if (promoValidateTimeout) {
    clearTimeout(promoValidateTimeout)
  }
  if (invitationValidateTimeout) {
    clearTimeout(invitationValidateTimeout)
  }
  if (stepSwapTimer) {
    clearTimeout(stepSwapTimer)
    stepSwapTimer = null
  }
})

// ==================== Login Agreement ====================

function applyLoginAgreementSettings(settings: {
  login_agreement_enabled?: boolean
  login_agreement_mode?: string
  login_agreement_updated_at?: string
  login_agreement_revision?: string
  login_agreement_documents?: LoginAgreementDocument[]
}): void {
  const documents = Array.isArray(settings.login_agreement_documents)
    ? settings.login_agreement_documents.filter((doc) => doc.title?.trim())
    : []
  loginAgreementDocuments.value = documents
  loginAgreementEnabled.value = settings.login_agreement_enabled === true && documents.length > 0
  loginAgreementMode.value = settings.login_agreement_mode === 'checkbox' ? 'checkbox' : 'modal'
  loginAgreementUpdatedAt.value = settings.login_agreement_updated_at || ''
  loginAgreementRevision.value =
    settings.login_agreement_revision ||
    `${loginAgreementUpdatedAt.value}:${documents.map((doc) => `${doc.id}:${doc.title}`).join('|')}`

  agreementAccepted.value = !loginAgreementEnabled.value || hasAcceptedLoginAgreement(loginAgreementRevision.value)
  showAgreementModal.value =
    loginAgreementEnabled.value && !agreementAccepted.value && loginAgreementMode.value !== 'checkbox'
}

function hasAcceptedLoginAgreement(revision: string): boolean {
  if (!revision) {
    return false
  }
  try {
    const raw = localStorage.getItem(LOGIN_AGREEMENT_STORAGE_KEY)
    if (!raw) {
      return false
    }
    const parsed = JSON.parse(raw) as { revision?: string }
    return parsed.revision === revision
  } catch {
    return false
  }
}

function acceptLoginAgreement(): void {
  if (loginAgreementRevision.value) {
    localStorage.setItem(
      LOGIN_AGREEMENT_STORAGE_KEY,
      JSON.stringify({
        revision: loginAgreementRevision.value,
        accepted_at: new Date().toISOString()
      })
    )
  }
  agreementAccepted.value = true
  showAgreementModal.value = false
}

function rejectLoginAgreement(): void {
  localStorage.removeItem(LOGIN_AGREEMENT_STORAGE_KEY)
  agreementAccepted.value = false
  showAgreementModal.value = false
  appStore.showWarning(t('legal.loginAgreementPrompt.registerRejectedWarning'))
}

// ==================== Promo Code Validation ====================

function handlePromoCodeInput(): void {
  const code = formData.promo_code.trim()

  // Clear previous validation
  promoValidation.valid = false
  promoValidation.invalid = false
  promoValidation.bonusAmount = null
  promoValidation.message = ''

  if (!code) {
    promoValidating.value = false
    return
  }

  // Debounce validation
  if (promoValidateTimeout) {
    clearTimeout(promoValidateTimeout)
  }

  promoValidateTimeout = setTimeout(() => {
    validatePromoCodeDebounced(code)
  }, 500)
}

async function validatePromoCodeDebounced(code: string): Promise<void> {
  if (!code.trim()) return

  promoValidating.value = true

  try {
    const result = await validatePromoCode(code)

    if (result.valid) {
      promoValidation.valid = true
      promoValidation.invalid = false
      promoValidation.bonusAmount = result.bonus_amount || 0
      promoValidation.message = ''
    } else {
      promoValidation.valid = false
      promoValidation.invalid = true
      promoValidation.bonusAmount = null
      // 根据错误码显示对应的翻译
      promoValidation.message = getPromoErrorMessage(result.error_code)
    }
  } catch (error) {
    console.error('Failed to validate promo code:', error)
    promoValidation.valid = false
    promoValidation.invalid = true
    promoValidation.message = t('auth.promoCodeInvalid')
  } finally {
    promoValidating.value = false
  }
}

function getPromoErrorMessage(errorCode?: string): string {
  switch (errorCode) {
    case 'PROMO_CODE_NOT_FOUND':
      return t('auth.promoCodeNotFound')
    case 'PROMO_CODE_EXPIRED':
      return t('auth.promoCodeExpired')
    case 'PROMO_CODE_DISABLED':
      return t('auth.promoCodeDisabled')
    case 'PROMO_CODE_MAX_USED':
      return t('auth.promoCodeMaxUsed')
    case 'PROMO_CODE_ALREADY_USED':
      return t('auth.promoCodeAlreadyUsed')
    default:
      return t('auth.promoCodeInvalid')
  }
}

// ==================== Invitation Code Validation ====================

function handleInvitationCodeInput(): void {
  const code = formData.invitation_code.trim()

  // Clear previous validation
  invitationValidation.valid = false
  invitationValidation.invalid = false
  invitationValidation.message = ''
  errors.invitation_code = ''

  if (!code) {
    return
  }

  // Debounce validation
  if (invitationValidateTimeout) {
    clearTimeout(invitationValidateTimeout)
  }

  invitationValidateTimeout = setTimeout(() => {
    validateInvitationCodeDebounced(code)
  }, 500)
}

async function validateInvitationCodeDebounced(code: string): Promise<void> {
  invitationValidating.value = true

  try {
    const result = await validateInvitationCode(code)

    if (result.valid) {
      invitationValidation.valid = true
      invitationValidation.invalid = false
      invitationValidation.message = ''
    } else {
      invitationValidation.valid = false
      invitationValidation.invalid = true
      invitationValidation.message = getInvitationErrorMessage(result.error_code)
    }
  } catch {
    invitationValidation.valid = false
    invitationValidation.invalid = true
    invitationValidation.message = t('auth.invitationCodeInvalid')
  } finally {
    invitationValidating.value = false
  }
}

function getInvitationErrorMessage(errorCode?: string): string {
  switch (errorCode) {
    case 'INVITATION_CODE_NOT_FOUND':
      return t('auth.invitationCodeInvalid')
    case 'INVITATION_CODE_INVALID':
      return t('auth.invitationCodeInvalid')
    case 'INVITATION_CODE_USED':
      return t('auth.invitationCodeInvalid')
    case 'INVITATION_CODE_DISABLED':
      return t('auth.invitationCodeInvalid')
    default:
      return t('auth.invitationCodeInvalid')
  }
}

// ==================== Turnstile Handlers ====================

function onTurnstileVerify(token: string, randstr = ''): void {
  turnstileToken.value = token
  tencentCaptchaRandstr.value = randstr
  errors.turnstile = ''
}

function onTurnstileExpire(): void {
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  errors.turnstile = t('auth.turnstileExpired')
}

function onTurnstileError(): void {
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  errors.turnstile = t('auth.turnstileFailed')
}

function resetCaptchaProof(): void {
  turnstileRef.value?.reset()
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  errors.turnstile = ''
}

async function acquireActionProof(): Promise<boolean> {
  if (!actionCaptchaEnabled.value) return true

  const proof = await turnstileRef.value?.verifyAction()
  if (!proof) return false

  turnstileToken.value = proof.token
  tencentCaptchaRandstr.value = proof.randstr
  return true
}

async function handleOAuthStart(request: OAuthLoginStart): Promise<void> {
  if (registrationActionDisabled.value) return

  if (!actionCaptchaEnabled.value) {
    window.location.href = buildOAuthLoginStartURL(request)
    return
  }

  isLoading.value = true
  try {
    const proof = await turnstileRef.value?.verifyAction()
    if (!proof) return

    const result = await startOAuthLogin(
      request,
      tencentCaptchaEnabled.value
        ? {
            tencent_captcha_ticket: proof.token,
            tencent_captcha_randstr: proof.randstr
          }
        : { turnstile_token: proof.token }
    )
    window.location.href = result.authorize_url
  } catch (error: unknown) {
    errorMessage.value = extractI18nErrorMessage(
      error,
      t,
      'auth.errors',
      t('auth.turnstileFailed')
    )
    appStore.showError(errorMessage.value)
  } finally {
    resetCaptchaProof()
    isLoading.value = false
  }
}

// ==================== Validation ====================

function validateEmail(email: string): boolean {
  const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
  return emailRegex.test(email)
}

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

function validateForm(scope: 'account' | 'details' | 'full' = 'full'): boolean {
  // Reset errors
  errors.email = ''
  errors.password = ''
  errors.turnstile = ''
  errors.invitation_code = ''

  let isValid = true

  // The agreement gate only belongs to the final submit; pressing Continue on a
  // step must not open the agreement dialog.
  if (scope === 'full' && agreementGateActive.value) {
    appStore.showWarning(t('legal.loginAgreementPrompt.registerRequiredWarning'))
    if (loginAgreementMode.value !== 'checkbox') {
      showAgreementModal.value = true
    }
    return false
  }

  // Email validation
  if (!formData.email.trim()) {
    errors.email = t('auth.emailRequired')
    isValid = false
  } else if (!validateEmail(formData.email)) {
    errors.email = t('auth.invalidEmail')
    isValid = false
  } else if (emailAliasBlockEnabled.value && isRegistrationEmailAlias(formData.email)) {
    errors.email = t('auth.emailAliasNotAllowed')
    isValid = false
  } else if (!isRegistrationEmailFormatValid(formData.email)) {
    // 与后端 EMAIL_FORMAT_INVALID 同口径：长度 / 特殊字符 / 多级域名。
    errors.email = t('auth.emailFormatInvalid')
    isValid = false
  } else if (
    !emailDomainQuotaEnabled.value &&
    !isRegistrationEmailSuffixAllowed(formData.email, registrationEmailSuffixWhitelist.value)
  ) {
    // 域名限量注册关闭时保持严格白名单预检；开启时交给后端按域名额度判定
    errors.email = buildEmailSuffixNotAllowedMessage()
    isValid = false
  }

  // Password validation
  if (!formData.password) {
    errors.password = t('auth.passwordRequired')
    isValid = false
  } else if (formData.password.length < 6) {
    errors.password = t('auth.passwordMinLength')
    isValid = false
  }

  // Invitation code validation (required when enabled)
  if (scope !== 'account' && invitationCodeEnabled.value) {
    if (!formData.invitation_code.trim()) {
      errors.invitation_code = t('auth.invitationCodeRequired')
      isValid = false
    }
  }

  // Turnstile validation
  if (scope === 'full' && turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

// ==================== Form Handlers ====================

async function handleRegister(): Promise<void> {
  // Clear previous error
  errorMessage.value = ''

  // Validate form
  if (!validateForm()) {
    return
  }

  // Check promo code validation status
  if (formData.promo_code.trim()) {
    // If promo code is being validated, wait
    if (promoValidating.value) {
      errorMessage.value = t('auth.promoCodeValidating')
      return
    }
    // If promo code is invalid, block submission
    if (promoValidation.invalid) {
      errorMessage.value = t('auth.promoCodeInvalidCannotRegister')
      return
    }
  }

  // Check invitation code validation status (if enabled and code provided)
  if (invitationCodeEnabled.value) {
    // If still validating, wait
    if (invitationValidating.value) {
      errorMessage.value = t('auth.invitationCodeValidating')
      return
    }
    // If invitation code is invalid, block submission
    if (invitationValidation.invalid) {
      errorMessage.value = t('auth.invitationCodeInvalidCannotRegister')
      return
    }
    // If invitation code is required but not validated yet
    if (formData.invitation_code.trim() && !invitationValidation.valid) {
      errorMessage.value = t('auth.invitationCodeValidating')
      // Trigger validation
      await validateInvitationCodeDebounced(formData.invitation_code.trim())
      if (!invitationValidation.valid) {
        errorMessage.value = t('auth.invitationCodeInvalidCannotRegister')
        return
      }
    }
  }

  if (!(await acquireActionProof())) {
    return
  }

  isLoading.value = true

  try {
    const affCode = formData.aff_code.trim() || loadAffiliateReferralCode()
    if (affCode) {
      formData.aff_code = affCode
    }

    // If email verification is enabled, redirect to verification page
    if (emailVerifyEnabled.value) {
      // Store registration data in sessionStorage
      sessionStorage.setItem(
        'register_data',
        JSON.stringify({
          email: formData.email,
          password: formData.password,
          turnstile_token:
            turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
          tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
          tencent_captcha_randstr: tencentCaptchaEnabled.value ? tencentCaptchaRandstr.value : undefined,
          promo_code: formData.promo_code || undefined,
          invitation_code: formData.invitation_code || undefined,
          ...(affCode ? { aff_code: affCode } : {})
        })
      )

      // Navigate to email verification page
      await router.push('/email-verify')
      return
    }

    // Otherwise, directly register
    await authStore.register({
      email: formData.email,
      password: formData.password,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value ? tencentCaptchaRandstr.value : undefined,
      promo_code: formData.promo_code || undefined,
      invitation_code: formData.invitation_code || undefined,
      ...(affCode ? { aff_code: affCode } : {})
    })
    clearAffiliateReferralCode()

    // Show success toast
    appStore.showSuccess(t('auth.accountCreatedSuccess', { siteName: siteName.value }))

    // Redirect to dashboard
    await router.push('/dashboard')
  } catch (error: unknown) {
    // Handle registration error
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.registrationFailed'))

    // Also show error toast
    appStore.showError(errorMessage.value)
  } finally {
    if (captchaEnabled.value) {
      resetCaptchaProof()
    }
    isLoading.value = false
  }
}

function buildRegistrationErrorMessage(error: unknown, fallback: string): string {
  if (extractApiErrorCode(error) === 'EMAIL_FORMAT_INVALID') {
    return t('auth.emailFormatInvalid')
  }
  if (extractApiErrorCode(error) === 'EMAIL_DOMAIN_REGISTRATION_LIMIT') {
    return t('auth.emailDomainRegistrationLimit')
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
