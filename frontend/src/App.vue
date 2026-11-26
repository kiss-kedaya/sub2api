<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'
import { computed, onMounted, onBeforeUnmount, watch } from 'vue'
import Toast from '@/components/common/Toast.vue'
import NavigationProgress from '@/components/common/NavigationProgress.vue'
import AdminComplianceDialog from '@/components/admin/AdminComplianceDialog.vue'
import { applyDocumentTitle } from '@/router/documentTitle'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'
import ConfirmHost from '@/components/common/ConfirmHost.vue'
import StepUpGate from '@/components/auth/StepUpGate.vue'
import StepUpSkipNotice from '@/components/auth/StepUpSkipNotice.vue'
import AppUpdateBanner from '@/components/common/AppUpdateBanner.vue'
import AppUpdateDialog from '@/components/common/AppUpdateDialog.vue'
import { useAppUpdate } from '@/composables/useAppUpdate'
import { useAppStore, useAuthStore, useSubscriptionStore, useAnnouncementStore, useAdminComplianceStore, useAdminSettingsStore } from '@/stores'
import { useTicketNotificationStore } from '@/stores/ticketNotifications'
import { getSetupStatus } from '@/api/setup'
import { updateFavicon } from '@/utils/branding'
import { applyPlatformProfile } from '@/utils/platform'
import { useNuxtUiLocale } from '@/plugins/nuxtUiLocale'
import { applySiteTheme, normalizeSiteTheme, useSiteTheme } from '@/composables/useSiteTheme'
import AppLayout from '@/components/layout/AppLayout.vue'
import RoutePageTransition from '@/components/layout/RoutePageTransition.vue'
import { isConsoleLayoutRoute } from '@/components/layout/consoleShell'
import { useClientAnchorNavigation } from '@/composables/useClientAnchorNavigation'
import { useRouteIntentPrefetch } from '@/composables/useRouteIntentPrefetch'

const router = useRouter()
useClientAnchorNavigation(router)
useRouteIntentPrefetch(router)
// 标签页开着不刷新会一直跑旧代码；发现服务端有新版前端就提示，并在下一次跳转时整页加载。
const appUpdate = useAppUpdate()
let stopAppUpdate: (() => void) | null = null
const nuxtUiLocale = useNuxtUiLocale()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const consoleShell = computed(() => isConsoleLayoutRoute(route.meta, route, authStore.isAuthenticated))
const PREFERS_REDUCED_MOTION =
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
    : false
const SHELL_TRANSITION_MS = PREFERS_REDUCED_MOTION ? 0 : { enter: 150, leave: 80 }
const subscriptionStore = useSubscriptionStore()
const announcementStore = useAnnouncementStore()
const ticketStore = useTicketNotificationStore()
const adminComplianceStore = useAdminComplianceStore()
const adminSettingsStore = useAdminSettingsStore()

function updateDocumentTitle() {
  applyDocumentTitle(route)
}

// main.ts applies the site theme before mounting. Apply it again when the public
// settings bring a different one (first fetch without injected config, or a save).
// An unchanged theme is left alone so an admin's live preview is not overwritten;
// the light/dark class is only reset when the site default mode itself changed.
const { theme: appliedSiteTheme } = useSiteTheme()
watch(
  () => JSON.stringify(normalizeSiteTheme(appStore.cachedPublicSettings)),
  (next) => {
    if (next === JSON.stringify(appliedSiteTheme.value)) return
    const theme = normalizeSiteTheme(appStore.cachedPublicSettings)
    applySiteTheme(theme, { colorMode: theme.theme_default_mode !== appliedSiteTheme.value.theme_default_mode })
  }
)

// Watch for site settings changes and update favicon/title
watch(
  () => appStore.siteLogo,
  (newLogo) => {
    if (newLogo) {
      updateFavicon(newLogo)
    }
  },
  { immediate: true }
)

watch(
  [
    () => route.fullPath,
    () => route.meta.title,
    () => route.meta.titleKey,
    () => appStore.siteName,
    () => appStore.cachedPublicSettings?.custom_menu_items,
    () => authStore.isAdmin,
    () => adminSettingsStore.customMenuItems,
  ],
  updateDocumentTitle,
  { deep: true }
)

// Badge counts only. Announcement bodies, the announcement timeline and the
// ticket lists stay lazy and are fetched by the panel that shows them.
function refreshBadges(force = false) {
  if (!authStore.isAuthenticated) return
  void announcementStore.fetchSummary(force)
  void ticketStore.fetchAll(authStore.isAdmin && !authStore.isAdminObserver, force)
}

// Watch for authentication state and manage subscription data + announcements
function onVisibilityChange() {
  if (document.visibilityState === 'visible' && authStore.isAuthenticated) {
    refreshBadges()
  }
}

function onAdminComplianceRequired(event: Event) {
  if (authStore.isAdminObserver) return
  const detail = (event as CustomEvent<Record<string, string>>).detail || {}
  adminComplianceStore.requireAcknowledgement(detail)
}

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated, oldValue) => {
    if (typeof document === 'undefined') return
    if (isAuthenticated) {
      if (authStore.isAdmin && !authStore.isAdminObserver) {
        adminComplianceStore.fetchStatus().catch((error) => {
          console.error('Failed to fetch admin compliance status:', error)
        })
      }

      // User logged in: preload subscriptions and start polling
      subscriptionStore.fetchActiveSubscriptions().catch((error) => {
        console.error('Failed to preload subscriptions:', error)
      })
      subscriptionStore.startPolling()

      // Announcements: new login vs page refresh restore.
      // The summary carries the bell count; the popup bootstrap is the only
      // first-paint call that carries bodies, and the server bounds it.
      if (oldValue === false) {
        // New login: delay 3s so the session settles, then refetch counts.
        setTimeout(() => refreshBadges(true), 3000)
        setTimeout(() => void announcementStore.fetchPopups(true), 3000)
      } else {
        // Page refresh restore (oldValue was undefined)
        refreshBadges()
        void announcementStore.fetchPopups()
      }

      // Register visibility change listener
      document.addEventListener('visibilitychange', onVisibilityChange)
    } else {
      // User logged out: clear data and stop polling
      subscriptionStore.clear()
      announcementStore.reset()
      ticketStore.reset()
      adminComplianceStore.reset()
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  },
  { immediate: true }
)

// Route change trigger (throttled by the stores). Counts only: navigating must
// never pull the announcement timeline or a ticket list.
router.afterEach(() => {
  refreshBadges()
})

onBeforeUnmount(() => {
  stopAppUpdate?.()
  stopAppUpdate = null
  if (typeof document === 'undefined') return
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('admin-compliance-required', onAdminComplianceRequired)
})

onMounted(async () => {
  applyPlatformProfile()
  window.addEventListener('admin-compliance-required', onAdminComplianceRequired)
  // 放在第一个 await 之前：下面「需要安装向导」的分支会提前 return。
  stopAppUpdate = appUpdate.start(router)

  // Check if setup is needed
  try {
    const status = await getSetupStatus()
    if (status.needs_setup && route.path !== '/setup') {
      router.replace('/setup')
      return
    }
  } catch {
    // If setup endpoint fails, assume normal mode and continue
  }

  // Load public settings into appStore (will be cached for other components)
  await appStore.fetchPublicSettings()

  // Re-resolve document title now that site settings are available
  updateDocumentTitle()
})
</script>

<template>
  <!-- UApp provides Nuxt UI's tooltip/toast/overlay context; it renders no wrapper element.
       The app's own Toast and dialogs keep working alongside it. -->
  <UApp :locale="nuxtUiLocale">
    <NavigationProgress />
    <Transition name="shell-fade" mode="out-in" :duration="SHELL_TRANSITION_MS">
      <AppLayout v-if="consoleShell">
        <RoutePageTransition />
      </AppLayout>
      <RoutePageTransition v-else />
    </Transition>
    <Toast />
    <AnnouncementPopup />
    <AdminComplianceDialog />
    <ConfirmHost />
    <!-- 全局 step-up TOTP 门：管理端敏感接口 403 时统一弹码并自动重试一次 -->
    <StepUpGate />
    <!-- 5 分钟免授权窗口内跳过 2FA 时，右下角展示「已跳过 + 剩余倒计时」 -->
    <StepUpSkipNotice />
    <!-- 页面版本过期：底部提示条 + 敏感操作前的必须刷新对话框 -->
    <AppUpdateBanner />
    <AppUpdateDialog />
  </UApp>
</template>
