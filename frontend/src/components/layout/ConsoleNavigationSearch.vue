<template>
  <!-- UDashboardSearch: opened by the sidebar's UDashboardSearchButton or Ctrl/⌘ K
       (its own shortcut, also while typing). Pages the visitor may open, then the theme. -->
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
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { THEME_MODE_OPTIONS, useSiteColorMode, type ThemeMode } from '@/composables/useSiteTheme'

const { t } = useI18n()
const { t: uiT } = useLocale()
const router = useRouter()
const auth = useAuthStore()
const app = useAppStore()
const colorMode = useSiteColorMode()
const open = ref(false)
const query = ref('')

interface Destination {
  path: string
  label: string
  icon: string
  group: 'admin' | 'personal'
}

// The same permission and feature-flag rules as before; only the presentation changed.
const destinations = computed((): Destination[] => {
  const personal: Destination[] = [
    { path: '/dashboard', label: t('nav.dashboard'), icon: 'i-lucide-layout-dashboard', group: 'personal' },
    { path: '/keys', label: t('nav.apiKeys'), icon: 'i-lucide-key-round', group: 'personal' },
    ...(!auth.isSimpleMode ? [{ path: '/usage', label: t('nav.usage'), icon: 'i-lucide-chart-column', group: 'personal' } as const] : []),
    ...(!auth.isSimpleMode ? [{ path: '/redeem', label: t('nav.redeem'), icon: 'i-lucide-gift', group: 'personal' } as const] : []),
    { path: '/profile', label: t('nav.profile'), icon: 'i-lucide-user', group: 'personal' },
    { path: '/tickets', label: t('tickets.title'), icon: 'i-lucide-message-circle', group: 'personal' },
    ...(isFeatureFlagEnabled(FeatureFlags.channelMonitor) ? [{ path: '/monitor', label: t('nav.channelStatus'), icon: 'i-lucide-activity', group: 'personal' } as const] : []),
    ...(!auth.isSimpleMode && isFeatureFlagEnabled(FeatureFlags.payment) ? [{ path: '/purchase', label: t('nav.buySubscription'), icon: 'i-lucide-credit-card', group: 'personal' } as const] : []),
  ]
  if (auth.user?.role !== 'admin') return app.backendModeEnabled ? [] : personal
  if (auth.isAdminObserver) {
    return [{ path: '/admin/usage', label: t('admin.usage.title'), icon: 'i-lucide-chart-column', group: 'admin' }, ...personal]
  }
  const admin: Destination[] = [
    { path: '/admin/dashboard', label: t('admin.dashboard.title'), icon: 'i-lucide-layout-dashboard', group: 'admin' },
    { path: '/admin/accounts', label: t('admin.accounts.title'), icon: 'i-lucide-server', group: 'admin' },
    { path: '/admin/users', label: t('admin.users.title'), icon: 'i-lucide-users', group: 'admin' },
    { path: '/admin/tickets', label: t('tickets.adminTitle'), icon: 'i-lucide-message-circle', group: 'admin' },
    { path: '/admin/groups', label: t('admin.groups.title'), icon: 'i-lucide-layout-grid', group: 'admin' },
    { path: '/admin/usage', label: t('admin.usage.title'), icon: 'i-lucide-chart-column', group: 'admin' },
    { path: '/admin/settings', label: t('admin.settings.title'), icon: 'i-lucide-settings', group: 'admin' },
  ]
  return auth.isSimpleMode ? admin.filter(item => item.path !== '/admin/users') : [...admin, ...personal]
})

// Plain substring match on label and path, as before (a path such as /admin/accounts works too).
const results = computed(() => {
  const term = query.value.trim().toLocaleLowerCase()
  return destinations.value.filter(item => !term || (item.label + ' ' + item.path).toLocaleLowerCase().includes(term))
})

const MODE_ICONS: Record<ThemeMode, string> = {
  system: 'i-lucide-monitor',
  light: 'i-lucide-sun',
  dark: 'i-lucide-moon'
}

const groups = computed((): CommandPaletteGroup<CommandPaletteItem>[] => {
  const toItem = (item: Destination): CommandPaletteItem => ({
    label: item.label,
    icon: item.icon,
    class: 'console-search-result',
    onSelect: () => navigate(item.path)
  })
  const navigation = ['admin', 'personal'].map((group) => ({
    id: `nav-${group}`,
    label: group === 'personal' && results.value.some(item => item.group === 'admin') ? t('nav.myAccount') : undefined,
    // Already filtered above; Fuse must not drop or reorder these.
    ignoreFilter: true,
    items: results.value.filter(item => item.group === group).map(toItem)
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
  return [...navigation.filter(group => group.items.length), theme]
})

watch(open, (value) => {
  if (value) query.value = ''
})

function navigate(path?: string) {
  if (!path) return
  open.value = false
  void router.push(path)
}
</script>
