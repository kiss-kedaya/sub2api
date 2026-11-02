import type { RouterConfig } from '@nuxt/schema'
import { routes } from '../src/router/records'

export default {
  routes: () => routes,
  scrollBehavior: () => ({ top: 0 }),
} satisfies RouterConfig
