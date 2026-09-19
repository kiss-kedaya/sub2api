import { computed } from 'vue'
import { useRoute } from 'vue-router'

export function useConsoleSignal() {
  const route = useRoute()
  const isAdminSignal = computed(() => (route.matched.at(-1)?.path || route.path).startsWith('/admin/'))
  // The shared layout owns its theme; new and embedded routes inherit it too.
  const isConsoleSignal = computed(() => true)

  return { isConsoleSignal, isAdminSignal }
}
