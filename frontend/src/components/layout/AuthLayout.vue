<template>
  <!--
    min-h-svh: `100vh` is the tallest viewport (browser chrome collapsed), so on a phone
    the page is taller than the visible area before any content loads.
    overflow-x-hidden + top alignment: a login card with OAuth buttons can be taller than
    the viewport; `overflow-hidden` + `items-center` used to clip its top edge and make it
    unreachable. Vertical scrolling stays available, horizontal stays clipped for the art.
  -->
  <div class="auth-paper relative flex min-h-svh items-start justify-center overflow-x-hidden p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:items-center">
    <!-- Background: plain white paper by day; the night background and grid are unchanged -->
    <div
      class="absolute inset-0 hidden bg-linear-to-br/srgb dark:block dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
    ></div>
    <div class="pointer-events-none absolute inset-0 hidden overflow-hidden dark:block">
      <div class="auth-grid absolute inset-0"></div>
    </div>

    <!-- Ink-wash sakura on the right, above the background and below the form -->
    <InkSakura front />

    <!-- Content: brand above a centred card, as in Nuxt UI's auth pages -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-6 flex flex-col items-center text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div
            class="mb-4 inline-flex h-12 w-12 items-center justify-center overflow-hidden rounded-xl dark:shadow-lg dark:shadow-primary-500/30"
          >
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <!-- Brand text must stay readable in both themes: use the semantic text
               tokens instead of a hardcoded colour. The old scoped title style
               painted the site name transparent and it could vanish on the dark
               auth background. -->
          <h1 class="text-2xl font-semibold text-highlighted">
            {{ siteName }}
          </h1>
          <p class="mt-1 text-sm text-muted">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container: Quiet variant — one card, 2xl corners, hairline-free, light shadow -->
      <div class="auth-card overflow-hidden rounded-2xl bg-default p-5 sm:p-6">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <div class="mt-5 text-muted">
        <LegalFooterLinks />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-muted">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import LegalFooterLinks from '@/components/legal/LegalFooterLinks.vue'
import InkSakura from './InkSakura.vue'
import '@/styles/console-elevation.css'
import '@/styles/auth-quiet.css'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
@reference "../../style.css";
.auth-grid {
  background-image:
    linear-gradient(rgba(58, 83, 117, 0.07) 1px, transparent 1px),
    linear-gradient(90deg, rgba(58, 83, 117, 0.07) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: radial-gradient(circle at center, black 0%, transparent 78%);
  animation: auth-grid-drift 28s linear infinite;
}

:global(html.dark) .auth-grid {
  background-image:
    linear-gradient(rgba(145, 169, 201, 0.11) 1px, transparent 1px),
    linear-gradient(90deg, rgba(145, 169, 201, 0.11) 1px, transparent 1px);
}

@keyframes auth-grid-drift {
  from { background-position: 0 0; }
  to { background-position: 48px 48px; }
}

@media (prefers-reduced-motion: reduce) {
  .auth-grid { animation: none; }
}
</style>


