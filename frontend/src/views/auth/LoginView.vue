<template>
  <AuthLayout>
    <div class="space-y-6">
      <!-- Title: follows the step. First the credentials, then (only when the server asks for one) the second factor. -->
      <div>
        <h2 class="auth-heading" data-testid="login-heading">
          {{ challengeHeading || t('auth.welcomeBack') }}
        </h2>
        <p class="auth-sub">
          {{ challengeSub || t('auth.signInToAccount') }}
        </p>
      </div>
      <!-- Login Form -->
      <!-- 标准登录表单：显式 on + username/current-password 语义，
           让密码管理器只在登录页填账号密码，不要串到其它页面。 -->
      <form autocomplete="on" @submit.prevent="handleFormSubmit" class="space-y-5">
        <!-- Step head, same as the sign-up page: only while a second factor is being asked for. -->
        <QuietStepHead
          v-if="factorShown"
          data-testid="login-step-head"
          :current="2"
          :total="2"
          :label="t('auth.stepProgress', { current: 2, total: 2 })"
        />

        <!-- Step 1: credentials. The pane stays mounted while step 2 is showing, so the fields and the
             captcha widget are not rebuilt (and do not re-verify) when the user comes back. -->
        <div
          class="auth-step-pane"
          :class="{ 'login-pane-static': !leftCredentialsOnce }"
          data-testid="login-credentials-pane"
          :data-pane-state="paneState('credentials')"
        >
          <!-- Email Input -->
          <div>
            <label for="email" class="input-label">
              {{ t('auth.emailLabel') }}
            </label>
            <div class="relative">
              <input
                id="email"
                ref="emailInput"
                name="email"
                v-model="formData.email"
                type="email"
                required
                autocomplete="username"
                :disabled="inputsLocked"
                :readonly="isLoading || passkeyLoading"
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
                ref="passwordInput"
                name="password"
                v-model="formData.password"
                :type="showPassword ? 'text' : 'password'"
                required
                autocomplete="current-password"
                :disabled="inputsLocked"
                :readonly="isLoading || passkeyLoading"
                class="input pr-10"
                :class="{ 'input-error': errors.password }"
                :placeholder="t('auth.passwordPlaceholder')"
              />
              <button
                type="button"
                @click="showPassword = !showPassword"
                :disabled="authActionDisabled"
                :aria-pressed="showPassword"
                :aria-label="t('auth.passwordLabel')"
                class="absolute inset-y-0 right-0 flex items-center justify-center rounded-md pr-3 text-dimmed transition-colors hover:text-default focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(pointer:coarse)]:min-w-11"
              >
                <Icon v-if="showPassword" name="eyeOff" size="md" />
                <Icon v-else name="eye" size="md" />
              </button>
            </div>
            <div class="mt-1 flex items-center justify-between">
              <span></span>
              <router-link
                v-if="passwordResetEnabled && !backendModeEnabled"
                to="/forgot-password"
                class="rounded-sm text-sm font-medium text-primary transition-colors hover:text-primary/80 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              >
                {{ t('auth.forgotPassword') }}
              </router-link>
            </div>
          </div>

          <!-- Turnstile Widget -->
          <div v-if="captchaEnabled">
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

          <!-- Submit Button -->
          <button
            type="submit"
            :disabled="authActionDisabled || (turnstileEnabled && !turnstileToken)"
            class="btn btn-primary w-full"
          >
            <svg
              v-if="isLoading"
              class="-ml-1 mr-2 h-4 w-4 animate-spin text-white motion-reduce:animate-none"
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
            <Icon v-else name="login" size="md" class="mr-2" />
            {{ isLoading ? t('auth.signingIn') : t('auth.signIn') }}
          </button>

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

          <div v-if="showPasskeyLogin || showOAuthLogin" class="space-y-3 pt-1">
            <div class="pt-2 text-center text-[13px] text-muted">
              {{ t('auth.oauthOrContinue') }}
            </div>

            <button
              v-if="showPasskeyLogin"
              type="button"
              class="btn btn-secondary w-full"
              :disabled="authActionDisabled"
              @click="handlePasskeyLogin"
            >
              <Icon name="key" size="md" class="mr-2" />
              {{ passkeyLoading ? t('auth.passkeySigningIn') : t('auth.passkeySignIn') }}
            </button>

            <EmailOAuthButtons
              :disabled="authActionDisabled"
              :github-enabled="githubOAuthEnabled"
              :google-enabled="googleOAuthEnabled"
              :show-divider="false"
              @start="handleOAuthStart"
            />

            <LinuxDoOAuthSection
              v-if="linuxdoOAuthEnabled"
              :disabled="authActionDisabled"
              :show-divider="false"
              @start="handleOAuthStart"
            />
            <DingTalkOAuthSection
              v-if="dingtalkOAuthEnabled"
              :disabled="authActionDisabled"
              :show-divider="false"
              @start="handleOAuthStart"
            />
            <WechatOAuthSection
              v-if="wechatOAuthEnabled"
              :disabled="authActionDisabled"
              :show-divider="false"
              @start="handleOAuthStart"
            />
            <OidcOAuthSection
              v-if="oidcOAuthEnabled"
              :disabled="authActionDisabled"
              :provider-name="oidcOAuthProviderName"
              :show-divider="false"
              @start="handleOAuthStart"
            />
          </div>
        </div>

        <!-- Step 2: second factor, inside the card instead of a full-page modal. The pane outlives the challenge by
             its exit animation (factorView), and its content only mounts once the pane is the one on screen: the
             code box focuses itself on mount, and a hidden input cannot take focus. -->
        <div
          v-if="factorView"
          class="auth-step-pane"
          data-testid="login-factor-pane"
          :data-pane-state="paneState('factor')"
        >
          <template v-if="shownPane === 'factor'">
            <TotpFactorPane
              v-if="factorView.nextFactor === 'totp'"
              ref="totpPaneRef"
              :temp-token="factorView.tempToken"
              :user-email-masked="factorView.userEmailMasked"
              :account-key="formData.email"
              @verify="handle2FAVerify"
            >
              <!-- Everyone who typed a wrong password also lands here (the server answers both the same way), so the
                   sentence that explains it is also the way out: one tap, with a comfortable target on a phone. -->
              <button
                type="button"
                class="-my-1 block w-full rounded-md py-1 text-left text-[13px] text-muted transition-colors hover:text-highlighted focus-visible:text-highlighted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:opacity-60 [@media(pointer:coarse)]:min-h-11"
                data-testid="login-challenge-back-hint"
                :disabled="factorBusy"
                @click="handle2FACancel"
              >
                {{ t('auth.loginChallengeBackHint') }}
              </button>
            </TotpFactorPane>
            <PasskeyFactorPane
              v-else
              :supported="passkeySupported"
              :verifying="passkeyVerifying"
              :user-email-masked="factorView.userEmailMasked"
              @verify="handlePasskeyFactor"
            />
          </template>
        </div>

        <!-- Footer: Back pinned left, like the sign-up page. -->
        <div v-if="factorShown" class="auth-step-footer">
          <button
            type="button"
            class="auth-step-back"
            data-testid="login-back"
            :disabled="factorBusy"
            @click="handle2FACancel"
          >
            {{ t('auth.back') }}
          </button>
          <span></span>
        </div>
      </form>
    </div>

    <!-- Footer -->
    <template v-if="shownPane === 'credentials' && !backendModeEnabled && publicSettingsLoaded && registrationEnabled" #footer>
      <p class="text-muted">
        {{ t('auth.dontHaveAccount') }}
        <router-link
          to="/register"
          class="rounded-sm font-medium text-primary transition-colors hover:text-primary/80 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        >
          {{ t('auth.signUp') }}
        </router-link>
      </p>
    </template>
  </AuthLayout>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import DingTalkOAuthSection from '@/components/auth/DingTalkOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import WechatOAuthSection from '@/components/auth/WechatOAuthSection.vue'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import QuietStepHead from '@/components/common/QuietStepHead.vue'
import TotpFactorPane from '@/components/auth/TotpFactorPane.vue'
import PasskeyFactorPane from '@/components/auth/PasskeyFactorPane.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/CaptchaChallenge.vue'
import { useAuthStore, useAppStore } from '@/stores'
import { passkeyAPI } from '@/api/passkey'
import {
  buildOAuthLoginStartURL,
  getPublicSettings,
  isTotp2FARequired,
  isWeChatWebOAuthEnabled,
  startOAuthLogin,
  type OAuthLoginStart
} from '@/api/auth'
import type {
  ActionCaptchaRequestProof,
  LoginAgreementDocument,
  TotpLoginResponse
} from '@/types'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { clearAllAffiliateReferralCodes } from '@/utils/oauthAffiliate'
import { isChunkLoadError, pushOrReload, recoverFromChunkLoadError } from '@/utils/chunkLoadError'

const { t } = useI18n()
const LOGIN_AGREEMENT_STORAGE_KEY = 'sub2api_login_agreement_consent'

// ==================== Router & Stores ====================

const router = useRouter()
const authStore = useAuthStore()
const appStore = useAppStore()

// ==================== State ====================

const isLoading = ref<boolean>(false)
const passkeyLoading = ref<boolean>(false)
const errorMessage = ref<string>('')
const showPassword = ref<boolean>(false)
const publicSettingsLoaded = ref<boolean>(false)

// Public settings
const registrationEnabled = ref<boolean>(false)
const turnstileEnabled = ref<boolean>(false)
const turnstileSiteKey = ref<string>('')
const tencentCaptchaEnabled = ref<boolean>(false)
const tencentCaptchaAppId = ref<string>('')
const tencentCaptchaRegion = ref<string>('cn')
const aliyunCaptchaEnabled = ref<boolean>(false)
const aliyunCaptchaSceneId = ref<string>('')
const aliyunCaptchaPrefix = ref<string>('')
const aliyunCaptchaRegion = ref<string>('cn')
const linuxdoOAuthEnabled = ref<boolean>(false)
const dingtalkOAuthEnabled = ref<boolean>(false)
const wechatOAuthEnabled = ref<boolean>(false)
const backendModeEnabled = ref<boolean>(false)
const oidcOAuthEnabled = ref<boolean>(false)
const oidcOAuthProviderName = ref<string>('OIDC')
const githubOAuthEnabled = ref<boolean>(false)
const googleOAuthEnabled = ref<boolean>(false)
const passwordResetEnabled = ref<boolean>(false)
const passkeyEnabled = ref<boolean>(false)
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
// 动作触发式验证码（腾讯/阿里云）：提交、OAuth 启动、passkey 时弹窗验证
const actionCaptchaEnabled = computed(
  () =>
    (tencentCaptchaEnabled.value && Boolean(tencentCaptchaAppId.value)) ||
    aliyunCaptchaReady.value
)
const captchaEnabled = computed(
  () =>
    (turnstileEnabled.value && Boolean(turnstileSiteKey.value)) || actionCaptchaEnabled.value
)

// 双因子登录挑战：服务端按账号已绑定因子下发 required_factors / next_factor。
type LoginFactor = 'totp' | 'passkey'
interface LoginFactorChallenge {
  tempToken: string
  requiredFactors: string[]
  passedFactors: string[]
  nextFactor: LoginFactor
  userEmailMasked: string
  passkeySessionToken: string
  passkeyOptions?: { publicKey: Record<string, unknown> }
}
const loginChallenge = ref<LoginFactorChallenge | null>(null)
const passkeyVerifying = ref<boolean>(false)
const passkeySupported = computed(() => passkeyAPI.isSupported())
const totpPaneRef = ref<InstanceType<typeof TotpFactorPane> | null>(null)
const totpVerifying = ref<boolean>(false)
const emailInput = ref<HTMLInputElement | null>(null)
const passwordInput = ref<HTMLInputElement | null>(null)

/** 把后端登录挑战转成前端状态机（缺字段时按 TOTP 单因子兼容旧后端）。 */
function applyLoginChallenge(response: TotpLoginResponse): void {
  const required = response.required_factors?.length ? response.required_factors : ['totp']
  const passed = response.passed_factors ?? []
  const next = response.next_factor || required.find((factor) => !passed.includes(factor)) || 'totp'
  loginChallenge.value = {
    tempToken: response.temp_token || '',
    requiredFactors: required,
    passedFactors: passed,
    nextFactor: next === 'passkey' ? 'passkey' : 'totp',
    userEmailMasked: response.user_email_masked || maskLoginEmail(formData.email),
    passkeySessionToken: response.passkey_session_token || '',
    passkeyOptions: response.passkey_options
  }
}

const formData = reactive({
  email: '',
  password: ''
})

watch([() => formData.email, () => formData.password], () => {
  if (loginChallenge.value) leaveChallenge()
})

const errors = reactive({
  email: '',
  password: '',
  turnstile: ''
})

const validationToastMessage = computed(
  () => errors.email || errors.password || errors.turnstile || ''
)

const agreementGateActive = computed(
  () => loginAgreementEnabled.value && !agreementAccepted.value
)

const authActionDisabled = computed(
  () => isLoading.value || passkeyLoading.value || !publicSettingsLoaded.value || agreementGateActive.value
)

// The fields are only really disabled while the page cannot take input at all. While a request is in flight they are
// read-only instead (see the template): disabling a focused field on a phone drops the soft keyboard, the viewport
// resizes, and the card jumps once per attempt.
const inputsLocked = computed(() => !publicSettingsLoaded.value || agreementGateActive.value)

// Back is off while a second-factor request is in flight, same as the old modal's Cancel.
const factorBusy = computed(() => passkeyVerifying.value || totpVerifying.value)

// ---- Step panes -----------------------------------------------------------------------------------------------
// Same contract as the sign-up page (auth-quiet.css): the pane that is going away plays its 150ms exit first, and only
// then the next pane takes its place and plays its own 200ms enter, so there is never a moment with two panes stacked.
type LoginPane = 'credentials' | 'factor'
const STEP_EXIT_MS = 150
const shownPane = ref<LoginPane>('credentials')
const leavingPane = ref<LoginPane | null>(null)
const leftCredentialsOnce = ref<boolean>(false)
/** What the second-step pane renders. Follows the challenge, but outlives it until the pane has finished leaving. */
const factorView = ref<LoginFactorChallenge | null>(null)
let paneTimer: ReturnType<typeof setTimeout> | null = null
let refocusPasswordOnReturn = false

/** The second step, only while it is the pane on screen: the heading, step head and Back button follow this. */
const factorShown = computed(() => (shownPane.value === 'factor' ? factorView.value : null))

const challengeHeading = computed(() => {
  const view = factorShown.value
  if (!view) return ''
  return view.nextFactor === 'passkey' ? t('auth.passkeyLoginTitle') : t('profile.totp.loginTitle')
})

const challengeSub = computed(() => {
  const view = factorShown.value
  if (!view) return ''
  return view.nextFactor === 'passkey' ? t('auth.passkeyLoginHint') : t('profile.totp.loginHint')
})

function paneState(key: LoginPane): 'active' | 'leaving' | 'idle' {
  if (key !== shownPane.value) return 'idle'
  return leavingPane.value === key ? 'leaving' : 'active'
}

function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  )
}

/**
 * The credentials pane is (back) on screen with no challenge behind it. The first attempt spent the captcha proof, so ask
 * for a new one now that the widget is visible (a widget that is reset while hidden can stall until it is visible).
 */
function arriveAtCredentials(): void {
  factorView.value = null
  if (captchaEnabled.value) void nextTick(() => resetCaptchaProof())
  if (refocusPasswordOnReturn) {
    refocusPasswordOnReturn = false
    // They came back to fix something, and a mistyped password is by far the usual reason: cursor in the box with the
    // text selected, so the next keystroke replaces it. (An autofill edit that cancelled the step must not grab focus.)
    void nextTick(() => {
      passwordInput.value?.focus()
      passwordInput.value?.select()
    })
  }
}

function showPane(next: LoginPane): void {
  if (paneTimer) {
    clearTimeout(paneTimer)
    paneTimer = null
  }
  if (next === shownPane.value) {
    // changed their mind while the pane was still leaving (e.g. autofill edited the fields): it simply stays
    leavingPane.value = null
    if (next === 'credentials') arriveAtCredentials()
    return
  }
  const finish = () => {
    shownPane.value = next
    leavingPane.value = null
    paneTimer = null
    if (next === 'credentials') arriveAtCredentials()
  }
  leftCredentialsOnce.value = true
  if (prefersReducedMotion()) {
    finish()
    return
  }
  leavingPane.value = shownPane.value
  paneTimer = setTimeout(finish, STEP_EXIT_MS)
}

// `sync`: the pane swap has to start in the same tick the challenge arrives or goes away.
watch(
  loginChallenge,
  (next, previous) => {
    if (next) factorView.value = next
    // the second step replacing itself (authenticator code -> passkey) keeps the pane; only a change of step swaps
    if (Boolean(next) === Boolean(previous)) return
    showPane(next ? 'factor' : 'credentials')
  },
  { flush: 'sync' }
)

onBeforeUnmount(() => {
  if (paneTimer) clearTimeout(paneTimer)
})

const showPasskeyLogin = computed(
  () => passkeyEnabled.value && typeof window !== 'undefined' && typeof window.PublicKeyCredential !== 'undefined'
)

const showOAuthLogin = computed(
  () =>
    !backendModeEnabled.value &&
    (linuxdoOAuthEnabled.value ||
      dingtalkOAuthEnabled.value ||
      wechatOAuthEnabled.value ||
      oidcOAuthEnabled.value ||
      githubOAuthEnabled.value ||
      googleOAuthEnabled.value)
)

watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// ==================== Lifecycle ====================

onMounted(async () => {
  const expiredFlag = sessionStorage.getItem('auth_expired')
  if (expiredFlag) {
    sessionStorage.removeItem('auth_expired')
    const message = t('auth.reloginRequired')
    errorMessage.value = message
    appStore.showWarning(message)
  }

  try {
    const settings = await getPublicSettings()
    registrationEnabled.value = settings.registration_enabled === true
    turnstileEnabled.value = settings.turnstile_enabled
    turnstileSiteKey.value = settings.turnstile_site_key || ''
    tencentCaptchaEnabled.value = settings.tencent_captcha_enabled === true
    tencentCaptchaAppId.value = settings.tencent_captcha_app_id || ''
    tencentCaptchaRegion.value = settings.tencent_captcha_region || 'cn'
    aliyunCaptchaEnabled.value = settings.aliyun_captcha_enabled === true
    aliyunCaptchaSceneId.value = settings.aliyun_captcha_scene_id || ''
    aliyunCaptchaPrefix.value = settings.aliyun_captcha_prefix || ''
    aliyunCaptchaRegion.value = settings.aliyun_captcha_region || 'cn'
    linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled
    dingtalkOAuthEnabled.value = settings.dingtalk_oauth_enabled ?? false
    wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
    backendModeEnabled.value = settings.backend_mode_enabled
    oidcOAuthEnabled.value = settings.oidc_oauth_enabled
    oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
    githubOAuthEnabled.value = settings.github_oauth_enabled
    googleOAuthEnabled.value = settings.google_oauth_enabled
    backendModeEnabled.value = settings.backend_mode_enabled
    passwordResetEnabled.value = settings.password_reset_enabled
    passkeyEnabled.value = settings.passkey_enabled === true
    applyLoginAgreementSettings(settings)
  } catch (error) {
    console.error('Failed to load public settings:', error)
    loginAgreementEnabled.value = false
    agreementAccepted.value = true
  } finally {
    publicSettingsLoaded.value = true
  }

  // `autofocus` used to open the soft keyboard on every phone page load. Focus the email field only on devices with a
  // precise pointer, and only once the field is enabled and no agreement dialog is in front of it.
  if (!showAgreementModal.value && window.matchMedia?.('(pointer: fine)').matches) {
    await nextTick()
    emailInput.value?.focus()
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
  appStore.showWarning(t('legal.loginAgreementPrompt.loginRejectedWarning'))
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
  // While the second step is on screen the widget sits hidden and its proof is not needed any more; saying
  // "verification expired" there would be a toast about a thing the user cannot see.
  if (shownPane.value === 'factor' || loginChallenge.value) return
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

/**
 * Back to the credentials step. The first attempt spent the captcha proof, so it is dropped here (it can never be
 * submitted again) and the widget itself is reset once the credentials pane is on screen again (see showPane).
 * `refocus`: the user chose to go back (Back, or the hint), so land them in the password box.
 */
function leaveChallenge(options: { refocus?: boolean } = {}): void {
  refocusPasswordOnReturn = options.refocus === true && loginChallenge.value !== null
  loginChallenge.value = null
  passkeyVerifying.value = false
  totpVerifying.value = false
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
}

async function acquireActionProof(): Promise<boolean> {
  if (!actionCaptchaEnabled.value) return true

  const proof = await turnstileRef.value?.verifyAction()
  if (!proof) return false

  turnstileToken.value = proof.token
  tencentCaptchaRandstr.value = proof.randstr
  return true
}

// ==================== Validation ====================

function validateForm(): boolean {
  // Reset errors
  errors.email = ''
  errors.password = ''
  errors.turnstile = ''

  let isValid = true

  if (agreementGateActive.value) {
    appStore.showWarning(t('legal.loginAgreementPrompt.loginRequiredWarning'))
    if (loginAgreementMode.value !== 'checkbox') {
      showAgreementModal.value = true
    }
    return false
  }

  // Email validation
  if (!formData.email.trim()) {
    errors.email = t('auth.emailRequired')
    isValid = false
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(formData.email)) {
    errors.email = t('auth.invalidEmail')
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

  // Turnstile validation
  if (turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

// ==================== Form Handlers ====================

/** Enter on the credentials step submits them; on the second step the code box owns Enter. */
function handleFormSubmit(): void {
  if (loginChallenge.value) return
  void handleLogin()
}

async function handleLogin(): Promise<void> {
  errorMessage.value = ''
  loginChallenge.value = null

  // Validate form
  if (!validateForm()) {
    return
  }

  if (!(await acquireActionProof())) {
    return
  }

  isLoading.value = true

  try {
    // Call auth store login（阿里云 captchaVerifyParam 复用 turnstile_token 字段）
    const response = await authStore.login({
      email: formData.email,
      password: formData.password,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? tencentCaptchaRandstr.value
        : undefined
    })

    // 第二步的挑战对「需要二次验证」和「凭据无效」是同一个响应：两种人都进卡片里的第二步。
    // 验证码在这一步被用掉了，但这里不重置：第二步不需要它，重置只会让小组件回到「正在验证…」转圈，
    // 登录键白白灰着；回到第一步时（leaveChallenge）再取新的。
    if (isTotp2FARequired(response)) {
      applyLoginChallenge(response as TotpLoginResponse)
      isLoading.value = false
      return
    }

    await finishLoginSuccess()
  } catch (error: unknown) {
    if (isChunkLoadError(error)) {
      recoverFromChunkLoadError((router.currentRoute.value.query.redirect as string) || '/dashboard')
      return
    }
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', t('auth.loginFailed'))

    // Also show error toast
    appStore.showError(errorMessage.value)
    // The proof went to the server with the failed attempt; the next attempt needs a new one. On success the page
    // navigates away, so there is nothing to reset (it used to restart the widget right before the redirect).
    if (captchaEnabled.value) {
      resetCaptchaProof()
    }
  } finally {
    isLoading.value = false
  }
}

async function handlePasskeyLogin(): Promise<void> {
  if (agreementGateActive.value) {
    appStore.showWarning(t('legal.loginAgreementPrompt.loginRequiredWarning'))
    if (loginAgreementMode.value !== 'checkbox') {
      showAgreementModal.value = true
    }
    return
  }

  passkeyLoading.value = true
  try {
    let proof: ActionCaptchaRequestProof | undefined
    if (actionCaptchaEnabled.value) {
      const result = await turnstileRef.value?.verifyAction()
      if (!result) return
      proof = tencentCaptchaEnabled.value
        ? {
            tencent_captcha_ticket: result.token,
            tencent_captcha_randstr: result.randstr
          }
        : { turnstile_token: result.token }
    }

    await authStore.loginWithPasskey(proof)
    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))
    const redirectTo = (router.currentRoute.value.query.redirect as string) || '/dashboard'
    await pushOrReload(router, redirectTo)
  } catch (error: unknown) {
    if (isChunkLoadError(error)) {
      recoverFromChunkLoadError((router.currentRoute.value.query.redirect as string) || '/dashboard')
      return
    }
    const fallback = error instanceof DOMException && error.name === 'NotAllowedError'
      ? t('auth.passkeyCancelled')
      : t('auth.passkeyFailed')
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', fallback)
    appStore.showError(errorMessage.value)
  } finally {
    if (actionCaptchaEnabled.value) {
      resetCaptchaProof()
    }
    passkeyLoading.value = false
  }
}

async function handleOAuthStart(request: OAuthLoginStart): Promise<void> {
  if (authActionDisabled.value) return

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

// ==================== 2FA Handlers ====================

// maskLoginEmail 与服务端 service.MaskEmail 保持同一规则：
// 只在本地兜底展示，服务端没有回显掩码邮箱时不让弹窗缺行。
function maskLoginEmail(email: string): string {
  const trimmed = (email || '').trim()
  if (trimmed.length < 3) return ''
  const at = trimmed.indexOf('@')
  if (at < 1) return trimmed.slice(0, 1) + '***'
  const local = trimmed.slice(0, at)
  const domain = trimmed.slice(at)
  if (local.length <= 2) return local.slice(0, 1) + '***' + domain
  return local.slice(0, 1) + '***' + local.slice(-1) + domain
}

const loginRedirect = (): string =>
  (router.currentRoute.value.query.redirect as string) || '/dashboard'

/** 全部因子通过后：清挑战、提示成功、跳转目标页。 */
async function finishLoginSuccess(): Promise<void> {
  loginChallenge.value = null
  clearAllAffiliateReferralCodes()
  appStore.showSuccess(t('auth.loginSuccess'))
  await pushOrReload(router, loginRedirect())
}

async function handle2FAVerify(code: string): Promise<void> {
  const challenge = loginChallenge.value
  if (!challenge) return
  totpVerifying.value = true
  totpPaneRef.value?.setVerifying(true)

  try {
    // 统一登录协议：第二步同时提交「动态码 + 邮箱 + 密码」，服务端重新校验凭据，
    // 密码错误与需要 2FA 在第一步的响应完全一致，凭据错误统一在这一步报错。
    const response = await authStore.login2FA(
      challenge.tempToken,
      code,
      formData.email,
      formData.password
    )
    if (isTotp2FARequired(response)) {
      // 两者都绑定：TOTP 通过后进入 Passkey 因子，仍不签发令牌。
      applyLoginChallenge(response as TotpLoginResponse)
      totpVerifying.value = false
      totpPaneRef.value?.setVerifying(false)
      return
    }

    // 成功即清：清空前端账号维度尝试计数
    totpPaneRef.value?.resetLimit?.()
    await finishLoginSuccess()
  } catch (error: unknown) {
    if (isChunkLoadError(error)) {
      recoverFromChunkLoadError(loginRedirect())
      return
    }
    const err = error as {
      message?: string
      reason?: string
      retryAfter?: string | number
      response?: { data?: { message?: string }; headers?: Record<string, string> }
    }
    // 第二步同时校验「动态码 + 邮箱 + 密码」：服务端已把「密码/邮箱错误」与
    // 「动态码错误」统一成 INVALID_CREDENTIALS，这里不再按 reason 区分文案，
    // 一律展示同一条提示，避免把「密码是否正确」暴露成可区分的错误文案。
    // 旧版后端可能仍返回 TOTP_INVALID_CODE，同样落到统一提示。
    const message =
      err.reason === 'INVALID_CREDENTIALS' || err.reason === 'TOTP_INVALID_CODE'
        ? t('profile.totp.loginInvalidCredentials')
        : err.response?.data?.message || err.message || t('profile.totp.loginFailed')
    // 服务端 429 的 Retry-After 优先于本地计数（网络层已把 header 解出为 retryAfter）。
    const retryAfterSeconds = Number(err.retryAfter ?? err.response?.headers?.['retry-after'] ?? 0) || 0

    totpVerifying.value = false
    if (totpPaneRef.value) {
      totpPaneRef.value.setError(message, retryAfterSeconds)
      totpPaneRef.value.setVerifying(false)
    }
  }
}

/** Passkey 因子：浏览器断言 → 提交后端；全部因子通过才签发令牌。 */
async function handlePasskeyFactor(): Promise<void> {
  const challenge = loginChallenge.value
  if (!challenge) return
  if (!passkeySupported.value) {
    appStore.showError(t('auth.passkeyUnsupported'))
    return
  }
  if (!challenge.passkeyOptions) {
    appStore.showError(t('auth.passkeyFailed'))
    return
  }
  passkeyVerifying.value = true
  try {
    const credential = await passkeyAPI.acceptLoginChallenge(challenge.passkeyOptions)
    await authStore.login2FAPasskeyFinish(
      challenge.tempToken,
      challenge.passkeySessionToken,
      credential
    )
    await finishLoginSuccess()
  } catch (error: unknown) {
    if (isChunkLoadError(error)) {
      recoverFromChunkLoadError(loginRedirect())
      return
    }
    const fallback =
      error instanceof DOMException && error.name === 'NotAllowedError'
        ? t('auth.passkeyCancelled')
        : t('auth.passkeyFailed')
    appStore.showError(extractI18nErrorMessage(error, t, 'auth.errors', fallback))
  } finally {
    passkeyVerifying.value = false
  }
}

function handle2FACancel(): void {
  leaveChallenge({ refocus: true })
}
</script>

<style scoped>
/* The first time the page shows, the card already plays its own rise; the credentials pane must not play a second
   enter on top of it. After the user has been to step 2 and back, the pane animates in like any other step. */
.auth-step-pane.login-pane-static[data-pane-state='active'] {
  animation: none;
}
</style>
