import { createRouter, createWebHistory } from 'vue-router'
import { routes } from './records'
import { installNavigationGuards } from './guards'
export { routes } from './records'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior(to, from, saved) {
    if (saved) return saved
    if (to.hash) return { el: to.hash, behavior: 'smooth' }
    if (to.path === '/docs' && from.path === '/docs') {
      const reduced =
        typeof window !== 'undefined' &&
        typeof window.matchMedia === 'function' &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches
      return { top: 0, behavior: reduced ? 'auto' : 'smooth' }
    }
    return { top: 0 }
  },
})
installNavigationGuards(router)
export default router
