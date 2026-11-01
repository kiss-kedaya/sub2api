<template>
  <!-- Sub-pages of one area: a horizontal UNavigationMenu (highlight) in the toolbar row under
       the navbar, like the template's settings page. Each item is a child route; the page
       body is the parent route's <RouterView />. The current item is the longest path that
       contains the current route, so /admin/settings/security/keys still lights "Security". -->
  <PageToolbar>
    <UNavigationMenu
      :items="menuItems"
      highlight
      class="page-subnav -mx-1 min-w-0 flex-1"
      :aria-label="label"
    />
    <template v-if="$slots.right" #right>
      <slot name="right" />
    </template>
  </PageToolbar>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import type { NavigationMenuItem } from '@nuxt/ui'
import PageToolbar from './PageToolbar.vue'

const props = defineProps<{
  /** Items as for UNavigationMenu: { label, icon?, to, badge?, ... }; a second list sits on the right. */
  items: NavigationMenuItem[] | NavigationMenuItem[][]
  /** Accessible name of the sub-navigation, e.g. the area name. */
  label?: string
}>()

const route = useRoute()
const router = useRouter()

const lists = computed<NavigationMenuItem[][]>(() =>
  Array.isArray(props.items[0]) ? (props.items as NavigationMenuItem[][]) : [props.items as NavigationMenuItem[]]
)

function pathOf(to: RouteLocationRaw | undefined): string | null {
  if (to === undefined || to === null) return null
  if (typeof to === 'string' && /^[a-z][a-z0-9+.-]*:/i.test(to)) return null
  try {
    return router.resolve(to).path.replace(/\/+$/, '') || '/'
  } catch {
    return null
  }
}

// The deepest item path that the current route is on or under.
const activePath = computed(() => {
  const current = route.path.replace(/\/+$/, '') || '/'
  let best: string | null = null
  for (const list of lists.value) {
    for (const item of list) {
      if (item.target === '_blank') continue
      const path = pathOf(item.to)
      if (!path) continue
      const hit = current === path || current.startsWith(path === '/' ? '/' : `${path}/`)
      if (hit && (!best || path.length > best.length)) best = path
    }
  }
  return best
})

const menuItems = computed<NavigationMenuItem[][]>(() =>
  lists.value.map((list) =>
    list.map((item) => ('active' in item && item.active !== undefined
      ? item
      : { ...item, active: item.target !== '_blank' && pathOf(item.to) === activePath.value }))
  )
)
</script>

<style>
/* The underline sits on the toolbar's hairline, as in the template (49px row, no padding). */
.console-toolbar:has(.page-subnav) {
  padding-block: 0;
}

.console-toolbar:has(.page-subnav) .console-toolbar-start {
  flex: 1 1 auto;
  min-width: 0;
}

/* Many sub-pages on a phone: scroll sideways instead of wrapping. */
.page-subnav {
  overflow-x: auto;
  scrollbar-width: none;
}

.page-subnav::-webkit-scrollbar {
  display: none;
}

.page-subnav [data-slot='list'] {
  flex-wrap: nowrap;
}

.page-subnav [data-slot='link'] {
  white-space: nowrap;
}
</style>
