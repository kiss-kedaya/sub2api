<template>
  <!-- Second sign-in step: a Nuxt UI style dialog with the same header as the sign-in card -->
  <div class="fixed inset-0 z-50 overflow-y-auto">
    <div class="flex min-h-full items-center justify-center p-4">
      <div class="fixed inset-0 bg-elevated/75"></div>

      <div role="dialog" aria-modal="true" class="relative w-full max-w-sm space-y-6 rounded-lg bg-default p-6 shadow-lg ring ring-default">
        <div class="flex flex-col text-center">
          <div class="mb-2">
            <UIcon name="i-lucide-shield-check" class="inline-block size-8 text-default" />
          </div>
          <h2 class="text-xl font-semibold text-highlighted">{{ t('profile.totp.loginTitle') }}</h2>
          <p class="mt-1 text-base text-muted">{{ t('profile.totp.loginHint') }}</p>
          <p v-if="userEmailMasked" class="mt-1 text-sm font-medium text-highlighted">
            {{ userEmailMasked }}
          </p>
        </div>

        <div>
          <TotpCodeInput
            ref="codeInput"
            v-model="code"
            :label="t('profile.totp.enterCode')"
            :disabled="verifying"
            @complete="submit"
          />
          <p v-if="verifying" class="mt-3 flex items-center justify-center gap-2 text-sm text-muted">
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
            {{ t('common.verifying') }}
          </p>
        </div>

        <UButton
          block
          color="neutral"
          variant="subtle"
          :label="t('common.cancel')"
          :disabled="verifying"
          @click="$emit('cancel')"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'

defineProps<{
  tempToken: string
  userEmailMasked?: string
}>()

const emit = defineEmits<{
  verify: [code: string]
  cancel: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const verifying = ref(false)
const code = ref('')
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)

function submit(otp: string) {
  if (verifying.value || !/^[0-9]{6}$/.test(otp)) return
  verifying.value = true
  emit('verify', otp)
}

defineExpose({
  setVerifying: (value: boolean) => { verifying.value = value },
  setError: (message: string) => {
    verifying.value = false
    if (message) {
      appStore.showError(message)
    }
    code.value = ''
    nextTick(() => {
      codeInput.value?.focus()
    })
  }
})

</script>
