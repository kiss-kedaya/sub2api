<template>
  <!-- Navbar of the dashboard panel (UDashboardNavbar): toggle + breadcrumb left, actions right. -->
  <header
    class="sticky top-0 z-30 border-b border-gray-200/50 bg-white dark:border-dark-700/50 dark:bg-dark-900"
    :class="{ 'signal-header': isConsoleSignal }"
  >
    <div class="header-inner flex h-16 items-center justify-between gap-2 px-2 sm:px-4 md:px-6">
      <!-- Left: mobile menu, sidebar collapse and the page location -->
      <div class="header-location flex shrink-0 items-center gap-1.5">
        <button
          type="button"
          class="header-shortcut inline-flex items-center justify-center lg:hidden"
          :aria-label="t('common.toggleMenu')"
          @click="toggleMobileSidebar"
        >
          <Icon name="menu" size="md" />
        </button>
        <button
          type="button"
          data-test="sidebar-collapse"
          class="header-shortcut hidden items-center justify-center lg:inline-flex"
          :title="collapseLabel"
          :aria-label="collapseLabel"
          :aria-expanded="appStore.sidebarCollapsed ? 'false' : 'true'"
          @click="appStore.toggleSidebar()"
        >
          <UIcon :name="appStore.sidebarCollapsed ? 'i-lucide-panel-left-open' : 'i-lucide-panel-left-close'" class="size-5" />
        </button>

        <nav v-if="isConsoleSignal" class="signal-breadcrumb" :aria-label="pageTitle">
          <router-link :to="authStore.isAdminObserver ? '/admin/usage' : authStore.isAdmin ? '/admin/dashboard' : '/dashboard'" class="signal-breadcrumb-home" :title="appStore.siteName">
            {{ appStore.siteName }}
          </router-link>
          <Icon name="chevronRight" size="xs" aria-hidden="true" />
          <h1 v-if="isAdminSignal" class="signal-admin-title" :title="pageTitle">{{ pageTitle }}</h1>
          <span v-else aria-current="page" :title="pageTitle">{{ pageTitle }}</span>
        </nav>
        <div v-else class="hidden lg:block">
          <h1 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ pageTitle }}
          </h1>
        </div>
      </div>

      <!-- Right: search, announcements, support, docs, model plaza, language, theme, subscriptions, balance -->
      <div class="header-actions flex min-w-0 items-center gap-1 sm:gap-3">
        <ConsoleNavigationSearch v-if="isConsoleSignal" />
        <AnnouncementBell v-if="user" class="header-bell" />
        <router-link
          v-if="user"
          :to="authStore.isAdmin && !authStore.isAdminObserver ? '/admin/tickets' : '/tickets'"
          class="header-shortcut inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-gray-600 transition-colors hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
          :title="t('tickets.support')"
          :aria-label="t('tickets.support')"
        >
          <Icon name="chat" size="md" />
        </router-link>

        <!-- Docs Link -->
        <a
          v-if="docUrl"
          :href="docUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="header-shortcut hidden items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white sm:flex"
          :title="t('nav.docs')"
        >
          <Icon name="book" size="sm" />
          <span class="hidden sm:inline">{{ t('nav.docs') }}</span>
        </a>

        <!-- Model Plaza Entry -->
        <router-link
          v-if="user && modelPlazaEnabled"
          :to="{ path: '/model-plaza', query: { embedded: '1' } }"
          class="header-shortcut hidden items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white sm:flex"
          :title="t('nav.modelPlaza')"
        >
          <Icon name="grid" size="sm" />
          <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
        </router-link>

        <!-- Language Switcher -->
        <LocaleSwitcher class="header-locale" />

        <!-- Light / dark (UColorModeButton): the icon shows the mode in force on <html>. -->
        <button
          type="button"
          data-test="theme-toggle"
          class="header-shortcut hidden shrink-0 items-center justify-center sm:inline-flex"
          :title="themeToggleLabel"
          :aria-label="themeToggleLabel"
          @click="colorMode.toggle()"
        >
          <UIcon :name="colorMode.isDark.value ? 'i-lucide-moon' : 'i-lucide-sun'" class="size-5" />
        </button>

        <!-- Subscription Progress (for users with active subscriptions) -->
        <SubscriptionProgressMini v-if="user" class="header-subscriptions" />

        <!-- Balance Display -->
        <div
          v-if="user"
          class="header-balance group relative hidden items-center gap-2 rounded-xl bg-primary-50 px-3 py-1.5 dark:bg-primary-900/20 sm:flex"
          :tabindex="isConsoleSignal ? 0 : undefined"
          :aria-label="isConsoleSignal ? `${balanceAvailableText} ${formatHeaderMoney(availableBalance)}` : undefined"
        >
          <svg
            class="h-4 w-4 text-primary-600 dark:text-primary-400"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z"
            />
          </svg>
          <span class="header-balance-value text-sm font-semibold text-primary-700 dark:text-primary-300">
            {{ formatHeaderMoney(availableBalance) }}
          </span>
          <span
            v-if="frozenBalance > 0"
            class="header-balance-frozen rounded-full bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-200"
          >
            {{ balanceFrozenLabel }}
          </span>
          <div
            class="header-balance-detail pointer-events-none absolute right-0 top-full mt-2 hidden w-56 rounded-lg border border-gray-200 bg-white p-3 text-xs shadow-lg group-hover:block dark:border-dark-700 dark:bg-dark-800"
          >
            <div class="flex items-center justify-between">
              <span class="text-gray-500 dark:text-dark-400">{{ balanceAvailableText }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatHeaderMoney(availableBalance) }}</span>
            </div>
            <div class="mt-2 flex items-center justify-between">
              <span class="text-gray-500 dark:text-dark-400">{{ balanceFrozenText }}</span>
              <span class="font-medium text-amber-700 dark:text-amber-200">{{ formatHeaderMoney(frozenBalance) }}</span>
            </div>
            <div class="mt-2 border-t border-gray-100 pt-2 dark:border-dark-700">
              <div class="flex items-center justify-between">
                <span class="text-gray-500 dark:text-dark-400">{{ balanceTotalText }}</span>
                <span class="font-semibold text-gray-900 dark:text-white">{{ formatHeaderMoney(totalBalance) }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Phones: the user menu (the sidebar footer has it on desktop). -->
        <UserMenu variant="header" />
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useLocale } from '@nuxt/ui/composables/useLocale'
import { useAppStore, useAuthStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import Icon from '@/components/icons/Icon.vue'
import { resolveDocsUrl } from '@/utils/docsUrl'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { useConsoleSignal } from '@/composables/useConsoleSignal'
import { useSiteColorMode } from '@/composables/useSiteTheme'
import ConsoleNavigationSearch from './ConsoleNavigationSearch.vue'
import UserMenu from './UserMenu.vue'

const route = useRoute()
const { isConsoleSignal, isAdminSignal } = useConsoleSignal()
const { t } = useI18n()
const { t: uiT } = useLocale()
const appStore = useAppStore()
const authStore = useAuthStore()
const adminSettingsStore = useAdminSettingsStore()
const colorMode = useSiteColorMode()

const user = computed(() => authStore.user)
const docUrl = computed(() => resolveDocsUrl(appStore.docUrl))
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const totalBalance = computed(() => availableBalance.value + frozenBalance.value)
const balanceAvailableText = computed(() => t('common.availableBalance') === 'common.availableBalance' ? '可用余额' : t('common.availableBalance'))
const balanceFrozenText = computed(() => t('common.frozenBalance') === 'common.frozenBalance' ? '冻结金额' : t('common.frozenBalance'))
const balanceTotalText = computed(() => t('common.totalBalance') === 'common.totalBalance' ? '总余额' : t('common.totalBalance'))
const balanceFrozenLabel = computed(() => `${balanceFrozenText.value} ${formatHeaderMoney(frozenBalance.value)}`)

// Nuxt UI's own strings (zh_cn / en follow the app locale through UApp).
const collapseLabel = computed(() => appStore.sidebarCollapsed ? uiT('dashboardSidebarCollapse.expand') : uiT('dashboardSidebarCollapse.collapse'))
const themeToggleLabel = computed(() => colorMode.isDark.value ? uiT('colorMode.switchToLight') : uiT('colorMode.switchToDark'))

const pageTitle = computed(() => {
  // For custom pages, use the menu item's label instead of generic "自定义页面"
  if (route.name === 'CustomPage') {
    const id = route.params.id as string
    const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
    const menuItem = publicItems.find((item) => item.id === id)
      ?? (authStore.isAdmin ? adminSettingsStore.customMenuItems.find((item) => item.id === id) : undefined)
    if (menuItem?.label) return menuItem.label
  }
  const titleKey = route.meta.titleKey as string
  if (titleKey) {
    return t(titleKey)
  }
  return (route.meta.title as string) || ''
})

function toggleMobileSidebar() {
  appStore.toggleMobileSidebar()
}

function formatHeaderMoney(value: number) {
  if (!Number.isFinite(value)) return '$0.00'
  return `$${value.toFixed(2)}`
}
</script>

<style>
/*
  Navbar in the Nuxt UI dashboard manner: 64px, hairline bottom border, 32px ghost
  icon buttons, a 14px breadcrumb. Selectors start with `.console-signal header.signal-header`
  so they outrank console-signal.css / console-studio.css / console-elevation.css in any
  load order. Sizes apply to both themes; colours under `html:not(.dark)` are the light
  theme only, so the night theme keeps its --signal-* colours.
*/
.console-signal header.signal-header .header-inner {
  height: 64px;
}

.console-signal header.signal-header .signal-breadcrumb {
  gap: 6px;
  font-size: 14px;
}

.console-signal header.signal-header .signal-breadcrumb > svg {
  flex-basis: 14px;
  width: 14px;
  height: 14px;
}

.console-signal header.signal-header .signal-breadcrumb > span,
.console-signal header.signal-header .signal-admin-title {
  font-size: 14px;
  font-weight: 600;
}

/* The bell is a ghost icon button too; no zoom on hover. */
.console-signal header.signal-header .header-bell > button:hover {
  scale: none;
}

@media (min-width: 640px) {
  .console-signal header.signal-header :is(.header-location > button, button.header-shortcut, a.header-shortcut[aria-label], .header-bell > button) {
    width: 32px;
    min-width: 32px;
    height: 32px;
    min-height: 32px;
    padding: 0;
    border-radius: 6px;
  }

  .console-signal header.signal-header :is(a.header-shortcut:not([aria-label]), .header-locale > button, .header-subscriptions > button) {
    min-height: 32px;
    padding-block: 0;
    border-radius: 6px;
  }

  .console-signal header.signal-header .header-balance {
    min-height: 32px;
    padding-block: 0;
  }
}

/* ---- Light theme colours ---- */
html:not(.dark) .console-signal header.signal-header .signal-breadcrumb {
  color: #737373;
}

html:not(.dark) .console-signal header.signal-header .signal-breadcrumb > svg {
  color: #a3a3a3;
}

html:not(.dark) .console-signal header.signal-header .signal-breadcrumb-home:hover {
  color: #171717;
}

html:not(.dark) .console-signal header.signal-header :is(.signal-breadcrumb > span, .signal-admin-title) {
  color: #171717;
}

/* Ghost neutral buttons; the subscription status dots keep their colours. */
html:not(.dark) .console-signal header.signal-header :is(.header-shortcut, .header-bell > button, .header-locale > button, .header-subscriptions > button) {
  border: 0;
  background: transparent;
  box-shadow: none;
  color: #525252;
  transition: background-color 150ms ease, color 150ms ease;
}

html:not(.dark) .console-signal header.signal-header :is(.header-shortcut, .header-bell > button, .header-locale > button, .header-subscriptions > button):hover {
  background: #f5f5f5;
  color: #171717;
}

html:not(.dark) .console-signal header.signal-header .header-bell > button.text-blue-600 {
  color: #2563eb;
}

/* Balance: outline chip with tabular numbers; the details card is a Nuxt UI popover. */
html:not(.dark) .console-signal header.signal-header .header-balance {
  border-color: #e5e5e5;
  border-radius: 6px;
  background: #ffffff;
  box-shadow: 0 1px 2px rgb(10 10 10 / 0.05);
}

html:not(.dark) .console-signal header.signal-header .header-balance > svg {
  color: #737373;
}

html:not(.dark) .console-signal header.signal-header .header-balance-value {
  color: #171717;
}

html:not(.dark) .console-signal header.signal-header .header-balance-frozen {
  background: #fffbeb;
  color: #b45309;
}

html:not(.dark) .console-signal header.signal-header .header-balance-detail {
  border-color: #e5e5e5;
  border-radius: 8px;
  box-shadow: 0 10px 15px -3px rgb(10 10 10 / 0.08), 0 4px 6px -4px rgb(10 10 10 / 0.06);
}
</style>
