<template>
  <!-- Generic OIDC sign-in: provider name decides the label, initials the mark. -->
  <SocialLoginButtons
    :providers="providers"
    :disabled="disabled"
    :divider="showDivider"
    :divider-label="t('auth.oauthOrContinue')"
    @select="startLogin"
  >
    <template #icon-oidc>
      <span
        class="inline-flex size-5 items-center justify-center rounded-full bg-primary-100 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
        aria-hidden="true"
      >
        {{ providerInitial }}
      </span>
    </template>
  </SocialLoginButtons>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import SocialLoginButtons from '@/components/common/SocialLoginButtons.vue'
import type { SocialLoginProvider } from '@/components/common/socialLogin'
import type { OAuthLoginStart } from '@/api/auth'
import { resolveAffiliateReferralCode, storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'

const props = withDefaults(defineProps<{
  disabled?: boolean
  affCode?: string
  providerName?: string
  showDivider?: boolean
}>(), {
  providerName: 'OIDC',
  showDivider: true
})
const emit = defineEmits<{
  start: [request: OAuthLoginStart]
}>()

const route = useRoute()
const { t } = useI18n()

const normalizedProviderName = computed(() => {
  const name = props.providerName?.trim()
  return name || 'OIDC'
})

const providerInitial = computed(() => normalizedProviderName.value.charAt(0).toUpperCase() || 'O')

const providers = computed<SocialLoginProvider[]>(() => [{
  id: 'oidc',
  label: t('auth.oidc.signIn', { providerName: normalizedProviderName.value }),
  ariaLabel: t('auth.oidc.signIn', { providerName: normalizedProviderName.value })
}])

function startLogin(): void {
  const redirectTo = (route.query.redirect as string) || '/dashboard'
  storeOAuthAffiliateCode(resolveAffiliateReferralCode(props.affCode, route.query.aff, route.query.aff_code))
  emit('start', { provider: 'oidc', params: { redirect: redirectTo } })
}
</script>
