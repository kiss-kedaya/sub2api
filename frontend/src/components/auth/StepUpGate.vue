<template>
  <TotpStepUpDialog :controller="stepUpGate" />
</template>

<script setup lang="ts">
/**
 * Mounts the application-level TOTP step-up dialog.
 *
 * The Axios response interceptor (src/api/client.ts) retries STEP_UP_REQUIRED
 * responses through this gate, so every sensitive admin call is covered even
 * when a page did not wrap it in useStepUp. Page-local dialogs stay supported;
 * they simply never fire for responses this gate already handled.
 */
import { onBeforeUnmount, onMounted } from 'vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { registerStepUpHost, stepUpGate } from '@/composables/useStepUpGate'

let releaseHost: (() => void) | null = null

onMounted(() => {
  releaseHost = registerStepUpHost()
})

onBeforeUnmount(() => {
  releaseHost?.()
  releaseHost = null
})
</script>
