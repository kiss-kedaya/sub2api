import { computed, getCurrentInstance, ref, type Ref } from 'vue'
import type { Router } from 'vue-router'

/**
 * Sub-pages of the sys admin areas (risk center, crypto, request capture, mihomo).
 * In the app every sub-page is a sibling route that shares the area's view and carries
 * `meta.section`, so switching sub-pages keeps the view (and its loaded data) mounted.
 * Without a router (specs mount the views bare) the section is local state and the view
 * keeps behaving as the single page it was before.
 */
export function useSectionRoute<S extends string>(fallback: S, names: Record<S, string>) {
  // Read the router without useRouter(): its missing-injection warning would flood router-less specs.
  const router = getCurrentInstance()?.appContext.config.globalProperties.$router as Router | undefined
  const local = ref(fallback) as Ref<S>
  const ownNames = Object.values(names) as string[]
  let last: S = fallback
  const section = computed<S>({
    get: () => {
      if (!router) return local.value
      const current = router.currentRoute.value
      // While leaving the area the next route is already current: keep the last section.
      if (!ownNames.includes(String(current.name))) return last
      const value = current.meta.section as S | undefined
      last = value && value in names ? value : fallback
      return last
    },
    set: (value: S) => {
      if (!router) { local.value = value; return }
      void router.push({ name: names[value] })
    }
  })
  return { section, routed: !!router, to: (value: S) => ({ name: names[value] }) }
}
