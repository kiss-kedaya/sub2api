<template>
  <div class="min-h-dvh bg-(--app-page-bg) px-4 py-10">
    <div class="mx-auto max-w-2xl">
      <div v-if="isProcessing" class="card p-6 text-center">
        <div class="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-primary border-t-transparent motion-reduce:animate-none motion-reduce:opacity-75"></div>
        <h1 class="mt-4 text-lg font-semibold text-highlighted">
          {{ t('auth.oauth.callbackTitle') }}
        </h1>
        <p class="mt-2 text-sm text-muted">
          {{ t('auth.oauth.callbackHint') }}
        </p>
      </div>

      <div v-else-if="needsRegistrationCompletion" class="card p-6">
        <h1 class="text-lg font-semibold text-highlighted">
          {{ t('auth.oidc.callbackTitle', { providerName }) }}
        </h1>
        <p class="mt-2 text-sm text-muted">
          {{ registrationHint }}
        </p>

        <div class="mt-6 space-y-4">
          <div>
            <label class="input-label">{{ t('auth.emailLabel') }}</label>
            <input
              class="input w-full"
              type="email"
              :value="registrationEmail"
              readonly
              disabled
            />
          </div>
          <div>
            <label class="input-label">{{ t('auth.passwordLabel') }}</label>
            <input
              v-model="password"
              type="password"
              class="input w-full"
              :placeholder="t('auth.createPasswordPlaceholder')"
              :disabled="isSubmitting"
              autocomplete="new-password"
              @keyup.enter="handleSubmitRegistration"
            />
          </div>
          <div>
            <label class="input-label">{{ t('auth.confirmPassword') }}</label>
            <input
              v-model="confirmPassword"
              type="password"
              class="input w-full"
              :placeholder="t('auth.confirmPasswordPlaceholder')"
              :disabled="isSubmitting"
              autocomplete="new-password"
              @keyup.enter="handleSubmitRegistration"
            />
          </div>
          <div v-if="invitationRequired">
            <label class="input-label">{{ t('auth.invitationCodeLabel') }}</label>
            <input
              v-model="invitationCode"
              type="text"
              class="input w-full"
              :placeholder="t('auth.invitationCodePlaceholder')"
              :disabled="isSubmitting"
              @keyup.enter="handleSubmitRegistration"
            />
          </div>
          <p v-if="registrationError" class="text-sm text-error">
            {{ registrationError }}
          </p>
          <button
            class="btn btn-primary w-full [@media(pointer:coarse)]:min-h-11"
            type="button"
            :disabled="isSubmitting || !canSubmitRegistration"
            @click="handleSubmitRegistration"
          >
            {{ isSubmitting ? t('common.processing') : t('auth.oidc.completeRegistration') }}
          </button>
        </div>
      </div>

      <div v-else-if="needsTotpChallenge" class="card p-6">
        <h1 class="text-lg font-semibold text-highlighted">
          {{ t('auth.oidc.callbackTitle', { providerName }) }}
        </h1>
        <template v-if="twoFactorNextFactor === 'passkey'">
          <div class="mt-6 space-y-3">
            <PasskeyFactorPane
              :supported="passkeySupported"
              :verifying="passkeyVerifying"
              :user-email-masked="totpUserEmailMasked"
              @verify="handlePasskeyFactorVerify"
            />
            <p v-if="totpError" class="text-sm text-error">
              {{ totpError }}
            </p>
          </div>
        </template>
        <template v-else>
          <p class="mt-2 text-sm text-muted">
            {{
              t('auth.oauthFlow.totpHint', {
                providerName,
                account: totpUserEmailMasked || t('auth.oauthFlow.yourAccount')
              })
            }}
          </p>

          <div class="mt-6 space-y-3">
            <input
              v-model="totpCode"
              data-testid="oauth-callback-totp"
              type="text"
              inputmode="numeric"
              maxlength="6"
              class="input w-full"
              placeholder="123456"
              :disabled="isSubmitting"
              @keyup.enter="handleSubmitTotpChallenge"
            />
            <p v-if="totpError" class="text-sm text-error">
              {{ totpError }}
            </p>
            <button
              data-testid="oauth-callback-totp-submit"
              class="btn btn-primary w-full [@media(pointer:coarse)]:min-h-11"
              type="button"
              :disabled="isSubmitting || totpCode.trim().length !== 6"
              @click="handleSubmitTotpChallenge"
            >
              {{ isSubmitting ? t('common.processing') : t('auth.oauthFlow.verifyAndContinue') }}
            </button>
          </div>
        </template>
      </div>

      <div v-else-if="invalidCallback" class="card p-6 text-center">
        <h1 class="text-lg font-semibold text-highlighted">
          {{ t('auth.oauth.invalidCallbackTitle') }}
        </h1>
        <p class="mt-2 text-sm text-muted">
          {{ callbackError || t('auth.oauth.invalidCallbackHint') }}
        </p>
        <button class="btn btn-primary mt-6 [@media(pointer:coarse)]:min-h-11" type="button" @click="router.replace('/login')">
          {{ t('auth.backToLogin') }}
        </button>
      </div>

      <div v-else class="card p-6">
        <h1 class="text-lg font-semibold text-highlighted">
          {{ t('auth.oauth.callbackTitle') }}
        </h1>
        <p class="mt-2 text-sm text-muted">
          {{ t('auth.oauth.callbackHint') }}
        </p>

        <div class="mt-6 space-y-4">
          <div>
            <label class="input-label">{{ t('auth.oauth.code') }}</label>
            <div class="flex gap-2">
              <input class="input min-w-0 flex-1 font-mono text-base sm:text-sm" :value="code" readonly />
              <button class="btn btn-secondary [@media(pointer:coarse)]:min-h-11" type="button" :disabled="!code" @click="copy(code)">
                {{ t('common.copy') }}
              </button>
            </div>
          </div>

          <div>
            <label class="input-label">{{ t('auth.oauth.state') }}</label>
            <div class="flex gap-2">
              <input class="input min-w-0 flex-1 font-mono text-base sm:text-sm" :value="state" readonly />
              <button
                class="btn btn-secondary [@media(pointer:coarse)]:min-h-11"
                type="button"
                :disabled="!state"
                @click="copy(state)"
              >
                {{ t('common.copy') }}
              </button>
            </div>
          </div>

          <div>
            <label class="input-label">{{ t('auth.oauth.fullUrl') }}</label>
            <div class="flex gap-2">
              <input class="input min-w-0 flex-1 font-mono text-base sm:text-xs" :value="fullUrl" readonly />
              <button
                class="btn btn-secondary [@media(pointer:coarse)]:min-h-11"
                type="button"
                :disabled="!fullUrl"
                @click="copy(fullUrl)"
              >
                {{ t('common.copy') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore, useAuthStore } from '@/stores'
import PasskeyFactorPane from '@/components/auth/PasskeyFactorPane.vue'
import { apiClient } from '@/api/client'
import { buildApiUrl } from '@/api/url'
import {
  exchangeOAuthLoginCode,
  exchangePendingOAuthCompletion,
  isOAuthLoginCompletion,
  isTotp2FARequired,
  login2FAPasskeyFinish,
  persistOAuthTokenContext,
  type OAuthTokenResponse
} from '@/api/auth'
import { passkeyAPI } from '@/api/passkey'
import type { TotpLoginResponse } from '@/types'
import {
  clearAllAffiliateReferralCodes,
  loadOAuthAffiliateCode,
  oauthAffiliatePayload
} from '@/utils/oauthAffiliate'
import { consumeOAuthFragment } from '@/utils/oauthCallback'
import { sanitizeInternalRedirectPath } from '@/utils/url'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()
const authStore = useAuthStore()
const isProcessing = ref(false)
const isSubmitting = ref(false)
const needsRegistrationCompletion = ref(false)
const invitationRequired = ref(false)
const registrationEmail = ref('')
const password = ref('')
const confirmPassword = ref('')
const invitationCode = ref('')
const registrationError = ref('')
const pendingProvider = ref<'github' | 'google'>('github')
const redirectTo = ref('/dashboard')
const needsTotpChallenge = ref(false)
const totpTempToken = ref('')
const totpCode = ref('')
const totpError = ref('')
const totpUserEmailMasked = ref('')
const twoFactorNextFactor = ref<'totp' | 'passkey'>('totp')
const passkeySessionToken = ref('')
const passkeyOptions = ref<{ publicKey: Record<string, unknown> } | undefined>(undefined)
const passkeyVerifying = ref(false)
const passkeySupported = computed(() => passkeyAPI.isSupported())
const invalidCallback = ref(false)
const callbackError = ref('')
const EMAIL_OAUTH_PENDING_PROVIDER_KEY = 'email_oauth_pending_provider'

type EmailOAuthPendingCompletion = Partial<OAuthTokenResponse> & {
  error?: string
  provider?: string
  redirect?: string
  email?: string
  resolved_email?: string
  invitation_required?: boolean
  binding?: boolean
  requires_2fa?: boolean
  temp_token?: string
  user_email_masked?: string
  required_factors?: string[]
  passed_factors?: string[]
  next_factor?: string
  passkey_session_token?: string
  passkey_options?: { publicKey: Record<string, unknown> }
}

const code = computed(() => (route.query.code as string) || '')
const state = computed(() => (route.query.state as string) || '')
const error = computed(
  () => (route.query.error as string) || (route.query.error_description as string) || ''
)

const fullUrl = computed(() => {
  if (typeof window === 'undefined') return ''
  return window.location.href
})
const providerName = computed(() =>
  pendingProvider.value === 'google' ? 'Google' : 'GitHub'
)
const registrationHint = computed(() =>
  invitationRequired.value
    ? t('auth.oidc.invitationRequired', { providerName: providerName.value })
    : t('auth.oidc.completeRegistration')
)
const canSubmitRegistration = computed(() => {
  if (!registrationEmail.value.trim()) return false
  if (password.value.length < 6) return false
  if (password.value !== confirmPassword.value) return false
  if (invitationRequired.value && !invitationCode.value.trim()) return false
  return true
})

function parseFragmentParams(): URLSearchParams {
  return consumeOAuthFragment()
}

function readTokenResponse(params: URLSearchParams): OAuthTokenResponse | null {
  const accessToken = params.get('access_token')?.trim() || ''
  const sessionManaged = params.get('session_managed') === '1'
  if (!accessToken && !sessionManaged) return null

  const response: OAuthTokenResponse = { access_token: accessToken, ...(sessionManaged ? { session_managed: true } : {}) }
  const refreshToken = params.get('refresh_token')?.trim() || ''
  if (refreshToken) response.refresh_token = refreshToken
  const expiresIn = Number.parseInt(params.get('expires_in')?.trim() || '', 10)
  if (Number.isFinite(expiresIn) && expiresIn > 0) response.expires_in = expiresIn
  const tokenType = params.get('token_type')?.trim() || ''
  if (tokenType) response.token_type = tokenType
  return response
}

function sanitizeRedirectPath(path: unknown): string {
  return sanitizeInternalRedirectPath(path, '/dashboard')
}
function readPendingEmailOAuthProvider(): 'github' | 'google' | null {
  if (typeof window === 'undefined') return null
  const provider = window.sessionStorage.getItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY)
  if (provider === 'github' || provider === 'google') return provider
  return null
}

// applyTotpChallenge 处理"已有账号已启用 2FA（TOTP / Passkey / 双因子）"的第三方登录挑战：
// 后端只下发一次性 temp_token 与当前待验因子，全部必需因子通过后才能拿到会话。
function applyTotpChallenge(completion: {
  requires_2fa?: boolean
  temp_token?: string
  user_email_masked?: string
  required_factors?: string[]
  passed_factors?: string[]
  next_factor?: string
  passkey_session_token?: string
  passkey_options?: { publicKey: Record<string, unknown> }
  provider?: string
  redirect?: string
}): boolean {
  if (completion.requires_2fa !== true || !completion.temp_token) return false
  const required = completion.required_factors?.length ? completion.required_factors : ['totp']
  const passed = completion.passed_factors ?? []
  const next = completion.next_factor || required.find((factor) => !passed.includes(factor)) || 'totp'

  needsTotpChallenge.value = true
  totpTempToken.value = completion.temp_token
  totpCode.value = ''
  totpError.value = ''
  totpUserEmailMasked.value = completion.user_email_masked || ''
  twoFactorNextFactor.value = next === 'passkey' ? 'passkey' : 'totp'
  passkeySessionToken.value = completion.passkey_session_token || ''
  passkeyOptions.value = completion.passkey_options
  const provider = String(completion.provider || '').toLowerCase()
  if (provider === 'github' || provider === 'google') pendingProvider.value = provider
  if (completion.redirect) redirectTo.value = sanitizeRedirectPath(completion.redirect)
  isProcessing.value = false
  return true
}

async function handleSubmitTotpChallenge() {
  totpError.value = ''
  const code = totpCode.value.trim()
  if (!totpTempToken.value || code.length !== 6) return

  isSubmitting.value = true
  try {
    const response = await authStore.login2FA(totpTempToken.value, code)
    if (response && isTotp2FARequired(response)) {
      applyTotpChallenge(response as TotpLoginResponse)
      return
    }
    if (typeof window !== 'undefined') {
      window.sessionStorage.removeItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY)
    }
    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))
    await router.replace(sanitizeRedirectPath(redirectTo.value))
  } catch (e: unknown) {
    const err = e as { message?: string; response?: { data?: { message?: string } } }
    totpError.value = err.response?.data?.message || err.message || t('auth.loginFailed')
  } finally {
    isSubmitting.value = false
  }
}

async function handlePasskeyFactorVerify() {
  totpError.value = ''
  if (!passkeySupported.value) {
    totpError.value = t('auth.passkeyUnsupported')
    return
  }
  if (!totpTempToken.value || !passkeySessionToken.value || !passkeyOptions.value) {
    totpError.value = t('auth.passkeyFailed')
    return
  }

  passkeyVerifying.value = true
  try {
    const credential = await passkeyAPI.acceptLoginChallenge(passkeyOptions.value)
    const completion = await login2FAPasskeyFinish({
      temp_token: totpTempToken.value,
      session_token: passkeySessionToken.value,
      credential
    })
    await finalizeTokenResponse(completion, redirectTo.value)
  } catch (e: unknown) {
    const fallback =
      e instanceof DOMException && e.name === 'NotAllowedError'
        ? t('auth.passkeyCancelled')
        : t('auth.passkeyFailed')
    const err = e as { message?: string; response?: { data?: { message?: string } } }
    totpError.value = err.response?.data?.message || err.message || fallback
  } finally {
    passkeyVerifying.value = false
  }
}

function redirectProviderCallbackToBackend(provider: 'github' | 'google'): void {
  if (typeof window === 'undefined') return
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(route.query)) {
    if (Array.isArray(value)) {
      value.forEach((item) => {
        if (item != null) params.append(key, String(item))
      })
    } else if (value != null) {
      params.set(key, String(value))
    }
  }
  const suffix = params.toString() ? `?${params.toString()}` : ''
  window.location.href = buildApiUrl(`/auth/oauth/${provider}/callback${suffix}`)
}

async function finalizeTokenResponse(tokenResponse: OAuthTokenResponse, redirect: string) {
  persistOAuthTokenContext(tokenResponse)
  if (tokenResponse.session_managed) await authStore.adoptCookieSession()
  else await authStore.setToken(tokenResponse.access_token)
  if (typeof window !== 'undefined') {
    window.sessionStorage.removeItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY)
  }
  clearAllAffiliateReferralCodes()
  appStore.showSuccess(t('auth.loginSuccess'))
  await router.replace(sanitizeRedirectPath(redirect))
}

function hasOAuthTokenResponse(value: Partial<OAuthTokenResponse>): value is OAuthTokenResponse {
  return isOAuthLoginCompletion(value)
}

async function resumePendingEmailOAuth() {
  isProcessing.value = true
  try {
    const completion = await exchangePendingOAuthCompletion() as EmailOAuthPendingCompletion
    const completionRedirect = completion.redirect || '/dashboard'
    if (applyTotpChallenge(completion)) {
      return
    }
    if (hasOAuthTokenResponse(completion)) {
      await finalizeTokenResponse(completion, completionRedirect)
      return
    }

    const provider = String(completion.provider || '').toLowerCase()
    if (provider === 'github' || provider === 'google') {
      pendingProvider.value = provider
    }
    redirectTo.value = sanitizeRedirectPath(completionRedirect)

    if (completion.error === 'invitation_required' || completion.error === 'registration_completion_required') {
      invitationRequired.value = completion.error === 'invitation_required' || completion.invitation_required === true
      registrationEmail.value = String(completion.resolved_email || completion.email || '').trim()
      needsRegistrationCompletion.value = true
      isProcessing.value = false
      return
    }

    if (completion.binding === true) {
      if (typeof window !== 'undefined') {
        window.sessionStorage.removeItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY)
      }
      clearAllAffiliateReferralCodes()
      appStore.showSuccess(t('profile.authBindings.bindSuccess'))
      await router.replace(sanitizeRedirectPath(completionRedirect))
      return
    }

    callbackError.value = completion.error || t('auth.loginFailed')
    invalidCallback.value = true
  } catch (e: unknown) {
    const err = e as { message?: string; response?: { data?: { message?: string } } }
    const message = err.response?.data?.message || err.message || t('auth.loginFailed')
    appStore.showError(message)
    invalidCallback.value = true
  } finally {
    if (!needsRegistrationCompletion.value) {
      isProcessing.value = false
    }
  }
}

async function handleSubmitRegistration() {
  registrationError.value = ''
  if (!registrationEmail.value.trim()) {
    registrationError.value = t('auth.emailRequired')
    return
  }
  if (password.value.length < 6) {
    registrationError.value = t('auth.passwordMinLength')
    return
  }
  if (password.value !== confirmPassword.value) {
    registrationError.value = t('auth.passwordsDoNotMatch')
    return
  }
  const code = invitationCode.value.trim()
  if (invitationRequired.value && !code) return

  isSubmitting.value = true
  try {
    const payload: { password: string; invitation_code?: string; aff_code?: string } = {
      password: password.value,
      ...oauthAffiliatePayload(loadOAuthAffiliateCode())
    }
    if (invitationRequired.value) {
      payload.invitation_code = code
    }
    const { data } = await apiClient.post<OAuthTokenResponse>(
      `/auth/oauth/${pendingProvider.value}/complete-registration`,
      payload
    )
    await finalizeTokenResponse(data, redirectTo.value)
  } catch (e: unknown) {
    const err = e as { message?: string; response?: { data?: { message?: string } } }
    registrationError.value =
      err.response?.data?.message || err.message || t('auth.oidc.completeRegistrationFailed')
  } finally {
    isSubmitting.value = false
  }
}

onMounted(async () => {
  if (error.value) return
  const params = parseFragmentParams()
  // 已有账号已启用 TOTP：后端不签发会话，必须先过动态码。
  if (
    applyTotpChallenge({
      requires_2fa: params.get('requires_2fa') === '1',
      temp_token: params.get('temp_token') || '',
      user_email_masked: params.get('user_email_masked') || '',
      required_factors: params.get('required_factors')
        ? params.get('required_factors')!.split(',').map((item) => item.trim()).filter(Boolean)
        : undefined,
      next_factor: params.get('next_factor') || undefined,
      provider: params.get('provider') || '',
      redirect: params.get('redirect') || ''
    })
  ) {
    return
  }
  const tokenResponse = readTokenResponse(params)
  const oneTimeCode = params.get('code')?.trim() || ''
  const fragmentError = params.get('error') || ''
  const fragmentErrorDescription =
    params.get('error_description') || params.get('error_message') || ''

  if (fragmentError) {
    callbackError.value = fragmentErrorDescription || fragmentError
    invalidCallback.value = true
    return
  }
  // New flow: the backend only put a single-use, 60-second code in the URL.
  // Exchange it immediately (the fragment was already cleared above) and never
  // keep the code in history / screenshots any longer than this request.
  if (oneTimeCode) {
    isProcessing.value = true
    try {
      const exchanged = await exchangeOAuthLoginCode(oneTimeCode)
      await finalizeTokenResponse(
        {
          access_token: exchanged.access_token,
          refresh_token: exchanged.refresh_token,
          expires_in: exchanged.expires_in,
          token_type: exchanged.token_type
        },
        exchanged.redirect || params.get('redirect') || '/dashboard'
      )
    } catch (error: unknown) {
      const message = (error as { message?: string })?.message || t('auth.loginFailed')
      callbackError.value = message
      invalidCallback.value = true
      isProcessing.value = false
    }
    return
  }
  if (!tokenResponse) {
    if (route.path === '/auth/oauth/callback') {
      const pendingEmailOAuthProvider = readPendingEmailOAuthProvider()
      if (pendingEmailOAuthProvider && code.value && state.value) {
        redirectProviderCallbackToBackend(pendingEmailOAuthProvider)
        return
      }
      await resumePendingEmailOAuth()
    }
    return
  }

  isProcessing.value = true
  try {
    await finalizeTokenResponse(tokenResponse, params.get('redirect') || '/dashboard')
  } catch (error: unknown) {
    const message = (error as { message?: string })?.message || t('auth.loginFailed')
    callbackError.value = message
    invalidCallback.value = true
    isProcessing.value = false
  }
})

watch(
  error,
  (message) => {
    if (message) {
      callbackError.value = String(route.query.error_message || route.query.error_description || message)
      invalidCallback.value = true
      isProcessing.value = false
    }
  },
  { immediate: true }
)

const copy = (value: string) => {
  if (!value) return
  copyToClipboard(value)
}
</script>
