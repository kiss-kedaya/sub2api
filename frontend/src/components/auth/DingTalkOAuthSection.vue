<template>
  <div class="space-y-4">
    <UButton
      block
      color="neutral"
      variant="subtle"
      :label="t('auth.dingtalk.signIn')"
      :disabled="disabled"
      @click="startLogin"
    >
      <template #leading>
        <svg
          class="size-5 shrink-0"
          viewBox="0 0 24 24"
          xmlns="http://www.w3.org/2000/svg"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="12" fill="#1677FF" />
          <text
            x="12"
            y="17"
            font-family="sans-serif"
            font-size="13"
            font-weight="bold"
            fill="white"
            text-anchor="middle"
          >D</text>
        </svg>
      </template>
    </UButton>

    <USeparator v-if="showDivider" :label="t('auth.oauthOrContinue')" />
  </div>
</template>

<script setup lang="ts">
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
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

function startLogin(): void {
  const redirectTo = (route.query.redirect as string) || '/dashboard'
  storeOAuthAffiliateCode(resolveAffiliateReferralCode(props.affCode, route.query.aff, route.query.aff_code))
  emit('start', { provider: 'dingtalk', params: { redirect: redirectTo } })
}
</script>
