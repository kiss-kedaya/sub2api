<template>
  <header class="plaza-public-nav">
    <div class="plaza-public-nav-inner">
      <div class="flex min-w-0 items-center gap-3">
        <template v-if="settings">
          <span class="plaza-public-logo">
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-semibold">
            {{ siteName }}
          </span>
        </template>
        <template v-else>
          <span class="h-9 w-9 flex-shrink-0 animate-pulse rounded-lg bg-[var(--signal-inset,#ededf2)]" aria-hidden="true"></span>
          <span class="h-5 w-28 animate-pulse rounded bg-[var(--signal-inset,#ededf2)]" aria-hidden="true"></span>
        </template>
      </div>

      <RouterLink
        v-if="isAuthenticated"
        :to="backTarget"
        class="btn btn-primary"
      >
        {{ t('modelPlaza.nav.backToDashboard') }}
      </RouterLink>
      <RouterLink
        v-else
        :to="{ path: '/login', query: { redirect: '/model-plaza' } }"
        class="btn btn-primary"
      >
        {{ t('modelPlaza.nav.login') }}
      </RouterLink>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { sanitizeUrl } from '@/utils/url'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const settings = computed(() => appStore.cachedPublicSettings)
const siteName = computed(() => settings.value?.site_name || 'Sub2API')
const siteLogo = computed(() =>
  sanitizeUrl(settings.value?.site_logo || '', { allowRelative: true, allowDataUrl: true })
)
const isAuthenticated = computed(() => authStore.isAuthenticated)
const backTarget = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
</script>

<style scoped>
.plaza-public-nav {
  position: sticky;
  top: 0;
  z-index: 30;
  background: var(--signal-surface, #ffffff);
  border-bottom: 1px solid var(--signal-line, #dedee4);
  color: var(--signal-text, #1c1c1e);
}

.plaza-public-nav-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  max-width: 80rem;
  margin-inline: auto;
  padding: 12px 16px;
}

.plaza-public-logo {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  overflow: hidden;
  border: 1px solid var(--signal-line, #dedee4);
  border-radius: 8px;
  background: var(--signal-surface, #ffffff);
}

@media (min-width: 640px) {
  .plaza-public-nav-inner {
    padding-inline: 24px;
  }
}
</style>
