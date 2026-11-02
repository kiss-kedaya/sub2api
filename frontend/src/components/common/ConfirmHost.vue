<template>
  <ConfirmDialog
    :show="state.open"
    :title="state.title || t('common.confirm')"
    :message="state.message"
    :confirm-text="state.confirmText || undefined"
    :cancel-text="state.cancelText || undefined"
    :danger="state.danger"
    @confirm="settleConfirmation(true)"
    @cancel="settleConfirmation(false)"
  />
</template>

<script setup lang="ts">
import { getCurrentInstance, onBeforeUnmount, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Router } from 'vue-router'
import ConfirmDialog from './ConfirmDialog.vue'
import {
  cancelConfirmation,
  registerConfirmationHost,
  settleConfirmation,
  useConfirmation
} from '@/composables/useConfirmation'

// Single host for every async confirmation request. Escape, a backdrop click and
// the Cancel button all resolve the request with `false`; a route change cancels
// whatever is still waiting so a dialog can never outlive the page that asked.
const { t } = useI18n()
const { state } = useConfirmation()

let unregisterHost: (() => void) | null = null
let stopRouteWatch: (() => void) | null = null

onMounted(() => {
  unregisterHost = registerConfirmationHost()

  const router = getCurrentInstance()?.appContext.config.globalProperties.$router as
    | Router
    | undefined
  if (router) {
    stopRouteWatch = watch(
      () => router.currentRoute.value.fullPath,
      () => cancelConfirmation()
    )
  }
})

onBeforeUnmount(() => {
  stopRouteWatch?.()
  stopRouteWatch = null
  unregisterHost?.()
  unregisterHost = null
})
</script>
