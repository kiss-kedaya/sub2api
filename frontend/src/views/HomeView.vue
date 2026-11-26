<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="safeHomeUrl"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals"
      referrerpolicy="no-referrer"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="safeHomeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-slate-100"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-700/80">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/logo.svg'"
            alt="Logo"
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <router-link
            v-if="docUrl && !docsLink.external"
            :to="docsLink.to"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </router-link>
          <a
            v-else-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            to="/docs?page=agent-setup"
            data-testid="compact-home-agent-setup"
            class="flex h-10 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('home.agentSetup.navLabel')"
          >
            <Icon name="terminal" size="md" />
            <span class="hidden sm:inline">{{ t('home.agentSetup.navLabel') }}</span>
          </router-link>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="flex h-10 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
            <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
          </router-link>
          <button
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img
          :src="siteLogo || '/logo.svg'"
          alt="Logo"
          class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain"
        />
        <h1 class="wrap-anywhere text-4xl font-semibold tracking-[-0.03em] md:text-5xl">{{ siteName }}</h1>
        <p class="mt-5 whitespace-pre-wrap wrap-anywhere text-[17px] leading-relaxed text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <div class="mt-9 flex flex-wrap items-center justify-center gap-3.5">
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-12 items-center justify-center rounded-full bg-primary-600 px-7 py-3 text-[15px] font-medium text-white transition hover:bg-primary-700"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
          </router-link>
          <router-link
            to="/docs?page=agent-setup"
            data-testid="compact-home-agent-setup-cta"
            class="inline-flex min-h-12 items-center justify-center gap-2 rounded-full border border-gray-300 bg-white px-6 py-3 text-[15px] font-medium text-gray-700 transition hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200 dark:hover:bg-dark-800"
          >
            <Icon name="terminal" size="sm" />
            <span>{{ t('home.agentSetup.navLabel') }}</span>
          </router-link>
        </div>
      </div>
    </main>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 wrap-anywhere sm:px-6 dark:border-dark-800 dark:text-dark-400">
      <p class="mx-auto max-w-3xl text-xs leading-6">{{ t('home.footer.riskStrip') }}</p>
      <p class="mt-3">&copy; {{ currentYear }} {{ siteName }}</p>
      <div class="mt-3">
        <LegalFooterLinks />
      </div>
    </footer>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    data-testid="default-home"
    class="relative flex min-h-screen flex-col bg-default text-default"
  >
    <!-- Background decoration: a static hairline grid that fades out below the hero. -->
    <div class="home-grid pointer-events-none absolute inset-0 opacity-50 dark:opacity-35" aria-hidden="true"></div>

    <!-- Header -->
    <header class="relative z-20 px-4 py-4 sm:px-6">
      <nav class="mx-auto flex max-w-7xl items-center justify-between gap-3">
        <router-link
          to="/home"
          class="flex min-w-0 items-center gap-2.5 rounded-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        >
          <span
            class="flex h-9 w-9 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-default bg-elevated"
          >
            <img :src="siteLogo || '/logo.svg'" alt="" class="h-full w-full object-contain" />
          </span>
          <span class="min-w-0 truncate text-sm font-semibold text-highlighted">{{ siteName }}</span>
        </router-link>

        <div class="flex shrink-0 items-center gap-1 sm:gap-2">
          <LocaleSwitcher />

          <router-link
            v-if="docUrl && !docsLink.external"
            :to="docsLink.to"
            class="hidden h-9 w-9 items-center justify-center rounded-full text-muted transition-colors hover:bg-elevated hover:text-highlighted sm:flex"
            :title="t('home.viewDocs')"
            :aria-label="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </router-link>
          <a
            v-else-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="hidden h-9 w-9 items-center justify-center rounded-full text-muted transition-colors hover:bg-elevated hover:text-highlighted sm:flex"
            :title="t('home.viewDocs')"
            :aria-label="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>

          <router-link
            data-testid="home-header-agent-setup"
            to="/docs?page=agent-setup"
            class="hidden h-9 items-center gap-1.5 rounded-md px-2 text-sm text-muted transition-colors hover:bg-elevated hover:text-highlighted sm:flex"
            :title="t('home.agentSetup.navLabel')"
          >
            <Icon name="terminal" size="md" />
            <span class="hidden md:inline">{{ t('home.agentSetup.navLabel') }}</span>
          </router-link>

          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="hidden h-9 items-center gap-1.5 rounded-md px-2 text-sm text-muted transition-colors hover:bg-elevated hover:text-highlighted sm:flex"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
            <span class="hidden md:inline">{{ t('nav.modelPlaza') }}</span>
          </router-link>

          <UButton
            color="neutral"
            variant="ghost"
            size="md"
            square
            :icon="isDark ? 'i-lucide-sun' : 'i-lucide-moon'"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          />

          <UButton
            data-testid="home-header-cta"
            :to="headerCta.to"
            size="md"
            color="neutral"
            :variant="isAuthenticated ? 'solid' : 'outline'"
            class="ml-0.5 shrink-0"
          >
            {{ headerCta.label }}
          </UButton>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="relative z-10 flex-1 px-4 sm:px-6">
      <!-- Hero: one sentence, two ways in. -->
      <section data-testid="home-hero" class="mx-auto max-w-4xl pt-16 text-center sm:pt-28">
        <h1
          class="text-balance text-4xl font-semibold leading-[1.1] tracking-[-0.03em] text-highlighted sm:text-5xl md:text-[54px]"
        >
          {{ t('home.heroSubtitle') }}
        </h1>
        <p class="mx-auto mt-6 max-w-2xl text-pretty text-[17px] leading-[1.68] text-muted sm:text-[19px]">
          {{ heroDescription }}
        </p>
        <div class="mt-9 flex flex-col items-stretch justify-center gap-3.5 sm:flex-row sm:items-center">
          <UButton
            data-testid="home-hero-cta"
            :to="primaryCta.to"
            size="lg"
            color="neutral"
            variant="solid"
            trailing-icon="i-lucide-arrow-right"
            class="min-h-12 justify-center rounded-full px-7 text-[15px] font-medium"
          >
            {{ primaryCta.label }}
          </UButton>
          <UButton
            data-testid="home-hero-docs"
            :to="docsLink.to"
            :target="docsLink.external ? '_blank' : undefined"
            :rel="docsLink.external ? 'noopener noreferrer' : undefined"
            size="lg"
            color="neutral"
            variant="outline"
            class="min-h-12 justify-center rounded-full px-7 text-[15px]"
          >
            {{ t('home.viewDocs') }}
          </UButton>
        </div>
      </section>

      <!-- Key figures + supported platforms. Cards render only for data the APIs actually returned. -->
      <section data-testid="home-status" class="mx-auto mt-16 max-w-6xl sm:mt-20">
        <div v-if="stats.length">
          <h2 id="home-stats-title" class="text-[15px] font-semibold text-highlighted">{{ t('home.stats.title') }}</h2>

          <ul data-testid="home-stats" class="mt-4 flex flex-wrap gap-3.5">
            <UCard
              v-for="stat in stats"
              :key="stat.key"
              as="li"
              :ui="{ root: 'w-[calc(50%-0.4375rem)] min-w-0 rounded-2xl bg-elevated sm:w-52', body: 'px-5 py-5' }"
            >
              <span data-stat-label class="block text-xs font-medium text-toned">{{ stat.label }}</span>
              <span
                data-stat-value
                class="mt-2 block truncate text-2xl font-semibold tabular-nums tracking-tight text-highlighted"
              >
                {{ stat.value }}
              </span>
            </UCard>
          </ul>
        </div>

        <div :class="stats.length ? 'mt-9' : ''">
          <h2 id="home-providers-title" class="text-[13px] font-medium text-muted">{{ t('home.providers.title') }}</h2>
          <ul data-testid="home-provider-list" class="mt-3 flex flex-wrap gap-2.5">
            <li
              v-for="provider in providers"
              :key="provider.key"
              class="inline-flex items-center gap-2 rounded-full border border-default bg-elevated py-2 pl-3 pr-4"
            >
              <PlatformIcon v-if="provider.platform" :platform="provider.platform" size="md" />
              <Icon v-else name="plus" size="sm" class="text-dimmed" />
              <span class="text-[14px] font-medium text-toned">{{ provider.name }}</span>
            </li>
          </ul>
        </div>
      </section>

      <!-- Quick start: three steps, vertical flow so it never reads as a feature-card row. -->
      <section
        data-testid="home-quickstart"
        class="mx-auto mt-20 max-w-6xl pb-14"
        aria-labelledby="home-quickstart-title"
      >
        <h2 id="home-quickstart-title" class="text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-highlighted sm:text-[28px]">
          {{ t('home.quickStart.title') }}
        </h2>
        <p class="mt-2.5 max-w-2xl text-[15px] leading-7 text-muted">
          {{ t('home.quickStart.description') }}
        </p>

        <ol class="mt-9 border-t border-default">
          <!-- Step 1: create a key -->
          <li class="grid gap-3 border-b border-default py-7 sm:grid-cols-[3.5rem_minmax(0,1fr)] sm:gap-4">
            <span class="font-mono text-sm tabular-nums text-muted" aria-hidden="true">01</span>
            <div class="min-w-0">
              <h3 class="text-[18px] font-semibold text-highlighted">
                {{ t('home.quickStart.createKey.title') }}
              </h3>
              <p class="mt-2 text-[15px] leading-7 text-muted">
                {{ t('home.quickStart.createKey.description') }}
              </p>
              <div class="mt-4">
                <UButton
                  data-testid="home-key-action"
                  :to="keyAction.to"
                  size="md"
                  color="neutral"
                  variant="outline"
                  leading-icon="i-lucide-key-round"
                  class="rounded-full px-5"
                >
                  {{ keyAction.label }}
                </UButton>
              </div>
            </div>
          </li>

          <!-- Step 2: point the SDK at the base URL -->
          <li class="grid gap-3 border-b border-default py-7 sm:grid-cols-[3.5rem_minmax(0,1fr)] sm:gap-4">
            <span class="font-mono text-sm tabular-nums text-muted" aria-hidden="true">02</span>
            <div class="min-w-0">
              <h3 class="text-[18px] font-semibold text-highlighted">
                {{ t('home.quickStart.config.title') }}
              </h3>
              <p class="mt-2 text-[15px] leading-7 text-muted">
                {{ t('home.quickStart.config.description') }}
              </p>
              <div class="mt-4 overflow-hidden rounded-2xl border border-default bg-muted">
                <div class="flex items-center justify-between gap-2 border-b border-default py-1.5 pl-4 pr-2">
                  <span class="truncate font-mono text-xs text-muted">base_url</span>
                  <UButton
                    data-testid="home-copy-base-url"
                    size="xs"
                    color="neutral"
                    variant="ghost"
                    :icon="copiedField === 'baseUrl' ? 'i-lucide-check' : 'i-lucide-copy'"
                    :aria-label="copiedField === 'baseUrl' ? t('common.copied') : t('common.copy')"
                    @click="copyExample('baseUrl', sdkBaseUrl)"
                  >
                    <span aria-live="polite">{{ copiedField === 'baseUrl' ? t('common.copied') : t('common.copy') }}</span>
                  </UButton>
                </div>
                <pre data-testid="home-base-url-example" class="overflow-x-auto px-4 py-3.5 font-mono text-[13.5px] leading-6 text-toned"><code>{{ sdkBaseUrl }}</code></pre>
              </div>
            </div>
          </li>

          <!-- Step 3: send the first request -->
          <li class="grid gap-3 border-b border-default py-7 sm:grid-cols-[3.5rem_minmax(0,1fr)] sm:gap-4">
            <span class="font-mono text-sm tabular-nums text-muted" aria-hidden="true">03</span>
            <div class="min-w-0">
              <h3 class="text-[18px] font-semibold text-highlighted">
                {{ t('home.quickStart.request.title') }}
              </h3>
              <p class="mt-2 text-[15px] leading-7 text-muted">
                {{ t('home.quickStart.request.description') }}
              </p>
              <div class="mt-4 overflow-hidden rounded-2xl border border-default bg-muted">
                <div class="flex items-center justify-between gap-2 border-b border-default py-1.5 pl-4 pr-2">
                  <span class="truncate font-mono text-xs text-muted">curl</span>
                  <UButton
                    data-testid="home-copy-request"
                    size="xs"
                    color="neutral"
                    variant="ghost"
                    :icon="copiedField === 'request' ? 'i-lucide-check' : 'i-lucide-copy'"
                    :aria-label="copiedField === 'request' ? t('common.copied') : t('common.copy')"
                    @click="copyExample('request', requestExample)"
                  >
                    <span aria-live="polite">{{ copiedField === 'request' ? t('common.copied') : t('common.copy') }}</span>
                  </UButton>
                </div>
                <pre data-testid="home-request-example" class="overflow-x-auto px-4 py-3.5 font-mono text-[13.5px] leading-6 text-toned"><code>{{ requestExample }}</code></pre>
              </div>
              <div class="mt-4">
                <UButton
                  data-testid="home-docs-link"
                  :to="docsLink.to"
                  :target="docsLink.external ? '_blank' : undefined"
                  :rel="docsLink.external ? 'noopener noreferrer' : undefined"
                  color="neutral"
                  variant="link"
                  size="sm"
                  trailing-icon="i-lucide-arrow-up-right"
                  class="px-0 text-[14px]"
                >
                  {{ t('home.quickStart.request.docs') }}
                </UButton>
              </div>
            </div>
          </li>
        </ol>
      </section>

      <!-- Agent Setup Launcher: airy cards tied to CC Switch + this site's Import to CCS flow -->
      <section
        data-testid="home-agent-setup"
        class="mx-auto mt-6 max-w-6xl border-t border-default pb-24 pt-16"
        aria-labelledby="home-agent-setup-title"
      >
        <div class="flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
          <div class="min-w-0">
            <h2 id="home-agent-setup-title" class="text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-highlighted sm:text-[28px]">
              {{ t('home.agentSetup.title') }}
            </h2>
            <p class="mt-2.5 max-w-2xl text-[15px] leading-7 text-muted">
              {{ t('home.agentSetup.description') }}
            </p>
          </div>
          <UButton
            data-testid="home-agent-setup-all"
            to="/docs?page=agent-setup"
            size="md"
            color="neutral"
            variant="outline"
            trailing-icon="i-lucide-arrow-right"
            class="shrink-0 self-start rounded-full px-5 sm:self-auto"
          >
            {{ t('home.agentSetup.viewAll') }}
          </UButton>
        </div>

        <ul class="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <li v-for="agent in homeAgentHighlights" :key="agent.id" class="min-w-0">
            <router-link
              :to="{ path: '/docs', query: { page: 'agent-setup', agent: agent.id } }"
              :data-testid="`home-agent-card-${agent.id}`"
              class="home-agent-card group flex h-full flex-col justify-between rounded-2xl border border-default bg-elevated p-6"
            >
              <div>
                <div class="flex items-center justify-between gap-2.5">
                  <div class="flex min-w-0 items-center gap-3">
                    <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-default bg-default">
                      <AgentBrandIcon :icon-key="agent.iconKey" :platform="agent.platform" size="md" />
                    </span>
                    <span class="truncate text-[16px] font-semibold text-highlighted">{{ agent.name }}</span>
                  </div>
                </div>
                <p class="mt-3.5 line-clamp-2 text-[14px] leading-6 text-muted">
                  {{ agent.summary }}
                </p>
              </div>
              <span class="mt-5 inline-flex items-center gap-1.5 text-[14px] font-medium text-highlighted">
                <span>{{ t('home.agentSetup.viewGuide') }}</span>
                <Icon name="arrowRight" size="xs" class="transition-transform group-hover:translate-x-0.5" />
              </span>
            </router-link>
          </li>
        </ul>

        <div class="mt-5 flex flex-col items-start justify-between gap-4 rounded-2xl border border-default bg-elevated px-5 py-4 sm:flex-row sm:items-center">
          <div class="flex min-w-0 items-center gap-3">
            <AgentBrandIcon icon-key="ccswitch" size="sm" class="shrink-0" />
            <p class="text-[14px] leading-6 text-muted">
              {{ t('home.agentSetup.ccSwitchStrip') }}
            </p>
          </div>
          <UButton
            data-testid="home-agent-setup-ccs"
            to="/docs?page=agent-setup#site-ccswitch-workflow"
            size="sm"
            color="neutral"
            variant="link"
            trailing-icon="i-lucide-arrow-right"
            class="shrink-0 px-0 text-[14px]"
          >
            {{ t('home.agentSetup.openCcSwitchGuide') }}
          </UButton>
        </div>
      </section>
    </main>

    <!-- Footer -->
    <footer class="relative z-10 border-t border-default px-4 py-8 sm:px-6">
      <div class="mx-auto flex max-w-7xl flex-col items-center gap-4 text-center">
        <p class="max-w-3xl text-xs leading-6 text-muted">
          {{ t('home.footer.riskStrip') }}
        </p>
        <LegalFooterLinks />
        <p class="text-xs text-muted">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <router-link
            v-if="docUrl && !docsLink.external"
            :to="docsLink.to"
            class="text-xs text-muted transition-colors hover:text-highlighted"
          >
            {{ t('home.docs') }}
          </router-link>
          <a
            v-else-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-muted transition-colors hover:text-highlighted"
          >
            {{ t('home.docs') }}
          </a>
          <a
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-muted transition-colors hover:text-highlighted"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import { toggleColorMode, useSiteColorMode } from '@/composables/useSiteTheme'
import { useClipboard } from '@/composables/useClipboard'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import AgentBrandIcon from '@/components/docs/AgentBrandIcon.vue'
import LegalFooterLinks from '@/components/legal/LegalFooterLinks.vue'
import { sanitizeUrl } from '@/utils/url'
import { sanitizeHtml } from '@/utils/sanitize'
import { resolveDocsUrl } from '@/utils/docsUrl'
import { apiSiteRoot } from '@/utils/apiBaseUrl'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { formatCurrency, formatNumber } from '@/utils/format'
import { getModelPlaza, type ModelPlazaResponse } from '@/api/modelPlaza'
import { getHomeAgentHighlights } from '@/data/agentSetupGuides'
import { PROJECT_GITHUB_URL } from '@/constants/project'
import type { GroupPlatform } from '@/types'

const i18nContext = useI18n()
const t = i18nContext.t
const activeLocale = computed(() => i18nContext.locale?.value || 'zh')

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
const safeHomeContent = computed(() => sanitizeHtml(homeContent.value))
const safeHomeUrl = computed(() => sanitizeUrl(homeContent.value.trim(), { allowRelative: false, allowDataUrl: false }))

const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(typeof document !== 'undefined' && document.documentElement.classList.contains('dark'))

// GitHub URL
const githubUrl = PROJECT_GITHUB_URL

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
const registrationEnabled = computed(() => appStore.cachedPublicSettings?.registration_enabled === true)

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Hero copy: the product promise is the heading, and the admin-configured subtitle carries the
// detail. When the subtitle repeats the promise verbatim (a copy/paste in site settings), fall
// back to the generic one-liner instead of printing the same sentence twice.
const heroDescription = computed(() => {
  const subtitle = siteSubtitle.value.trim()
  if (subtitle && subtitle !== t('home.heroSubtitle')) return subtitle
  return t('home.heroDescription')
})

// One primary action per state: visitors start an account, signed-in users return to the console.
const primaryCta = computed(() => {
  if (isAuthenticated.value) return { to: dashboardPath.value, label: t('home.goToDashboard') }
  return registrationEnabled.value
    ? { to: '/register', label: t('home.getStarted') }
    : { to: '/login', label: t('home.login') }
})
const headerCta = computed(() => isAuthenticated.value
  ? { to: dashboardPath.value, label: t('home.goToDashboard') }
  : { to: '/login', label: t('home.login') })
const keyAction = computed(() => isAuthenticated.value
  ? { to: '/keys', label: t('home.quickStart.createKey.openKeys') }
  : primaryCta.value)
// Docs target: resolveDocsUrl already falls back to the built-in docs page.
const docsLink = computed(() => {
  const to = docUrl.value
  return { to, external: /^https?:\/\//i.test(to) }
})

// API examples follow this site: the admin-configured endpoint when set, otherwise the current origin.
// The configured address is usually written with its /v1 suffix (OpenAI SDK style), so it is reduced to the
// site root first and /v1 is appended exactly once below; building from it as-is produced .../v1/v1/chat/completions.
const endpointBase = computed(() => {
  const configured = apiSiteRoot(appStore.apiBaseUrl || appStore.cachedPublicSettings?.api_base_url)
  if (configured) return configured
  if (typeof window !== 'undefined') return window.location.origin.replace(/\/+$/, '')
  return ''
})
const sdkBaseUrl = computed(() => (endpointBase.value ? `${endpointBase.value}/v1` : ''))
const homeAgentHighlights = computed(() => getHomeAgentHighlights(activeLocale.value, endpointBase.value || 'http://localhost'))

// Model plaza doubles as the public source for model/platform counts. It is only fetched when
// the feature is on and anonymous visitors are allowed to read it; any failure simply hides the
// figures instead of inventing them.
const plaza = ref<ModelPlazaResponse | null>(null)
let plazaRequested = false

async function loadPlazaStats() {
  if (plazaRequested || !modelPlazaEnabled.value) return
  if (!isAuthenticated.value && modelPlazaRequiresAuth.value) return
  plazaRequested = true
  try {
    plaza.value = await getModelPlaza()
  } catch {
    plaza.value = null
  }
}

const plazaTotals = computed(() => {
  const models = new Set<string>()
  const platforms = new Set<string>()
  const groups = plaza.value?.groups ?? []
  for (const group of groups) {
    if (group.platform) platforms.add(group.platform)
    for (const model of group.models ?? []) {
      if (model.name) models.add(model.name)
    }
  }
  return { models: models.size, platforms: platforms.size, groups: groups.length }
})

const exampleModelId = computed(() => {
  for (const group of plaza.value?.groups ?? []) {
    const model = group.models?.[0]?.name
    if (model) return model
  }
  return 'YOUR_MODEL_ID'
})

interface HomeStat {
  key: 'models' | 'platforms' | 'groups' | 'balance'
  label: string
  value: string
}

/** Only figures backed by a real API response; an unavailable source contributes no card. */
const stats = computed<HomeStat[]>(() => {
  const items: HomeStat[] = []
  if (plaza.value) {
    items.push(
      { key: 'models', label: t('home.stats.models'), value: formatNumber(plazaTotals.value.models) },
      { key: 'platforms', label: t('home.stats.platforms'), value: formatNumber(plazaTotals.value.platforms) },
    )
    if (isAuthenticated.value) {
      items.push({ key: 'groups', label: t('home.stats.groups'), value: formatNumber(plazaTotals.value.groups) })
    }
  }
  const balance = authStore.user?.balance
  if (isAuthenticated.value && typeof balance === 'number') {
    items.push({ key: 'balance', label: t('home.stats.balance'), value: formatCurrency(balance) })
  }
  return items
})

const providers = computed<Array<{ key: string; platform: GroupPlatform | null; name: string }>>(() => [
  { key: 'claude', platform: 'anthropic', name: t('home.providers.claude') },
  { key: 'gpt', platform: 'openai', name: 'GPT' },
  { key: 'gemini', platform: 'gemini', name: t('home.providers.gemini') },
  { key: 'antigravity', platform: 'antigravity', name: t('home.providers.antigravity') },
  { key: 'more', platform: null, name: t('home.providers.more') },
])

// First request example: real field names, the site's real endpoint, and a model ID that this
// site actually lists when the plaza is available.
const requestExample = computed(() => {
  const base = endpointBase.value
  if (!base) return ''
  const body = JSON.stringify({
    model: exampleModelId.value,
    messages: [{ role: 'user', content: 'Hello!' }],
  })
  return [
    `curl ${base}/v1/chat/completions \\`,
    '  -H "Authorization: Bearer $API_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '${body}'`,
  ].join('\n')
})

// Copy feedback lives on the button itself (icon + label) so it is visible without a toast in
// the corner; useClipboard also surfaces the site's standard success toast.
const { copyToClipboard } = useClipboard()
const copiedField = ref<'' | 'baseUrl' | 'request'>('')
let copiedTimer: ReturnType<typeof setTimeout> | null = null

async function copyExample(field: 'baseUrl' | 'request', text: string) {
  if (!text) return
  const ok = await copyToClipboard(text)
  if (!ok) return
  copiedField.value = field
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copiedField.value = ''
    copiedTimer = null
  }, 2000)
}

onUnmounted(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
})

// Toggle theme: the shared switch cross-fades the page and flips the class inside that transition, so follow it.
const colorMode = useSiteColorMode()
watch(colorMode.isDark, (dark) => { isDark.value = dark })

function toggleTheme() {
  toggleColorMode()
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }

  void loadPlazaStats()
})

// Settings may arrive after mount (no injected config); fetch the figures once the flag is known.
watch(modelPlazaEnabled, (enabled) => {
  if (enabled) void loadPlazaStats()
})
</script>

<style scoped>
/*
  The only decoration on the page: a static hairline grid that fades smoothly below the hero.
  Multi-stop radial easing prevents a visible edge; dark mode dials opacity down so obsidian stays calm.
*/
.home-grid {
  background-image:
    linear-gradient(color-mix(in oklab, var(--ui-border) 58%, transparent) 1px, transparent 1px),
    linear-gradient(90deg, color-mix(in oklab, var(--ui-border) 58%, transparent) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: radial-gradient(
    ellipse 88% 54% at 50% 0%,
    #000 0%,
    rgb(0 0 0 / 0.72) 34%,
    rgb(0 0 0 / 0.24) 62%,
    transparent 82%
  );
}

.home-agent-card {
  transition:
    transform 160ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)),
    border-color 150ms ease,
    background-color 150ms ease,
    box-shadow 160ms ease;
}

.home-agent-card:active {
  transform: scale(0.98);
}

@media (hover: hover) and (pointer: fine) {
  .home-agent-card:hover {
    transform: translateY(-2px);
    border-color: var(--ui-border-accented);
    box-shadow: var(--app-shadow-card);
  }
}

@media (prefers-reduced-motion: reduce) {
  .home-agent-card {
    transition: border-color 150ms ease, background-color 150ms ease;
  }
  .home-agent-card:hover,
  .home-agent-card:active {
    transform: none;
  }
}
</style>
