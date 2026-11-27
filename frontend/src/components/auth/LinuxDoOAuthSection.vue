<template>
  <!-- Linux.do sign-in shares the auth button surface (SocialLoginButtons). -->
  <SocialLoginButtons
    :providers="providers"
    :disabled="disabled"
    :divider="showDivider"
    :divider-label="t('auth.oauthOrContinue')"
    @select="startLogin"
  >
    <template #icon-linuxdo>
      <LinuxDoMark class="size-5" />
    </template>
  </SocialLoginButtons>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import LinuxDoMark from './LinuxDoMark.vue'
import SocialLoginButtons from '@/components/common/SocialLoginButtons.vue'
import type { SocialLoginProvider } from '@/components/common/socialLogin'
import type { OAuthLoginStart } from '@/api/auth'
import { resolveAffiliateReferralCode, storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'
import { sanitizeInternalRedirectPath } from '@/utils/url'

const props = withDefaults(defineProps<{
  disabled?: boolean
  affCode?: string
  promoCode?: string
  showDivider?: boolean
}>(), {
  showDivider: true
})
const emit = defineEmits<{
  start: [request: OAuthLoginStart]
}>()

const route = useRoute()
const { t } = useI18n()

const providers = computed<SocialLoginProvider[]>(() => [{
  id: 'linuxdo',
  label: t('auth.linuxdo.signIn'),
  ariaLabel: t('auth.linuxdo.signIn')
}])

function startLogin(): void {
  const redirectTo = sanitizeInternalRedirectPath(route.query.redirect, '/dashboard')
  storeOAuthAffiliateCode(resolveAffiliateReferralCode(props.affCode, route.query.aff, route.query.aff_code))
  const params: Record<string, string> = { redirect: redirectTo }
  const promoCode = props.promoCode?.trim()
  if (promoCode) {
    params.promo_code = promoCode
  }
  emit('start', { provider: 'linuxdo', params })
}
</script>
