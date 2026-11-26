<template>
  <!-- DingTalk sign-in shares the auth button surface (SocialLoginButtons). -->
  <SocialLoginButtons
    :providers="providers"
    :disabled="disabled"
    :divider="showDivider"
    :divider-label="t('auth.oauthOrContinue')"
    @select="startLogin"
  >
    <template #icon-dingtalk>
      <DingTalkMark class="size-5 text-[#1677FF] dark:text-[#40A9FF]" />
    </template>
  </SocialLoginButtons>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import DingTalkMark from './DingTalkMark.vue'
import SocialLoginButtons from '@/components/common/SocialLoginButtons.vue'
import type { SocialLoginProvider } from '@/components/common/socialLogin'
import type { OAuthLoginStart } from '@/api/auth'
import { resolveAffiliateReferralCode, storeOAuthAffiliateCode } from '@/utils/oauthAffiliate'

const props = withDefaults(defineProps<{
  disabled?: boolean
  affCode?: string
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
  id: 'dingtalk',
  label: t('auth.dingtalk.signIn'),
  ariaLabel: t('auth.dingtalk.signIn')
}])

function startLogin(): void {
  const redirectTo = (route.query.redirect as string) || '/dashboard'
  storeOAuthAffiliateCode(resolveAffiliateReferralCode(props.affCode, route.query.aff, route.query.aff_code))
  emit('start', { provider: 'dingtalk', params: { redirect: redirectTo } })
}
</script>
