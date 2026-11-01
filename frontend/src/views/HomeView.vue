<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <div
    v-else
    :data-testid="compactHomeEnabled ? 'compact-home' : undefined"
    class="flex min-h-screen flex-col bg-default text-default"
  >
    <PublicHeader :site-name="siteName" :logo="siteLogo">
      <template #actions>
        <LocaleSwitcher />
        <UButton
          v-if="docUrl"
          :href="docUrl"
          external
          target="_blank"
          icon="i-lucide-book-open"
          color="neutral"
          variant="ghost"
          square
          :aria-label="t('home.viewDocs')"
          :title="t('home.viewDocs')"
        />
        <UButton
          v-if="showModelPlazaEntry"
          to="/model-plaza"
          icon="i-lucide-layout-grid"
          color="neutral"
          variant="ghost"
          :title="t('nav.modelPlaza')"
          :ui="{ label: 'hidden sm:inline' }"
          :label="t('nav.modelPlaza')"
        />
        <ThemeToggleButton />
        <UButton
          :to="isAuthenticated ? dashboardPath : '/login'"
          :label="isAuthenticated ? t('home.dashboard') : t('home.login')"
          :trailing-icon="isAuthenticated ? 'i-lucide-arrow-up-right' : undefined"
          color="neutral"
          :variant="isAuthenticated ? 'outline' : 'solid'"
          class="ms-1"
        />
      </template>
    </PublicHeader>

    <!-- Compact Home Page -->
    <main v-if="compactHomeEnabled" class="flex min-w-0 flex-1 items-center">
      <UPageHero
        :title="siteName"
        :description="siteSubtitle"
        class="w-full"
        :ui="{
          container: 'py-16 sm:py-24 lg:py-24',
          title: 'wrap-anywhere text-3xl sm:text-4xl',
          description: 'mx-auto max-w-2xl whitespace-pre-wrap wrap-anywhere text-base sm:text-lg',
          links: 'mt-8'
        }"
      >
        <template #headline>
          <img :src="siteLogo || '/logo.svg'" alt="" class="mx-auto size-16 rounded-xl object-contain" />
        </template>
        <template #links>
          <UButton
            :to="isAuthenticated ? dashboardPath : '/login'"
            :label="isAuthenticated ? t('home.goToDashboard') : t('home.login')"
            size="lg"
          />
        </template>
      </UPageHero>
    </main>

    <!-- Default Home Page -->
    <main v-else class="flex-1">
      <UPageHero
        :title="siteName"
        :description="siteSubtitle"
        :ui="{
          container: 'pt-16 sm:pt-24 lg:pt-28 pb-16 sm:pb-20 lg:pb-24 gap-12 sm:gap-y-16',
          title: 'text-4xl sm:text-6xl lg:text-7xl tracking-tighter',
          description: 'mx-auto mt-5 max-w-2xl text-base sm:text-lg',
          links: 'mt-8 gap-3'
        }"
      >
        <template #headline>
          <div class="flex flex-wrap justify-center gap-2">
            <UBadge
              v-for="tag in tags"
              :key="tag.key"
              :icon="tag.icon"
              :label="t(`home.tags.${tag.key}`)"
              color="neutral"
              variant="subtle"
              size="lg"
              class="rounded-full px-3"
            />
          </div>
        </template>
        <template #links>
          <UButton
            :to="isAuthenticated ? dashboardPath : '/login'"
            :label="isAuthenticated ? t('home.goToDashboard') : t('home.getStarted')"
            trailing-icon="i-lucide-arrow-right"
            size="xl"
          />
          <UButton
            v-if="docUrl"
            :href="docUrl"
            external
            target="_blank"
            :label="t('home.docs')"
            icon="i-lucide-book-open"
            color="neutral"
            variant="subtle"
            size="xl"
          />
        </template>

        <!-- Terminal -->
        <div class="terminal-container mx-auto w-full max-w-2xl">
          <div class="overflow-hidden rounded-xl border border-default bg-muted">
            <div class="flex items-center gap-1.5 border-b border-default px-4 py-3">
              <span class="size-2.5 rounded-full border border-default bg-elevated"></span>
              <span class="size-2.5 rounded-full border border-default bg-elevated"></span>
              <span class="size-2.5 rounded-full border border-default bg-elevated"></span>
              <span class="ms-auto font-mono text-xs text-dimmed">terminal</span>
            </div>
            <div class="terminal-body px-5 py-5 text-left font-mono text-[13px] leading-loose sm:px-6">
              <div class="code-line line-1">
                <span class="text-dimmed">$</span>
                <span class="text-highlighted">curl</span>
                <span class="text-primary">-X POST</span>
                <span class="text-info">/v1/messages</span>
              </div>
              <div class="code-line line-2">
                <span class="italic text-muted"># Routing to upstream...</span>
              </div>
              <div class="code-line line-3">
                <span class="font-semibold text-success">200 OK</span>
                <span class="text-toned">{ "content": "Hello!" }</span>
              </div>
              <div class="code-line line-4">
                <span class="text-dimmed">$</span>
                <span class="cursor"></span>
              </div>
            </div>
          </div>
        </div>
      </UPageHero>

      <!-- Features -->
      <UPageSection :ui="{ container: 'max-w-5xl pt-0 sm:pt-0 lg:pt-0 pb-16 sm:pb-20 lg:pb-24' }">
        <div class="overflow-hidden rounded-xl border border-default">
          <div class="grid grid-cols-1 gap-px bg-(--ui-border) md:grid-cols-3">
            <UPageCard
              v-for="feature in features"
              :key="feature.key"
              :icon="feature.icon"
              :title="t(`home.features.${feature.key}`)"
              :description="t(`home.features.${feature.key}Desc`)"
              variant="ghost"
              class="rounded-none bg-default"
              :ui="{
                leading: 'mb-4 flex size-9 items-center justify-center rounded-lg bg-primary/10',
                leadingIcon: 'size-5',
                description: 'text-sm leading-relaxed text-muted'
              }"
            />
          </div>
        </div>
      </UPageSection>

      <!-- Supported Providers -->
      <UPageSection
        :title="t('home.providers.title')"
        :description="t('home.providers.description')"
        :ui="{
          container: 'max-w-5xl pt-0 sm:pt-0 lg:pt-0 pb-20 sm:pb-24 lg:pb-28 gap-8 sm:gap-10',
          title: 'text-2xl sm:text-3xl lg:text-3xl',
          description: 'mt-3 text-sm sm:text-base'
        }"
      >
        <div class="overflow-hidden rounded-xl border border-default">
          <div class="grid grid-cols-2 gap-px bg-(--ui-border) sm:grid-cols-3 lg:grid-cols-5">
            <div
              v-for="provider in providers"
              :key="provider.key"
              class="flex flex-col items-center gap-2.5 bg-default px-4 py-6 text-center last:col-span-2 lg:last:col-span-1"
            >
              <PlatformIcon
                v-if="provider.platform"
                :platform="provider.platform"
                size="lg"
                class="text-highlighted"
              />
              <UIcon v-else name="i-lucide-plus" class="size-6 text-muted" />
              <span class="text-sm font-semibold text-highlighted">{{ provider.name }}</span>
              <UBadge
                :label="provider.supported ? t('home.providers.supported') : t('home.providers.soon')"
                :color="provider.supported ? 'success' : 'neutral'"
                variant="subtle"
                size="sm"
              />
            </div>
          </div>
        </div>
      </UPageSection>
    </main>

    <!-- Footer -->
    <footer class="border-t border-default">
      <UContainer class="flex flex-col items-center gap-4 py-8 text-center">
        <p class="max-w-3xl text-xs leading-6 text-muted wrap-anywhere">{{ t('home.footer.riskStrip') }}</p>
        <LegalFooterLinks />
        <div class="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 text-sm text-muted">
          <span v-if="compactHomeEnabled">&copy; {{ currentYear }} {{ siteName }}</span>
          <span v-else>&copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}</span>
          <template v-if="!compactHomeEnabled">
            <UButton
              v-if="docUrl"
              :href="docUrl"
              external
              target="_blank"
              :label="t('home.docs')"
              color="neutral"
              variant="link"
              size="sm"
            />
            <UButton
              :href="githubUrl"
              external
              target="_blank"
              label="GitHub"
              color="neutral"
              variant="link"
              size="sm"
            />
          </template>
        </div>
      </UContainer>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import LegalFooterLinks from '@/components/legal/LegalFooterLinks.vue'
import PublicHeader from '@/views/public/components/PublicHeader.vue'
import ThemeToggleButton from '@/views/public/components/ThemeToggleButton.vue'
import { sanitizeUrl } from '@/utils/url'
import { resolveDocsUrl } from '@/utils/docsUrl'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import type { GroupPlatform } from '@/types'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const docUrl = computed(() => resolveDocsUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// GitHub URL
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const modelPlazaRequiresAuth = computed(
  () => appStore.cachedPublicSettings?.model_plaza_require_auth === true,
)
const showModelPlazaEntry = computed(
  () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value),
)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

const tags = [
  { key: 'subscriptionToApi', icon: 'i-lucide-arrow-left-right' },
  { key: 'stickySession', icon: 'i-lucide-shield-check' },
  { key: 'realtimeBilling', icon: 'i-lucide-chart-column' }
] as const

const features = [
  { key: 'unifiedGateway', icon: 'i-lucide-server' },
  { key: 'multiAccount', icon: 'i-lucide-users' },
  { key: 'balanceQuota', icon: 'i-lucide-wallet' }
] as const

const providers = computed<Array<{ key: string; platform?: GroupPlatform; name: string; supported: boolean }>>(() => [
  { key: 'claude', platform: 'anthropic', name: t('home.providers.claude'), supported: true },
  { key: 'openai', platform: 'openai', name: 'GPT', supported: true },
  { key: 'gemini', platform: 'gemini', name: t('home.providers.gemini'), supported: true },
  { key: 'antigravity', platform: 'antigravity', name: t('home.providers.antigravity'), supported: true },
  { key: 'more', name: t('home.providers.more'), supported: false }
])

onMounted(() => {
  // Light/dark comes from the shared site theme (main.ts): the visitor's saved choice,
  // else the site default mode, where "system" follows the operating system.

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
/* Terminal lines type in one after another; the cursor blinks. */
.code-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  opacity: 0;
  animation: line-appear 0.5s ease forwards;
}

.line-1 { animation-delay: 0.3s; }
.line-2 { animation-delay: 1s; }
.line-3 { animation-delay: 1.8s; }
.line-4 { animation-delay: 2.5s; }

@keyframes line-appear {
  from { opacity: 0; transform: translateY(5px); }
  to { opacity: 1; transform: translateY(0); }
}

.cursor {
  display: inline-block;
  width: 8px;
  height: 16px;
  background: var(--ui-primary);
  animation: blink 1s step-end infinite;
}

@keyframes blink {
  0%, 50% { opacity: 1; }
  51%, 100% { opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
  .code-line { opacity: 1; animation: none; }
  .cursor { animation: none; }
}
</style>
