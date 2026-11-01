<template>
  <!-- UDashboardSearch: opened by the sidebar's UDashboardSearchButton or Ctrl/⌘ K
       (its own shortcut, also while typing). Every sidebar page in the same groups, the
       sub-pages reached through an area's sub-nav, then the theme. -->
  <UDashboardSearch
    v-model:open="open"
    v-model:search-term="query"
    :groups="groups"
    :color-mode="false"
    :fuse="{ resultLimit: 100 }"
    :title="t('common.search')"
    :placeholder="uiT('dashboardSearchButton.label')"
  >
    <template #empty>
      <p class="py-6 text-center text-sm text-muted">{{ uiT('commandPalette.noMatch', { searchTerm: query }) }}</p>
    </template>
  </UDashboardSearch>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { CommandPaletteGroup, CommandPaletteItem } from '@nuxt/ui'
import { useLocale } from '@nuxt/ui/composables/useLocale'
import { THEME_MODE_OPTIONS, useSiteColorMode, type ThemeMode } from '@/composables/useSiteTheme'
import { useConsoleNav, type NavLeaf } from './consoleNav'

const { t } = useI18n()
const { t: uiT } = useLocale()
const router = useRouter()
const colorMode = useSiteColorMode()
// The same tree as the sidebar (permissions, feature flags, simple mode): consoleNav.tsx.
const { tree, secondary } = useConsoleNav()
const open = ref(false)
const query = ref('')

interface Destination {
  path: string
  label: string
  icon?: string
  external?: boolean
}

interface DestinationGroup {
  id: string
  label?: string
  items: Destination[]
}

const toDestination = (leaf: NavLeaf, icon?: string): Destination => ({ path: leaf.path, label: leaf.label, icon: leaf.icon ?? icon, external: leaf.external })

// Groups as in the sidebar; consecutive top-level pages share an unnamed group.
const destinationGroups = computed((): DestinationGroup[] => {
  const out: DestinationGroup[] = []
  for (const node of tree.value) {
    if (node.kind === 'group') {
      const items = node.children.filter((child): child is NavLeaf => child.kind === 'link').map((leaf) => toDestination(leaf, node.icon))
      out.push({ id: `nav-${node.key}`, label: node.label, items })
    } else if (out.at(-1)?.label === undefined && out.length) {
      out[out.length - 1].items.push(toDestination(node))
    } else {
      out.push({ id: `nav-top-${out.length}`, items: [toDestination(node)] })
    }
  }
  if (secondary.value.length) out.push({ id: 'nav-more', items: secondary.value.map((leaf) => toDestination(leaf)) })
  return out
})

// Plain substring match on label and path, as before (a path such as /admin/accounts works too).
const results = computed(() => {
  const term = query.value.trim().toLocaleLowerCase()
  return destinationGroups.value
    .map((group) => ({ ...group, items: group.items.filter((item) => !term || (item.label + ' ' + item.path).toLocaleLowerCase().includes(term)) }))
    .filter((group) => group.items.length)
})

const MODE_ICONS: Record<ThemeMode, string> = {
  system: 'i-lucide-monitor',
  light: 'i-lucide-sun',
  dark: 'i-lucide-moon'
}

const groups = computed((): CommandPaletteGroup<CommandPaletteItem>[] => {
  const navigation = results.value.map((group) => ({
    id: group.id,
    label: group.label,
    // Already filtered above; Fuse must not drop or reorder these.
    ignoreFilter: true,
    items: group.items.map((item): CommandPaletteItem => ({
      label: item.label,
      icon: item.icon,
      class: 'console-search-result',
      onSelect: () => navigate(item)
    }))
  }))
  // Our own theme group: Nuxt UI's colorMode is off, the site keeps its own light / dark state.
  const theme = {
    id: 'theme',
    label: uiT('dashboardSearch.theme'),
    items: THEME_MODE_OPTIONS.map((mode) => ({
      label: t(`admin.settings.siteTheme.modes.${mode}`),
      icon: MODE_ICONS[mode],
      active: colorMode.preference.value === mode,
      onSelect: () => colorMode.setPreference(mode)
    }))
  }
  return [...navigation, theme]
})

watch(open, (value) => {
  if (value) query.value = ''
})

function navigate(item: Destination) {
  open.value = false
  if (item.external) {
    window.open(item.path, '_blank', 'noopener,noreferrer')
    return
  }
  void router.push(item.path)
}
</script>
