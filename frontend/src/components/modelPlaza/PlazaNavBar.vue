<template>
  <PublicHeader :site-name="siteName" :logo="siteLogo">
    <template v-if="!settings" #brand>
      <span class="flex items-center gap-2.5" aria-hidden="true">
        <USkeleton class="size-8 rounded-md" />
        <USkeleton class="h-5 w-28" />
      </span>
    </template>
    <template #actions>
      <UButton
        v-if="isAuthenticated"
        :to="backTarget"
        :label="t('modelPlaza.nav.backToDashboard')"
        color="neutral"
      />
      <UButton
        v-else
        :to="{ path: '/login', query: { redirect: '/model-plaza' } }"
        :label="t('modelPlaza.nav.login')"
        color="neutral"
      />
    </template>
  </PublicHeader>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PublicHeader from '@/views/public/components/PublicHeader.vue'
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
