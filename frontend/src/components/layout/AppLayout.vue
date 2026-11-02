<template>
  <slot v-if="shellHost" />
  <div
    v-else
    class="console-frame"
    :class="[isConsoleSignal ? 'console-signal' : '', { 'signal-admin': isAdminSignal, 'signal-nav-collapsed': sidebarCollapsed }]"
  >
    <!-- Nuxt UI dashboard: UDashboardGroup > sidebar + one panel (navbar, optional toolbar,
         body). The panel body is the scroll container; the sakura canvas sits behind. -->
    <SakuraPetalField v-if="isConsoleSignal" />
    <InkSakura v-if="isConsoleSignal" />
    <UDashboardGroup unit="rem" storage="local" storage-key="sub2api-console">
      <AppSidebar />

      <UDashboardPanel id="console" class="console-panel" :ui="{ body: 'console-panel-body' }">
        <template #header>
          <AppHeader />
        </template>

        <template #body>
          <main ref="mainRef" class="signal-main">
            <slot />
          </main>
        </template>
      </UDashboardPanel>

      <ConsoleNavigationSearch />
    </UDashboardGroup>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import '@/styles/console-signal.css'
import '@/styles/console-account.css'
import '@/styles/console-studio.css'
import { computed, inject, onBeforeUnmount, onMounted, provide, ref } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingStore } from '@/stores/onboarding'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useConsoleSignal } from '@/composables/useConsoleSignal'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import ConsoleNavigationSearch from './ConsoleNavigationSearch.vue'
import InkSakura from './InkSakura.vue'
import SakuraPetalField from './SakuraPetalField.vue'
import { provideDashboardFrame } from './dashboardFrame'
import { usePanelScroll } from './panelScroll'
import { CONSOLE_SHELL_KEY } from './consoleShell'

// The app shell mounts this component once for authenticated pages. Views still
// wrap their content in <AppLayout> for the standalone SPA build; when a host is
// present they become a transparent slot instead of remounting the sidebar,
// header and scroll container on every route change.
const shellHost = inject(CONSOLE_SHELL_KEY, false)
if (!shellHost) provide(CONSOLE_SHELL_KEY, true)

const appStore = useAppStore()
const authStore = useAuthStore()
const { isConsoleSignal, isAdminSignal } = useConsoleSignal()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

// Page headers and <PageNavbar> / <PageToolbar> inject this to reach the navbar and toolbar.
const mainRef = ref<HTMLElement | null>(null)
let replayTour: (() => void) | undefined

if (!shellHost) {
  provideDashboardFrame()
  usePanelScroll(computed(() => mainRef.value?.parentElement ?? null))

  const { replayTour: replay } = useOnboardingTour({
    storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
    autoStart: !authStore.isAdminObserver
  })
  replayTour = replay

  const onboardingStore = useOnboardingStore()
  let petalEnableTimer = 0
  let petalEnableIdleId = 0

  onMounted(() => {
    onboardingStore.setReplayCallback(replay)
    if (isConsoleSignal.value) {
      const enable = () => document.documentElement.classList.add('console-petals-ready')
      const idleWindow = window as unknown as { requestIdleCallback?: (callback: () => void, options: { timeout: number }) => number }
      petalEnableIdleId = idleWindow.requestIdleCallback?.(enable, { timeout: 1500 }) ?? 0
      if (!petalEnableIdleId) petalEnableTimer = window.setTimeout(enable, 500)
    }
  })

  onBeforeUnmount(() => {
    document.documentElement.classList.remove('console-petals-ready')
    window.clearTimeout(petalEnableTimer)
    const idleWindow = window as unknown as { cancelIdleCallback?: (id: number) => void }
    if (petalEnableIdleId) idleWindow.cancelIdleCallback?.(petalEnableIdleId)
  })
}

defineExpose({ replayTour })
</script>

<style>
.console-frame {
  position: relative;
  isolation: isolate;
  min-height: 100dvh;
}

/* Transparent so the sakura canvas behind it shows in empty areas. */
.console-frame .console-panel {
  background: transparent;
}

.console-frame .console-panel-body {
  /* Nuxt UI's body is a flex column with gap-4 / sm:gap-6; the page is one child. */
  gap: 0;
}

.console-frame .signal-main {
  width: 100%;
  min-width: 0;
}

/* Sticky offsets that pages wrote for the old window scroll (under a 64px header). The panel
   body scrolls below the navbar now and sticky counts from inside its padding, so 0 keeps
   them where they rest and sticks them at the body's content edge. */
.console-frame .signal-main :is(.settings-tabs-shell, .pay-summary) {
  top: 0;
}

/* Table pages fill the panel height; their table scrolls inside (TablePageLayout). */
.console-frame .signal-main:has(> .table-page-layout:not(.mobile-mode)) {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  flex-direction: column;
}

/* Phones: keep the last content row clear of the home indicator. A no-op while the
   document has no viewport-fit=cover. */
@media (max-width: 639px) {
  .console-frame .console-panel-body {
    padding-bottom: calc(1rem + env(safe-area-inset-bottom, 0px));
  }
}
</style>
