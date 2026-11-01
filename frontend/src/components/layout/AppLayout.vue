<template>
  <div
    class="console-frame"
    :class="[isConsoleSignal ? 'console-signal' : '', { 'signal-admin': isAdminSignal, 'signal-nav-collapsed': sidebarCollapsed }]"
  >
    <!-- Nuxt UI dashboard: UDashboardGroup > sidebar + one panel (navbar, optional toolbar,
         body). The panel body is the scroll container; the sakura canvas sits behind. -->
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
import { computed, onMounted, ref } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import { useConsoleSignal } from '@/composables/useConsoleSignal'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import ConsoleNavigationSearch from './ConsoleNavigationSearch.vue'
import InkSakura from './InkSakura.vue'
import { provideDashboardFrame } from './dashboardFrame'
import { usePanelScroll } from './panelScroll'

const appStore = useAppStore()
const authStore = useAuthStore()
const { isConsoleSignal, isAdminSignal } = useConsoleSignal()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

// Page headers and <PageNavbar> / <PageToolbar> inject this to reach the navbar and toolbar.
provideDashboardFrame()

const mainRef = ref<HTMLElement | null>(null)
usePanelScroll(computed(() => mainRef.value?.parentElement ?? null))

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: !authStore.isAdminObserver
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

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
