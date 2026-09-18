import { computed } from 'vue'
import { useRoute } from 'vue-router'

const signalPaths = new Set([
  '/dashboard', '/keys', '/usage', '/monitor', '/available-channels',
  '/purchase', '/orders', '/subscriptions', '/redeem',
  '/profile', '/affiliate', '/ip-allowlist',
  '/payment/qrcode', '/payment/result', '/payment/stripe', '/payment/airwallex',
])

export function useConsoleSignal() {
  const route = useRoute()
  const isAdminSignal = computed(() => (route.matched.at(-1)?.path || route.path).startsWith('/admin/'))
  const isConsoleSignal = computed(() => isAdminSignal.value || signalPaths.has(route.matched.at(-1)?.path || route.path))

  return { isConsoleSignal, isAdminSignal }
}
