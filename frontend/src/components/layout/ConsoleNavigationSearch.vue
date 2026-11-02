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
  group: 'admin' | 'personal' | 'settings'
  /** Extra search terms (brand names, aliases) that are not part of the label. */
  keywords?: string[]
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

// Settings sub-sections (brand logins, captcha, payment, ...). Only the full
// administrators can open /admin/settings, and every entry deep-links to its tab.
const settingsDestinations = computed((): Destination[] => {
  if (auth.user?.role !== 'admin' || auth.isAdminObserver) return []
  const link = (tab: string, section: string) => `/admin/settings?tab=${tab}&section=${section}`
  return [
    { path: link('security', 'settings-admin-api-key'), label: t('admin.settings.adminApiKey.title'), icon: 'i-lucide-key-round', group: 'settings', keywords: ['api key', 'admin key', '管理员密钥'] },
    { path: link('security', 'settings-registration'), label: t('admin.settings.registration.title'), icon: 'i-lucide-user-plus', group: 'settings', keywords: ['register', 'signup', '注册'] },
    { path: link('security', 'settings-captcha'), label: t('admin.settings.captcha.title'), icon: 'i-lucide-shield-check', group: 'settings', keywords: ['captcha', 'turnstile', 'tencent', 'aliyun', '人机验证', '天御', '阿里云', '腾讯'] },
    { path: link('security', 'settings-linuxdo'), label: t('admin.settings.linuxdo.title'), icon: 'i-lucide-globe', group: 'settings', keywords: ['linuxdo', 'linux do', 'oauth', '第三方登录'] },
    { path: link('security', 'settings-email-oauth'), label: t('admin.settings.search.emailOAuth'), icon: 'i-lucide-mail', group: 'settings', keywords: ['github', 'google', 'oauth', 'email', '邮箱快捷登录', '谷歌'] },
    { path: link('security', 'settings-wechat'), label: t('admin.settings.wechatConnect.title'), icon: 'i-lucide-message-circle', group: 'settings', keywords: ['wechat', 'weixin', '微信', '公众号', '小程序'] },
    { path: link('security', 'settings-dingtalk'), label: t('admin.settings.dingtalk.title'), icon: 'i-lucide-message-square', group: 'settings', keywords: ['dingtalk', 'ding', '钉钉'] },
    { path: link('security', 'settings-oidc'), label: t('admin.settings.oidc.title'), icon: 'i-lucide-fingerprint', group: 'settings', keywords: ['oidc', 'openid', 'sso', '单点登录', '统一认证'] },
    { path: link('users', 'settings-defaults'), label: t('admin.settings.defaults.title'), icon: 'i-lucide-users', group: 'settings', keywords: ['defaults', '默认', '订阅默认值'] },
    { path: link('gateway', 'settings-gateway-forwarding'), label: t('admin.settings.gatewayForwarding.title'), icon: 'i-lucide-arrow-left-right', group: 'settings', keywords: ['cache', 'ttl', 'cache_control', 'antigravity', 'claude oauth', '转发'] },
    { path: link('general', 'settings-site'), label: t('admin.settings.site.title'), icon: 'i-lucide-house', group: 'settings', keywords: ['site', '站点', '名称', 'logo'] },
    { path: link('general', 'settings-custom-menu'), label: t('admin.settings.customMenu.title'), icon: 'i-lucide-list', group: 'settings', keywords: ['menu', '自定义菜单', '导航'] },
    { path: link('features', 'settings-features-channel-monitor'), label: t('admin.settings.features.channelMonitor.title'), icon: 'i-lucide-activity', group: 'settings', keywords: ['monitor', '监控', '渠道状态'] },
    { path: link('payment', 'settings-payment'), label: t('admin.settings.payment.title'), icon: 'i-lucide-credit-card', group: 'settings', keywords: ['payment', '充值', '支付', '支付宝', '微信支付'] },
    { path: link('email', 'settings-smtp'), label: t('admin.settings.smtp.title'), icon: 'i-lucide-mail', group: 'settings', keywords: ['smtp', 'mail', '邮件', '邮箱'] },
    { path: link('backup', 'settings-backup'), label: t('admin.settings.tabs.backup'), icon: 'i-lucide-database', group: 'settings', keywords: ['backup', '备份', '恢复'] },
    { path: link('agreement', 'settings-agreement'), label: t('admin.settings.tabs.agreement'), icon: 'i-lucide-file-text', group: 'settings', keywords: ['agreement', 'terms', '条款', '协议'] },
  ]
})

// Plain substring match on label, path and keywords (a path such as /admin/accounts works too).
const results = computed(() => {
  const term = query.value.trim().toLocaleLowerCase()
  const all = [...destinations.value, ...settingsDestinations.value]
  if (!term) return all
  return all.filter(item =>
    (item.label + ' ' + item.path + ' ' + (item.keywords?.join(' ') ?? '')).toLocaleLowerCase().includes(term)
  )
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
  const navigation = ['admin', 'personal', 'settings'].map((group) => ({
    id: `nav-${group}`,
    label: group === 'personal' && results.value.some(item => item.group === 'admin')
      ? t('nav.myAccount')
      : group === 'settings'
        ? t('admin.settings.title')
        : undefined,
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
