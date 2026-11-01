<template>
  <AppLayout>
    <div class="space-y-6">
      <AdminPageHeader :title="headerTitle" />

      <!-- Sub-nav (template settings page): each item is a child route -->
      <PageToolbar>
        <template #left>
          <UButton color="neutral" variant="ghost" icon="i-lucide-arrow-left" :label="t('admin.users.detail.back')" to="/admin/users" />
          <UNavigationMenu :items="tabs" highlight class="-mx-1" />
        </template>
      </PageToolbar>

      <p v-if="notFound" class="py-16 text-center text-sm text-muted">{{ t('admin.users.detail.notFound') }}</p>
      <div v-else-if="!user" class="flex justify-center py-16">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>
      <div v-else class="mx-auto flex w-full max-w-3xl flex-col gap-6">
        <div class="flex items-center gap-3">
          <UAvatar :alt="user.email" :text="user.email.charAt(0).toUpperCase()" size="lg" />
          <div class="min-w-0">
            <p class="truncate font-semibold text-highlighted">{{ user.email }}</p>
            <p class="text-sm text-muted">#{{ user.id }}<template v-if="user.username"> · {{ user.username }}</template></p>
          </div>
          <div class="ms-auto flex items-center gap-1.5">
            <UBadge :color="user.role === 'admin' ? 'primary' : 'neutral'" variant="subtle" :label="t('admin.users.roles.' + user.role)" />
            <UBadge :color="user.status === 'active' ? 'success' : 'error'" variant="subtle" :label="user.status === 'active' ? t('common.active') : t('admin.users.disabled')" />
          </div>
        </div>
        <RouterView />
      </div>
    </div>

    <!-- The list page's dialogs, reused as they are -->
    <UserEditModal :show="showEdit" :user="user" @close="showEdit = false" @success="reload" />
    <UserBalanceModal :show="showBalance" :user="user" :operation="balanceOperation" @close="showBalance = false" @success="reload" />
    <UserBalanceHistoryModal
      :show="showHistory"
      :user="user"
      @close="showHistory = false"
      @deposit="showHistory = false; openBalance('add')"
      @withdraw="showHistory = false; openBalance('subtract')"
    />
    <UserApiKeysModal :show="showKeys" :user="user" @close="showKeys = false; version++" />
    <UserAllowedGroupsModal :show="showGroups" :user="user" @close="showGroups = false" @success="reload" />
    <UserPlatformQuotaModal :show="showQuota" :user="user" @close="showQuota = false" @success="reload" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, provide, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminUser } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import UserEditModal from '@/components/admin/user/UserEditModal.vue'
import UserBalanceModal from '@/components/admin/user/UserBalanceModal.vue'
import UserBalanceHistoryModal from '@/components/admin/user/UserBalanceHistoryModal.vue'
import UserApiKeysModal from '@/components/admin/user/UserApiKeysModal.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import UserPlatformQuotaModal from '@/components/admin/user/UserPlatformQuotaModal.vue'
import { userDetailKey } from './userDetailContext'

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()

const user = ref<AdminUser | null>(null)
const notFound = ref(false)
const version = ref(0)
const userId = computed(() => Number(route.params.id))

const headerTitle = computed(() => user.value?.email || t('admin.users.detail.title'))

const tabs = computed(() => {
  const base = `/admin/users/${route.params.id}`
  return [
    { label: t('admin.users.detail.tabs.profile'), icon: 'i-lucide-user', to: `${base}/profile` },
    { label: t('admin.users.detail.tabs.balance'), icon: 'i-lucide-wallet', to: `${base}/balance` },
    { label: t('admin.users.detail.tabs.keys'), icon: 'i-lucide-key-round', to: `${base}/keys` },
    { label: t('admin.users.detail.tabs.usage'), icon: 'i-lucide-chart-column', to: `${base}/usage` },
    { label: t('admin.users.detail.tabs.subscriptions'), icon: 'i-lucide-badge-check', to: `${base}/subscriptions` }
  ]
})

async function reload() {
  const id = userId.value
  if (!Number.isFinite(id) || id <= 0) {
    notFound.value = true
    return
  }
  try {
    const loaded = await adminAPI.users.getById(id)
    if (id !== userId.value) return
    user.value = loaded
    notFound.value = false
    version.value++
  } catch (error: any) {
    if (error?.status === 404 || error?.response?.status === 404) {
      notFound.value = true
    } else {
      appStore.showError(error?.message || t('admin.users.failedToLoad'))
    }
  }
}

watch(userId, () => {
  user.value = null
  notFound.value = false
  void reload()
}, { immediate: true })

const showEdit = ref(false)
const showBalance = ref(false)
const balanceOperation = ref<'add' | 'subtract'>('add')
const showHistory = ref(false)
const showKeys = ref(false)
const showGroups = ref(false)
const showQuota = ref(false)

function openBalance(operation: 'add' | 'subtract') {
  balanceOperation.value = operation
  showBalance.value = true
}

provide(userDetailKey, {
  user,
  version,
  reload,
  openEdit: () => { showEdit.value = true },
  openBalance,
  openBalanceHistory: () => { showHistory.value = true },
  openApiKeys: () => { showKeys.value = true },
  openAllowedGroups: () => { showGroups.value = true },
  openPlatformQuota: () => { showQuota.value = true }
})
</script>
