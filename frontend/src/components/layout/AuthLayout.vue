<template>
  <!-- Nuxt UI's sign-in layout: one card in the middle of a plain page, brand in the corner. -->
  <div class="auth-paper relative flex min-h-screen flex-col bg-default">
    <!-- Ink-wash sakura on the right, above the page and below the card -->
    <InkSakura front />

    <header class="relative z-10 flex h-16 shrink-0 items-center px-4 sm:px-8">
      <template v-if="settingsLoaded">
        <router-link
          v-if="!backendModeEnabled"
          to="/home"
          class="inline-flex items-center gap-2.5 rounded-md text-highlighted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        >
          <img :src="siteLogo || '/logo.svg'" alt="" class="size-7 rounded-md object-contain" />
          <span class="text-base font-semibold">{{ siteName }}</span>
        </router-link>
        <div v-else class="inline-flex items-center gap-2.5 text-highlighted">
          <img :src="siteLogo || '/logo.svg'" alt="" class="size-7 rounded-md object-contain" />
          <span class="text-base font-semibold">{{ siteName }}</span>
        </div>
      </template>
    </header>

    <main class="relative z-10 flex flex-1 items-center justify-center px-4 py-6">
      <UPageCard variant="subtle" class="w-full max-w-sm bg-muted">
        <slot />
        <div v-if="$slots.footer" class="text-center text-sm text-muted">
          <slot name="footer" />
        </div>
      </UPageCard>
    </main>

    <footer class="relative z-10 space-y-2 px-4 pb-6 text-center text-muted">
      <LegalFooterLinks />
      <p class="text-xs text-dimmed">&copy; {{ currentYear }} {{ siteName }}</p>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import LegalFooterLinks from '@/components/legal/LegalFooterLinks.vue'
import InkSakura from './InkSakura.vue'
// The OAuth callback pages still use the shared .btn/.input classes, styled under .auth-paper.
import '@/styles/console-elevation.css'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
// In backend mode every public page redirects to /login, so the brand is not a link there.
const backendModeEnabled = computed(() => appStore.backendModeEnabled)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>
