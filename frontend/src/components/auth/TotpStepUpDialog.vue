<template>
  <div v-if="controller.visible.value" class="fixed inset-0 z-[60] overflow-y-auto">
    <div class="flex min-h-full items-center justify-center p-4">
      <div class="fixed inset-0 bg-black/50 transition-opacity" @click="handleCancel"></div>

      <div class="relative w-full max-w-md transform rounded-xl bg-white p-6 shadow-xl transition-all dark:bg-dark-800">
        <div class="mb-6 text-center">
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
            <svg class="h-6 w-6 text-primary-600 dark:text-primary-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
            </svg>
          </div>
          <h3 class="mt-4 text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('stepUp.title') }}
          </h3>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('stepUp.hint') }}
          </p>
        </div>

        <div class="mb-6">
          <TotpCodeInput
            ref="codeInput"
            v-model="code"
            :label="t('profile.totp.enterCode')"
            :disabled="verifying"
          />
          <div v-if="verifying" class="mt-3 flex items-center justify-center gap-2 text-sm text-gray-500">
            <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-primary-500"></div>
            {{ t('common.verifying') }}
          </div>
        </div>

        <button
          type="button"
          class="btn btn-secondary w-full"
          :disabled="verifying"
          @click="handleCancel"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { totpAPI } from '@/api'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import type { StepUpController } from '@/composables/useStepUp'

const props = defineProps<{
  controller: StepUpController
}>()

const { t } = useI18n()
const appStore = useAppStore()

const verifying = ref(false)
const code = ref('')
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)

// Focus the first cell whenever the dialog opens.
watch(
  () => props.controller.visible.value,
  (open) => {
    if (open) {
      resetInputs()
      nextTick(() => codeInput.value?.focus())
    }
  }
)

// Auto-submit once 6 digits are entered.
watch(
  code,
  (newCode) => {
    if (newCode.length === 6 && !verifying.value) {
      submit(newCode)
    }
  }
)

async function submit(otp: string) {
  verifying.value = true
  try {
    await totpAPI.stepUp(otp)
    verifying.value = false
    resetInputs()
    props.controller.onVerified()
  } catch (err: any) {
    verifying.value = false
    appStore.showError(err?.message || t('stepUp.verifyFailed'))
    resetInputs()
    nextTick(() => codeInput.value?.focus())
  }
}

function resetInputs() {
  code.value = ''
}

function handleCancel() {
  if (verifying.value) return
  props.controller.onCancel()
}

</script>
