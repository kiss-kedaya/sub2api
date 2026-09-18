import { computed } from 'vue'
import { useRoute } from 'vue-router'

const signalPaths = new Set([
  '/dashboard', '/keys', '/usage', '/monitor', '/available-channels',
  '/purchase', '/orders', '/subscriptions', '/redeem',
  '/profile', '/affiliate', '/ip-allowlist',
  '/payment/qrcode', '/payment/result',
])

export function useConsoleSignal() {
  const route = useRoute()
  const isConsoleSignal = computed(() => signalPaths.has(route.matched.at(-1)?.path || route.path))

  return { isConsoleSignal }
}
