<template>
  <button type="button" class="console-search-trigger btn btn-secondary btn-icon" :title="t('common.search')" :aria-label="t('common.search')" @click="open = true">
    <Icon name="search" size="sm" />
  </button>
  <BaseDialog :show="open" :title="t('common.search')" width="normal" :close-on-click-outside="true" @close="open = false">
    <div class="console-navigation-search">
      <div class="relative">
        <Icon name="search" size="md" class="pointer-events-none absolute left-3 top-3 text-gray-400" />
        <input ref="searchInput" v-model="query" class="input pl-10" :aria-label="t('common.search')" :placeholder="t('common.search')" autocomplete="off" @keydown.enter.prevent="navigate(results[0]?.path)" />
      </div>
      <nav v-if="results.length" class="console-search-results" :aria-label="t('common.search')">
        <button v-for="item in results" :key="item.path" type="button" class="console-search-result" @click="navigate(item.path)">
          <Icon :name="item.icon" size="md" />
          <span>{{ item.label }}</span>
          <Icon name="chevronRight" size="sm" />
        </button>
      </nav>
      <p v-else class="py-8 text-center text-sm text-gray-500">{{ t('common.noData') }}</p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const app = useAppStore()
const open = ref(false)
const query = ref('')
const searchInput = ref<HTMLInputElement | null>(null)
const destinations = computed(() => {
  const personal = [
    { path: '/dashboard', label: t('nav.dashboard'), icon: 'chart' as const },
    { path: '/keys', label: t('nav.apiKeys'), icon: 'key' as const },
    ...(!auth.isSimpleMode ? [{ path: '/usage', label: t('nav.usage'), icon: 'chart' as const }] : []),
    ...(!auth.isSimpleMode ? [{ path: '/redeem', label: t('nav.redeem'), icon: 'gift' as const }] : []),
    { path: '/profile', label: t('nav.profile'), icon: 'user' as const },
    ...(isFeatureFlagEnabled(FeatureFlags.channelMonitor) ? [{ path: '/monitor', label: t('nav.channelStatus'), icon: 'server' as const }] : []),
    ...(!auth.isSimpleMode && isFeatureFlagEnabled(FeatureFlags.payment) ? [{ path: '/purchase', label: t('nav.buySubscription'), icon: 'creditCard' as const }] : []),
  ]
  if (auth.user?.role !== 'admin') return app.backendModeEnabled ? [] : personal
  const admin = [
    { path: '/admin/dashboard', label: t('admin.dashboard.title'), icon: 'chart' as const },
    { path: '/admin/accounts', label: t('admin.accounts.title'), icon: 'server' as const },
    { path: '/admin/users', label: t('admin.users.title'), icon: 'users' as const },
    { path: '/admin/groups', label: t('admin.groups.title'), icon: 'grid' as const },
    { path: '/admin/usage', label: t('admin.usage.title'), icon: 'chart' as const },
    { path: '/admin/settings', label: t('admin.settings.title'), icon: 'cog' as const },
  ]
  return auth.isSimpleMode ? admin.filter(item => item.path !== '/admin/users') : [...admin, ...personal]
})
const results = computed(() => {
  const term = query.value.trim().toLocaleLowerCase()
  return destinations.value.filter(item => !term || (item.label+' '+item.path).toLocaleLowerCase().includes(term))
})

watch(open, async (value) => {
  if (!value) return
  query.value = ''
  await nextTick()
  searchInput.value?.focus()
})

function navigate(path?: string) {
  if (!path) return
  open.value = false
  void router.push(path)
}
</script>

<style scoped>
.console-search-results { display: grid; gap: 4px; margin-top: 14px; max-height: 360px; overflow-y: auto; }
.console-search-result { display: flex; align-items: center; gap: 12px; min-height: 44px; padding: 10px 12px; border: 1px solid transparent; border-radius: 6px; color: var(--signal-text); text-align: left; font-size: 14px; }
.console-search-result > svg:first-child { color: var(--signal-muted); flex-shrink: 0; }
.console-search-result > span { flex: 1; min-width: 0; }
.console-search-result > svg:last-child { color: var(--signal-muted); opacity: 0; }
.console-search-result:hover, .console-search-result:focus-visible { border-color: var(--signal-line); background: var(--signal-hover); }
.console-search-result:hover > svg:last-child, .console-search-result:focus-visible > svg:last-child { opacity: 1; }
</style>
