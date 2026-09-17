import { computed } from 'vue'
import { useRoute } from 'vue-router'

const signalPaths = new Set(['/dashboard', '/keys', '/usage'])

export function useConsoleSignal() {
  const route = useRoute()
  const isConsoleSignal = computed(() => signalPaths.has(route.path))

  return { isConsoleSignal }
}
