<template>
  <!-- Renders nothing itself: the app store's toasts are mirrored into Nuxt UI's toaster (UApp). -->
  <span v-if="false" />
</template>

<script setup lang="ts">
import { watch } from 'vue'
import { useToast } from '@nuxt/ui/composables/useToast'
import { useAppStore } from '@/stores/app'
import type { Toast as AppToast, ToastType } from '@/types'

const appStore = useAppStore()
const nuxtToast = useToast()

const TOAST_COLOR: Record<ToastType, 'success' | 'error' | 'warning' | 'info'> = {
  success: 'success',
  error: 'error',
  warning: 'warning',
  info: 'info'
}

const TOAST_ICON: Record<ToastType, string> = {
  success: 'i-lucide-circle-check',
  error: 'i-lucide-circle-x',
  warning: 'i-lucide-triangle-alert',
  info: 'i-lucide-info'
}

/**
 * Store toast ids handed to Nuxt UI → whether Nuxt UI has listed them yet.
 * useToast queues additions, so a toast only counts as closed after it was shown once.
 */
const mirrored = new Map<string, boolean>()

function show(toast: AppToast) {
  const type: ToastType = TOAST_COLOR[toast.type] ? toast.type : 'info'
  nuxtToast.add({
    id: toast.id,
    title: toast.title || toast.message,
    description: toast.title ? toast.message : undefined,
    color: TOAST_COLOR[type],
    icon: TOAST_ICON[type],
    // The store owns dismissal (its own timer or hideToast); no duration means it stays until closed.
    duration: toast.duration ?? Number.POSITIVE_INFINITY
  })
  mirrored.set(toast.id, false)
}

// Store → Nuxt UI: show new toasts, drop the ones the store removed (timer, hideToast, clearAllToasts).
watch(
  () => appStore.toasts.map((toast) => toast.id),
  (ids) => {
    const current = new Set(ids)
    for (const id of [...mirrored.keys()]) {
      if (!current.has(id)) {
        mirrored.delete(id)
        nuxtToast.remove(id)
      }
    }
    for (const toast of appStore.toasts) {
      if (!mirrored.has(toast.id)) show(toast)
    }
  },
  { immediate: true }
)

// Nuxt UI → store: closing a toast (close button, swipe) removes it from the store as well.
watch(
  () => nuxtToast.toasts.value.map((toast) => `${toast.id}:${toast.open === false ? 0 : 1}`),
  () => {
    const open = new Set<string>()
    for (const toast of nuxtToast.toasts.value) {
      const id = String(toast.id)
      if (mirrored.has(id)) mirrored.set(id, true)
      if (toast.open !== false) open.add(id)
    }
    for (const [id, shown] of [...mirrored]) {
      if (shown && !open.has(id)) {
        mirrored.delete(id)
        appStore.hideToast(id)
      }
    }
  }
)
</script>
