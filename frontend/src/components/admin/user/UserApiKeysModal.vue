<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3">
        <UAvatar :alt="user.email" :text="(user.email).charAt(0).toUpperCase()" size="md" />
        <div class="min-w-0">
          <p class="truncate font-medium text-highlighted">{{ user.email }}</p>
          <p class="text-sm text-muted">{{ user.username }}</p>
        </div>
      </div>
      <div v-if="loading" class="flex justify-center py-8">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-dimmed" />
      </div>
      <p v-else-if="apiKeys.length === 0" class="py-8 text-center text-sm text-muted">{{ t('admin.users.noApiKeys') }}</p>
      <ul v-else ref="scrollContainerRef" class="max-h-96 divide-y divide-default overflow-y-auto border-y border-default" @scroll="closeGroupSelector">
        <li v-for="key in apiKeys" :key="key.id" class="py-3">
          <div class="flex items-center gap-2">
            <span class="font-medium text-highlighted">{{ key.name }}</span>
            <UBadge :color="key.status === 'active' ? 'success' : 'error'" variant="subtle" size="sm" :label="key.status" />
          </div>
          <p class="mt-0.5 truncate font-mono text-sm text-muted">{{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}</p>
          <div class="mt-2 flex flex-wrap items-center gap-4 text-xs text-muted">
            <div class="flex items-center gap-1">
              <span>{{ t('admin.users.group') }}:</span>
              <!-- Group picker: one popover per key, at most one open -->
              <UPopover
                :open="groupSelectorKeyId === key.id"
                :content="{ align: 'start' }"
                @update:open="(open: boolean) => (open ? openGroupSelector(key) : closeGroupSelector())"
              >
                <UButton color="neutral" variant="ghost" size="xs" class="-mx-1" :disabled="updatingKeyIds.has(key.id)">
                  <GroupBadge
                    v-if="key.group_id && key.group"
                    :name="key.group.name"
                    :platform="key.group.platform"
                    :subscription-type="key.group.subscription_type"
                    :rate-multiplier="key.group.rate_multiplier"
                    :peak-rate-enabled="key.group.peak_rate_enabled"
                    :peak-start="key.group.peak_start"
                    :peak-end="key.group.peak_end"
                    :peak-rate-multiplier="key.group.peak_rate_multiplier"
                  />
                  <span v-else class="italic text-dimmed">{{ t('admin.users.none') }}</span>
                  <UIcon v-if="updatingKeyIds.has(key.id)" name="i-lucide-loader-circle" class="size-3 animate-spin" />
                  <UIcon v-else name="i-lucide-chevrons-up-down" class="size-3 text-dimmed" />
                </UButton>
                <template #content>
                  <div class="max-h-64 w-64 overflow-y-auto p-1.5">
                    <UButton
                      color="neutral"
                      :variant="!key.group_id ? 'soft' : 'ghost'"
                      block
                      class="justify-start"
                      :trailing-icon="!key.group_id ? 'i-lucide-check' : undefined"
                      @click="changeGroup(key, null)"
                    >
                      <span class="italic text-muted">{{ t('admin.users.none') }}</span>
                    </UButton>
                    <UButton
                      v-for="group in allGroups"
                      :key="group.id"
                      color="neutral"
                      :variant="key.group_id === group.id ? 'soft' : 'ghost'"
                      block
                      class="justify-between"
                      @click="changeGroup(key, group.id)"
                    >
                      <GroupOptionItem
                        :name="group.name"
                        :platform="group.platform"
                        :subscription-type="group.subscription_type"
                        :rate-multiplier="group.rate_multiplier"
                        :peak-rate-enabled="group.peak_rate_enabled"
                        :peak-start="group.peak_start"
                        :peak-end="group.peak_end"
                        :peak-rate-multiplier="group.peak_rate_multiplier"
                        :description="group.description"
                        :selected="key.group_id === group.id"
                      />
                    </UButton>
                  </div>
                </template>
              </UPopover>
            </div>
            <span>{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span>
          </div>
        </li>
      </ul>
      <Pagination
        v-if="!loading && total > 0"
        :total="total"
        :page="page"
        :page-size="pageSize"
        @update:page="changePage"
        @update:page-size="changePageSize"
      />
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser, AdminGroup, ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const apiKeys = ref<ApiKey[]>([])
const allGroups = ref<AdminGroup[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(getPersistedPageSize())
const total = ref(0)
let requestVersion = 0
const updatingKeyIds = ref(new Set<number>())
const groupSelectorKeyId = ref<number | null>(null)
const scrollContainerRef = ref<HTMLElement | null>(null)


watch(() => [props.show, props.user?.id] as const, ([show], _, onCleanup) => {
  onCleanup(() => { requestVersion++ })
  closeGroupSelector()
  apiKeys.value = []
  page.value = 1
  total.value = 0
  if (show && props.user) {
    load()
    loadGroups()
  }
})

const load = async () => {
  if (!props.user) return
  const version = ++requestVersion
  closeGroupSelector()
  apiKeys.value = []
  loading.value = true
  try {
    const res = await adminAPI.users.getUserApiKeys(props.user.id, page.value, pageSize.value)
    if (version !== requestVersion) return
    apiKeys.value = res.items || []
    total.value = res.total
    page.value = res.page
    pageSize.value = res.page_size
  } catch (error) {
    if (version !== requestVersion) return
    console.error('Failed to load API keys:', error)
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

const changePage = (nextPage: number) => {
  page.value = nextPage
  load()
}

const changePageSize = (nextPageSize: number) => {
  pageSize.value = nextPageSize
  page.value = 1
  load()
}

const loadGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    allGroups.value = groups
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}


// UPopover handles placement, outside clicks and Escape; only one key's picker is open at a time.
const openGroupSelector = (key: ApiKey) => {
  groupSelectorKeyId.value = key.id
}

const closeGroupSelector = () => {
  groupSelectorKeyId.value = null
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  closeGroupSelector()
  if (key.group_id === newGroupId || (!key.group_id && newGroupId === null)) return

  updatingKeyIds.value.add(key.id)
  try {
    const result = await adminAPI.apiKeys.updateApiKeyGroup(key.id, newGroupId)
    // Update local data
    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(t('admin.users.groupChangedWithGrant', { group: result.granted_group_name }))
    } else {
      appStore.showSuccess(t('admin.users.groupChangedSuccess'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

const handleClose = () => {
  closeGroupSelector()
  emit('close')
}
</script>
