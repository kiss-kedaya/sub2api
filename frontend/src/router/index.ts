import { createRouter, createWebHistory } from 'vue-router'
import { routes } from './records'
import { installNavigationGuards } from './guards'
export { routes } from './records'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior(_to, _from, saved) { return saved ?? { top: 0 } },
})
installNavigationGuards(router)
export default router
