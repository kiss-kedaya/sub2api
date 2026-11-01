<template>
  <BaseDialog
    :show="controller.visible.value"
    :title="t('stepUp.title')"
    width="narrow"
    :show-close-button="false"
    :close-on-escape="!verifying"
    :close-on-click-outside="!verifying"
    @close="handleCancel"
  >
    <div class="mb-6 text-center">
      <div class="mx-auto flex size-12 items-center justify-center rounded-full bg-primary/10 ring ring-inset ring-primary/25">
        <UIcon name="i-lucide-lock-keyhole" class="size-6 text-primary" />
      </div>
      <p class="mt-3 text-sm text-muted">
        {{ hint || t('stepUp.hint') }}
      </p>
    </div>

    <div class="mb-6">
      <TotpCodeInput
        ref="codeInput"
        v-model="code"
        :label="t('profile.totp.enterCode')"
        :disabled="verifying"
        @complete="submit"
      />
      <div v-if="verifying" class="mt-3 flex items-center justify-center gap-2 text-sm text-muted">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
        {{ t('common.verifying') }}
      </div>
    </div>

    <UButton
      color="neutral"
      variant="subtle"
      block
      :label="t('common.cancel')"
      :disabled="verifying"
      @click="handleCancel"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { totpAPI } from '@/api'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { StepUpController } from '@/composables/useStepUp'

const props = defineProps<{
  controller: StepUpController
  verify?: (code: string) => Promise<void>
  hint?: string
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

async function submit(otp: string) {
  if (!props.controller.visible.value || verifying.value || !/^[0-9]{6}$/.test(otp)) return
  verifying.value = true
  try {
    if (props.verify) await props.verify(otp)
    else await totpAPI.stepUp(otp)
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
