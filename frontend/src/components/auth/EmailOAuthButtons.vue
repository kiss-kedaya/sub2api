<template>
  <!--
    GitHub / Google sign-in. The button list, spacing and shared radius live in
    SocialLoginButtons; this wrapper keeps the OAuth request + label rules.
  -->
  <SocialLoginButtons
    :providers="providers"
    :disabled="disabled"
    :divider="showDivider"
    :divider-label="t('auth.oauthOrContinue')"
    @select="startLogin"
  >
    <template #icon-github>
      <GitHubMark class="size-5 text-highlighted" />
    </template>
    <template #icon-google>
      <GoogleMark class="size-5" />
    </template>
  </SocialLoginButtons>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import GitHubMark from './GitHubMark.vue'
import GoogleMark from './GoogleMark.vue'
import SocialLoginButtons from '@/components/common/SocialLoginButtons.vue'
import type { SocialLoginProvider } from '@/components/common/socialLogin'
import type { OAuthLoginStart } from '@/api/auth'
import { resolveAffiliateReferralCode, storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'
import { sanitizeInternalRedirectPath } from '@/utils/url'

type EmailOAuthProvider = 'github' | 'google'
const EMAIL_OAUTH_PENDING_PROVIDER_KEY = 'email_oauth_pending_provider'

const props = withDefaults(defineProps<{
  disabled?: boolean
  affCode?: string
  promoCode?: string
  githubEnabled?: boolean
  googleEnabled?: boolean
  showDivider?: boolean
}>(), {
  showDivider: true
})
const emit = defineEmits<{
  start: [request: OAuthLoginStart]
}>()

const route = useRoute()
const { t } = useI18n()

const visibleProviders = computed<EmailOAuthProvider[]>(() => {
  const providers: EmailOAuthProvider[] = []
  if (props.githubEnabled) providers.push('github')
  if (props.googleEnabled) providers.push('google')
  return providers
})

const hasMultipleProviders = computed(() => visibleProviders.value.length > 1)

function providerLabel(provider: EmailOAuthProvider): string {
  const name = provider === 'github' ? 'GitHub' : 'Google'
  return hasMultipleProviders.value ? name : t('auth.emailOAuth.signIn', { providerName: name })
}

const providers = computed<SocialLoginProvider[]>(() =>
  visibleProviders.value.map(provider => ({
    id: provider,
    label: providerLabel(provider),
    ariaLabel: t('auth.emailOAuth.signIn', { providerName: provider === 'github' ? 'GitHub' : 'Google' })
  }))
)

function startLogin(provider: string): void {
  const emailProvider = provider as EmailOAuthProvider
  const redirectTo = sanitizeInternalRedirectPath(route.query.redirect, '/dashboard')
  const affiliateCode = resolveAffiliateReferralCode(props.affCode, route.query.aff, route.query.aff_code)
  storeOAuthAffiliateCode(affiliateCode)
  window.sessionStorage.setItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY, emailProvider)
  const params: Record<string, string> = { redirect: redirectTo }
  if (affiliateCode) {
    params.aff_code = affiliateCode
  }
  const promoCode = props.promoCode?.trim()
  if (promoCode) {
    params.promo_code = promoCode
  }
  emit('start', { provider: emailProvider, params })
}
</script>
