<template>
  <div class="fixed inset-0 z-50 overflow-y-auto">
    <div class="flex min-h-full items-start justify-center p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:items-center">
      <div class="fixed inset-0 bg-black/50 transition-opacity"></div>

      <div class="relative w-full max-w-md transform rounded-xl bg-white p-6 shadow-xl dark:bg-dark-800">
        <div class="mb-6 text-center">
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
            <svg class="h-6 w-6 text-primary-600 dark:text-primary-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-18 0m-1.5 0a9 9 0 0118 0m-9 7.5v6m-3-3h6" />
            </svg>
          </div>
          <h3 class="mt-4 text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('auth.passkeyLoginTitle') }}
          </h3>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('auth.passkeyLoginHint') }}
          </p>
          <p v-if="userEmailMasked" class="mt-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ userEmailMasked }}
          </p>
        </div>

        <div
          v-if="!supported"
          class="mb-6 rounded-lg bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
          data-testid="passkey-login-unsupported"
        >
          {{ t('auth.passkeyUnsupported') }}
        </div>

        <div v-if="verifying" class="mb-6 flex items-center justify-center gap-2 text-sm text-gray-500">
          <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-primary-500"></div>
          {{ t('auth.passkeyVerifying') }}
        </div>

        <div class="mb-3">
          <button
            type="button"
            class="btn btn-primary w-full"
            data-testid="passkey-login-verify"
            :disabled="verifying || !supported"
            @click="$emit('verify')"
          >
            {{ verifying ? t('auth.passkeyVerifying') : t('auth.passkeyVerify') }}
          </button>
        </div>
        <button
          type="button"
          class="btn btn-secondary w-full"
          :disabled="verifying"
          @click="$emit('cancel')"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </div>
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
  cancel: []
}>()

const { t } = useI18n()
</script>
