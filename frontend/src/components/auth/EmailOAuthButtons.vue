<template>
  <div v-if="hasProviders" class="space-y-4">
    <USeparator v-if="showDivider" :label="t('auth.oauthOrContinue')" />

    <div :class="providerGridClass">
      <!-- Provider buttons as in Nuxt UI's UAuthForm: block, neutral, subtle -->
      <UButton
        v-for="provider in visibleProviders"
        :key="provider"
        block
        color="neutral"
        variant="subtle"
        :label="providerLabel(provider)"
        :disabled="disabled"
        @click="startLogin(provider)"
      >
        <template #leading>
          <GitHubMark v-if="provider === 'github'" class="size-5 shrink-0" />
          <GoogleMark v-else class="size-5 shrink-0" />
        </template>
      </UButton>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import GitHubMark from './GitHubMark.vue'
import GoogleMark from './GoogleMark.vue'
import type { OAuthLoginStart } from '@/api/auth'
import { resolveAffiliateReferralCode, storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'

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

const hasProviders = computed(() => visibleProviders.value.length > 0)
const hasMultipleProviders = computed(() => visibleProviders.value.length > 1)
const providerGridClass = computed(() => [
  'grid',
  'grid-cols-1',
  'gap-3',
  hasMultipleProviders.value ? 'sm:grid-cols-2' : ''
])

function providerLabel(provider: EmailOAuthProvider): string {
  const name = provider === 'github' ? 'GitHub' : 'Google'
  return hasMultipleProviders.value ? name : t('auth.emailOAuth.signIn', { providerName: name })
}

function startLogin(provider: EmailOAuthProvider): void {
  const redirectTo = (route.query.redirect as string) || '/dashboard'
  const affiliateCode = resolveAffiliateReferralCode(props.affCode, route.query.aff, route.query.aff_code)
  storeOAuthAffiliateCode(affiliateCode)
  window.sessionStorage.setItem(EMAIL_OAUTH_PENDING_PROVIDER_KEY, provider)
  const params: Record<string, string> = { redirect: redirectTo }
  if (affiliateCode) {
    params.aff_code = affiliateCode
  }
  const promoCode = props.promoCode?.trim()
  if (promoCode) {
    params.promo_code = promoCode
  }
  emit('start', { provider, params })
}
</script>
