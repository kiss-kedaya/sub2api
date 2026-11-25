<template>
  <!-- 登录第二步（Passkey）：卡片内的一格。标题、步骤头和「上一步」由登录页给。 -->
  <div class="space-y-4" data-testid="login-passkey-pane">
    <p v-if="userEmailMasked" class="text-[13px] text-muted" data-testid="login-factor-account">
      {{ userEmailMasked }}
    </p>

    <div
      v-if="!supported"
      class="rounded-xl bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      data-testid="passkey-login-unsupported"
    >
      {{ t('auth.passkeyUnsupported') }}
    </div>

    <button
      type="button"
      class="btn btn-primary w-full"
      data-testid="passkey-login-verify"
      :disabled="verifying || !supported"
      @click="$emit('verify')"
    >
      <svg
        v-if="verifying"
        class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
        fill="none"
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
        <path
          class="opacity-75"
          fill="currentColor"
          d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
        ></path>
      </svg>
      {{ verifying ? t('auth.passkeyVerifying') : t('auth.passkeyVerify') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{
  supported: boolean
  verifying: boolean
  userEmailMasked?: string
}>()

defineEmits<{
  verify: []
}>()

const { t } = useI18n()
</script>
