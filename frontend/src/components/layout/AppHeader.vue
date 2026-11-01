<template>
  <!-- UDashboardNavbar: collapse + page title on the left; page actions, notifications,
       light / dark and balance on the right. Pages fill the targets below through their
       header component or <PageNavbar> / <PageToolbar> (see dashboardFrame.ts). -->
  <UDashboardNavbar
    as="header"
    class="console-navbar signal-header bg-(--app-page-bg)"
    :ui="{ right: 'gap-1.5 sm:gap-2', left: 'min-w-0 flex-1' }"
  >
    <!-- Phones: opens the sidebar drawer (UDashboardSidebarToggle). -->
    <template #toggle>
      <UDashboardSidebarToggle data-test="sidebar-toggle" class="-ms-1.5" />
    </template>

    <template #leading>
      <UDashboardSidebarCollapse data-test="sidebar-collapse" class="-ms-1.5" />
    </template>

    <template #title>
      <span v-if="!titleClaim?.slot" class="truncate" :title="pageTitle">{{ pageTitle }}</span>
      <span :id="FRAME_TARGETS.title" class="console-navbar-target"></span>
    </template>

    <template #trailing>
      <span :id="FRAME_TARGETS.titleTrailing" class="console-navbar-trailing"></span>
    </template>

    <template #right>
      <span :id="FRAME_TARGETS.actions" class="console-navbar-actions"></span>

      <AnnouncementBell v-if="user" class="header-bell" />

      <!-- Light / dark (UColorModeButton): the icon shows the mode in force on <html>. -->
      <UButton
        data-test="theme-toggle"
        color="neutral"
        variant="ghost"
        square
        class="hidden sm:inline-flex"
        :icon="colorMode.isDark.value ? 'i-lucide-moon' : 'i-lucide-sun'"
        :title="themeToggleLabel"
        :aria-label="themeToggleLabel"
        @click="colorMode.toggle()"
      />

      <!-- Balance; hover or focus shows available / frozen / total. -->
      <div
        v-if="user"
        class="header-balance group relative hidden items-center gap-1.5 sm:flex"
        tabindex="0"
        :aria-label="`${balanceAvailableText} ${formatHeaderMoney(availableBalance)}`"
      >
        <UIcon name="i-lucide-wallet" class="size-4 shrink-0 text-dimmed" />
        <span class="header-balance-value">{{ formatHeaderMoney(availableBalance) }}</span>
        <span v-if="frozenBalance > 0" class="header-balance-frozen hidden xl:inline">{{ balanceFrozenLabel }}</span>
        <div class="header-balance-detail">
          <div class="flex items-center justify-between">
            <span class="text-muted">{{ balanceAvailableText }}</span>
            <span class="font-medium text-highlighted">{{ formatHeaderMoney(availableBalance) }}</span>
          </div>
          <div class="mt-2 flex items-center justify-between">
            <span class="text-muted">{{ balanceFrozenText }}</span>
            <span class="font-medium text-warning">{{ formatHeaderMoney(frozenBalance) }}</span>
          </div>
          <div class="mt-2 flex items-center justify-between border-t border-default pt-2">
            <span class="text-muted">{{ balanceTotalText }}</span>
            <span class="font-semibold text-highlighted">{{ formatHeaderMoney(totalBalance) }}</span>
          </div>
        </div>
      </div>

      <!-- Phones: the user menu (the sidebar footer has it on desktop). -->
      <UserMenu variant="header" />
    </template>
  </UDashboardNavbar>

  <!-- UDashboardToolbar: shown only while a page puts something in it (CSS :has below). -->
  <UDashboardToolbar
    class="console-toolbar bg-(--app-page-bg)"
    :ui="{ root: 'overflow-visible flex-wrap gap-y-2 py-2' }"
  >
    <div :id="FRAME_TARGETS.toolbarStart" class="console-toolbar-start"></div>
    <div :id="FRAME_TARGETS.toolbarEnd" class="console-toolbar-end"></div>
  </UDashboardToolbar>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useLocale } from '@nuxt/ui/composables/useLocale'
import { useAuthStore, useAppStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import { useSiteColorMode } from '@/composables/useSiteTheme'
import UserMenu from './UserMenu.vue'
import { FRAME_TARGETS, useDashboardFrame } from './dashboardFrame'

const route = useRoute()
const { t } = useI18n()
const { t: uiT } = useLocale()
const appStore = useAppStore()
const authStore = useAuthStore()
const adminSettingsStore = useAdminSettingsStore()
const colorMode = useSiteColorMode()
const frame = useDashboardFrame()

const user = computed(() => authStore.user)
const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const totalBalance = computed(() => availableBalance.value + frozenBalance.value)
const balanceAvailableText = computed(() => t('common.availableBalance') === 'common.availableBalance' ? '可用余额' : t('common.availableBalance'))
const balanceFrozenText = computed(() => t('common.frozenBalance') === 'common.frozenBalance' ? '冻结金额' : t('common.frozenBalance'))
const balanceTotalText = computed(() => t('common.totalBalance') === 'common.totalBalance' ? '总余额' : t('common.totalBalance'))
const balanceFrozenLabel = computed(() => `${balanceFrozenText.value} ${formatHeaderMoney(frozenBalance.value)}`)

// Nuxt UI's own strings (zh_cn / en follow the app locale through UApp).
const themeToggleLabel = computed(() => colorMode.isDark.value ? uiT('colorMode.switchToLight') : uiT('colorMode.switchToDark'))

// The newest page header that names the page (its title prop or its own #title slot).
const titleClaim = computed(() => frame?.titles.at(-1) ?? null)

const routeTitle = computed(() => {
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

const pageTitle = computed(() => titleClaim.value?.text() || routeTitle.value)

function formatHeaderMoney(value: number) {
  if (!Number.isFinite(value)) return '$0.00'
  return `$${value.toFixed(2)}`
}
</script>

<style>
/* The toolbar row exists only while a page teleports something into it. */
.console-toolbar:not(:has(> .console-toolbar-start:not(:empty), > .console-toolbar-end:not(:empty))) {
  display: none;
}

/* Empty teleport targets take no room in the navbar row. */
.console-navbar-target:empty,
.console-navbar-trailing:empty,
.console-navbar-actions:empty,
.console-toolbar-start:empty,
.console-toolbar-end:empty {
  display: none;
}

.console-navbar-target {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}

.console-navbar-trailing {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  overflow: hidden;
  color: var(--ui-text-muted);
  font-size: 14px;
  white-space: nowrap;
}

.console-navbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-inline-end: 4px;
}

.console-toolbar-start,
.console-toolbar-end {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.console-toolbar-end {
  margin-inline-start: auto;
}

/* Legacy page controls teleported into the bar get UButton / UInput md sizes (32px). */
@media (min-width: 640px) {
  :is(.console-navbar.console-navbar, .console-toolbar.console-toolbar) :is(.btn, .select-trigger, .date-picker-trigger) {
    min-height: 32px;
    padding: 5px 10px;
    gap: 6px;
    border-radius: calc(var(--ui-radius) * 1.5);
    font-size: 14px;
    line-height: 20px;
  }

  :is(.console-navbar.console-navbar, .console-toolbar.console-toolbar) .btn:has(> svg:only-child) {
    padding-inline: 6px;
  }
}

/* AnnouncementBell is a ghost neutral icon button here, like the template's bell. */
.console-navbar .header-bell > button {
  width: 32px;
  height: 32px;
  border-radius: calc(var(--ui-radius) * 1.5);
  color: var(--ui-text);
}

.console-navbar .header-bell > button:hover {
  background: color-mix(in oklab, var(--ui-bg-elevated) 100%, transparent);
  color: var(--ui-text-highlighted);
  scale: none;
}

/* Balance: an outline neutral chip; the details card opens on hover or keyboard focus. */
.console-navbar .header-balance {
  min-height: 32px;
  padding: 0 10px;
  border-radius: calc(var(--ui-radius) * 1.5);
  box-shadow: inset 0 0 0 1px var(--ui-border-accented);
  font-size: 14px;
  font-variant-numeric: tabular-nums;
  cursor: default;
}

.console-navbar .header-balance:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 2px;
}

.console-navbar .header-balance-value {
  color: var(--ui-text-highlighted);
  font-weight: 600;
  white-space: nowrap;
}

.console-navbar .header-balance-frozen {
  padding: 0 6px;
  border-radius: var(--ui-radius);
  background: color-mix(in oklab, var(--ui-warning) 12%, transparent);
  color: var(--ui-warning);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
}

.console-navbar .header-balance-detail {
  position: absolute;
  top: 100%;
  right: 0;
  z-index: 50;
  display: none;
  width: 224px;
  max-width: calc(100dvw - 32px);
  margin-top: 8px;
  padding: 12px;
  border-radius: calc(var(--ui-radius) * 1.5);
  background: var(--ui-bg);
  box-shadow: 0 10px 15px -3px rgb(0 0 0 / 0.08), 0 4px 6px -4px rgb(0 0 0 / 0.06), 0 0 0 1px var(--ui-border);
  font-size: 12px;
  pointer-events: none;
}

.console-navbar .header-balance:hover .header-balance-detail,
.console-navbar .header-balance:focus-visible .header-balance-detail {
  display: block;
}
</style>
