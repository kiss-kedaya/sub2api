<template>
  <div class="flex flex-col gap-4">
    <PageToolbar>
      <template #left>
        <USelect v-model="channelId" :items="channelItems" class="min-w-48" :aria-label="text('上游', 'Upstream')" />
        <UInput v-model="search" icon="i-lucide-search" class="w-56 max-w-full" :placeholder="text('搜索名称或 key', 'Search name or key')" />
      </template>
      <template #right>
        <USelect v-model="statusFilter" :items="statusItems" class="min-w-32" :aria-label="text('状态', 'Status')" />
        <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="text('刷新', 'Refresh')" :loading="loading" :disabled="!channelId" @click="load(1)" />
        <UButton icon="i-lucide-plus" :disabled="!channelId" :label="text('新建 Key', 'New key')" @click="openCreate" />
      </template>
    </PageToolbar>

    <UAlert v-if="!channelId" color="neutral" variant="subtle" icon="i-lucide-info" :title="text('先在「渠道与凭据」里添加一个上游，再回来管理它的 API Key。', 'Add an upstream in Channels first, then manage its API keys here.')" />
    <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
    <UAlert v-if="notice" color="success" variant="subtle" icon="i-lucide-check-circle-2" :title="notice" />
    <UAlert v-if="revealed" color="warning" variant="subtle" icon="i-lucide-key-round" :title="text('已显示一个 Key，关闭后不会再显示', 'A key is revealed; it is hidden again when you close this')">
      <div class="mt-1 flex flex-wrap items-center gap-2">
        <code class="break-all rounded bg-elevated px-2 py-1 text-xs">{{ revealed }}</code>
        <UButton size="xs" color="neutral" variant="outline" :label="text('收起', 'Hide')" @click="revealed = ''" />
      </div>
    </UAlert>

    <div class="card overflow-x-auto">
      <table class="w-full min-w-[900px] text-sm">
        <thead>
          <tr class="border-b border-default text-left text-xs text-muted">
            <th class="p-3 font-medium">{{ text('名称', 'Name') }}</th>
            <th class="p-3 font-medium">{{ text('分组', 'Group') }}</th>
            <th class="p-3 font-medium">{{ text('额度', 'Quota') }}</th>
            <th class="p-3 font-medium">{{ text('限速 / 用量', 'Rate limit / usage') }}</th>
            <th class="p-3 font-medium">{{ text('状态', 'Status') }}</th>
            <th class="p-3 text-right font-medium">{{ text('操作', 'Actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="key in rows" :key="key.id" class="border-b border-default last:border-b-0 align-top">
            <td class="p-3">
              <p class="font-medium text-highlighted">{{ key.name || `#${key.id}` }}</p>
              <p class="text-xs text-muted">
                {{ text('到期', 'expires') }}: {{ key.expires_at ? formatTime(key.expires_at) : text('不限', 'never') }}
                <span v-if="key.last_used_at"> · {{ text('最近使用', 'last used') }} {{ formatTime(key.last_used_at) }}</span>
              </p>
            </td>
            <td class="p-3 text-xs">
              {{ key.group_name || (key.group_id ? `#${key.group_id}` : '—') }}
              <span v-if="key.group_ratio" class="text-muted"> × {{ key.group_ratio }}</span>
            </td>
            <td class="p-3 text-xs tabular-nums">
              <p>{{ formatMoney(key.quota_used) }} / {{ key.quota > 0 ? formatMoney(key.quota) : text('不限', 'unlimited') }}</p>
            </td>
            <td class="p-3 text-xs tabular-nums text-muted">
              <p>5h {{ formatMoney(key.rate_limit_5h) }} · 1d {{ formatMoney(key.rate_limit_1d) }} · 7d {{ formatMoney(key.rate_limit_7d) }}</p>
              <p>{{ text('已用', 'used') }} {{ formatMoney(key.usage_5h) }} / {{ formatMoney(key.usage_1d) }} / {{ formatMoney(key.usage_7d) }}</p>
              <p v-if="key.ip_whitelist?.length">{{ text('白名单', 'allow') }}: {{ key.ip_whitelist.join(', ') }}</p>
              <p v-if="key.ip_blacklist?.length">{{ text('黑名单', 'deny') }}: {{ key.ip_blacklist.join(', ') }}</p>
            </td>
            <td class="p-3">
              <UBadge :color="key.status === 'active' ? 'success' : 'neutral'" size="sm" variant="subtle">{{ key.status || '—' }}</UBadge>
            </td>
            <td class="p-3">
              <div class="flex flex-wrap justify-end gap-1">
                <UButton size="xs" color="neutral" variant="ghost" :label="text('显示', 'Reveal')" @click="reveal(key)" />
                <UButton size="xs" color="neutral" variant="ghost" :label="text('编辑', 'Edit')" @click="openEdit(key)" />
                <UButton size="xs" color="error" variant="ghost" :label="text('删除', 'Delete')" @click="askDelete(key)" />
              </div>
            </td>
          </tr>
          <tr v-if="!rows.length && !loading">
            <td colspan="6" class="p-8 text-center text-muted">
              {{ channelId ? text('这个上游还没有 API Key，点右上角新建。', 'This upstream has no API keys yet.') : text('先选择上游。', 'Pick an upstream first.') }}
            </td>
          </tr>
        </tbody>
      </table>
      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default p-3 text-sm text-muted">
        <span>{{ text(`${total} 个 Key`, `${total} keys`) }}</span>
        <div class="flex items-center gap-2">
          <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" :disabled="loading || page <= 1" :aria-label="text('上一页', 'Previous page')" @click="load(page - 1)" />
          <span class="tabular-nums">{{ page }} / {{ pages || 1 }}</span>
          <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" :disabled="loading || page >= pages" :aria-label="text('下一页', 'Next page')" @click="load(page + 1)" />
        </div>
      </div>
    </div>

    <BaseDialog :show="dialogOpen" :title="editingKey ? text('编辑 API Key', 'Edit API key') : text('新建 API Key', 'New API key')" @close="dialogOpen = false">
      <div class="space-y-4">
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-xs text-muted">
            {{ text('名称', 'Name') }}
            <UInput v-model="form.name" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            {{ text('分组', 'Group') }}
            <USelect v-model="form.group_id" :items="groupItems" class="mt-1 w-full" />
          </label>
          <label v-if="!editingKey" class="text-xs text-muted">
            {{ text('自定义 Key（留空自动生成）', 'Custom key (blank = generate)') }}
            <UInput v-model="form.custom_key" class="mt-1 w-full" autocomplete="off" />
          </label>
          <label v-if="editingKey" class="text-xs text-muted">
            {{ text('状态', 'Status') }}
            <USelect v-model="form.status" :items="keyStatusItems" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            {{ text('总额度（0 表示不限）', 'Total quota (0 = unlimited)') }}
            <UInput v-model.number="form.quota" type="number" min="0" step="0.01" class="mt-1 w-full" />
          </label>
          <label v-if="!editingKey" class="text-xs text-muted">
            {{ text('有效天数（留空不限）', 'Valid days (blank = never)') }}
            <UInput v-model.number="form.expires_in_days" type="number" min="0" step="1" class="mt-1 w-full" />
          </label>
          <label v-else class="text-xs text-muted">
            {{ text('到期时间', 'Expires at') }}
            <UInput v-model="form.expires_at" type="datetime-local" class="mt-1 w-full" />
          </label>
        </div>
        <div class="grid gap-3 sm:grid-cols-3">
          <label class="text-xs text-muted">
            5h {{ text('限额', 'limit') }}
            <UInput v-model.number="form.rate_limit_5h" type="number" min="0" step="0.01" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            1d {{ text('限额', 'limit') }}
            <UInput v-model.number="form.rate_limit_1d" type="number" min="0" step="0.01" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            7d {{ text('限额', 'limit') }}
            <UInput v-model.number="form.rate_limit_7d" type="number" min="0" step="0.01" class="mt-1 w-full" />
          </label>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-xs text-muted">
            IP {{ text('白名单（逗号分隔）', 'allow list (comma separated)') }}
            <UInput v-model="form.ip_whitelist" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            IP {{ text('黑名单（逗号分隔）', 'deny list (comma separated)') }}
            <UInput v-model="form.ip_blacklist" class="mt-1 w-full" />
          </label>
        </div>
        <div v-if="editingKey" class="flex flex-wrap gap-4 text-sm">
          <label class="inline-flex items-center gap-2">
            <USwitch v-model="form.reset_quota" />
            <span>{{ text('重置已用额度', 'Reset used quota') }}</span>
          </label>
          <label class="inline-flex items-center gap-2">
            <USwitch v-model="form.reset_rate_limit_usage" />
            <span>{{ text('重置限速用量', 'Reset rate-limit usage') }}</span>
          </label>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" :label="text('取消', 'Cancel')" @click="dialogOpen = false" />
          <UButton :loading="saving" :label="editingKey ? text('保存', 'Save') : text('创建', 'Create')" @click="save" />
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="deleteTarget !== null" :title="text('删除 API Key', 'Delete API key')" @close="deleteTarget = null">
      <p class="text-sm">{{ text(`确认在上游删除「${deleteTarget?.name ?? ''}」？删除后使用这个 Key 的调用会立即失效。`, `Delete "${deleteTarget?.name ?? ''}" upstream? Calls using this key stop working immediately.`) }}</p>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" :label="text('取消', 'Cancel')" @click="deleteTarget = null" />
          <UButton color="error" :loading="deleting" :label="text('确认删除', 'Delete')" @click="confirmDelete" />
        </div>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import {
  createUpstreamAPIKey,
  deleteUpstreamAPIKey,
  listChannels,
  listUpstreamAPIKeyGroups,
  listUpstreamAPIKeys,
  revealUpstreamAPIKey,
  updateUpstreamAPIKey,
  type UpstreamAPIKey,
  type UpstreamAPIKeyGroup,
  type UpstreamChannel,
} from '@/api/admin/upstreamChannel'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const channels = ref<UpstreamChannel[]>([])
const channelId = ref<number | null>(null)
const channelItems = ref<{ label: string; value: number | null }[]>([])
const rows = ref<UpstreamAPIKey[]>([])
const groups = ref<UpstreamAPIKeyGroup[]>([])
const total = ref(0)
const page = ref(1)
const pages = ref(1)
const pageSize = 20
const search = ref('')
const statusFilter = ref('all')
const loading = ref(false)
const error = ref('')
const notice = ref('')
const revealed = ref('')

const dialogOpen = ref(false)
const editingKey = ref<UpstreamAPIKey | null>(null)
const saving = ref(false)
const deleteTarget = ref<UpstreamAPIKey | null>(null)
const deleting = ref(false)

const form = reactive({
  name: '',
  group_id: null as number | null,
  custom_key: '',
  status: 'active',
  quota: 0,
  expires_in_days: null as number | null,
  expires_at: '',
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  ip_whitelist: '',
  ip_blacklist: '',
  reset_quota: false,
  reset_rate_limit_usage: false,
})

const statusItems = [
  { label: text('全部状态', 'All status'), value: 'all' },
  { label: text('启用', 'Active'), value: 'active' },
  { label: text('停用', 'Disabled'), value: 'disabled' },
]
const keyStatusItems = statusItems.slice(1)
const groupItems = ref<{ label: string; value: number | null }[]>([{ label: text('不绑定分组', 'No group'), value: null }])

function formatTime(value?: string | null): string {
  return value ? new Date(value).toLocaleString() : '—'
}
function formatMoney(value?: number | null): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—'
  return value.toFixed(2)
}
function reason(err: unknown, fallback: string): string {
  const candidate = err as { message?: string }
  return candidate?.message || fallback
}
function splitList(value: string): string[] {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

async function loadChannels() {
  try {
    const result = await listChannels({ page: 1, pageSize: 100 })
    channels.value = result.items ?? []
    channelItems.value = channels.value.map(channel => ({ label: channel.name, value: channel.id }))
    if (!channelId.value && channels.value.length) channelId.value = channels.value[0].id
  } catch (err) {
    error.value = reason(err, text('读取上游列表失败', 'Failed to load upstreams'))
  }
}

async function loadGroups() {
  if (!channelId.value) return
  try {
    const result = await listUpstreamAPIKeyGroups(channelId.value)
    groups.value = result.items ?? []
    groupItems.value = [
      { label: text('不绑定分组', 'No group'), value: null },
      ...groups.value.map(group => ({ label: `${group.name} × ${group.ratio}`, value: group.id })),
    ]
  } catch {
    groupItems.value = [{ label: text('读取分组失败（可先不绑定）', 'Groups unavailable (leave unbound)'), value: null }]
  }
}

async function load(pageValue = page.value) {
  if (!channelId.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await listUpstreamAPIKeys(channelId.value, {
      page: pageValue,
      pageSize,
      search: search.value,
      status: statusFilter.value === 'all' ? '' : statusFilter.value,
    })
    rows.value = result.items ?? []
    total.value = result.total ?? 0
    pages.value = result.pages ?? 1
    page.value = result.page ?? pageValue
  } catch (err) {
    error.value = reason(err, text('读取 API Key 失败，先确认该上游已登录', 'Failed to load keys; sign in to the upstream first'))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingKey.value = null
  Object.assign(form, {
    name: '', group_id: null, custom_key: '', status: 'active', quota: 0,
    expires_in_days: null, expires_at: '', rate_limit_5h: 0, rate_limit_1d: 0,
    rate_limit_7d: 0, ip_whitelist: '', ip_blacklist: '',
    reset_quota: false, reset_rate_limit_usage: false,
  })
  error.value = ''
  dialogOpen.value = true
}

function openEdit(key: UpstreamAPIKey) {
  editingKey.value = key
  Object.assign(form, {
    name: key.name,
    group_id: key.group_id,
    custom_key: '',
    status: key.status || 'active',
    quota: key.quota,
    expires_in_days: null,
    expires_at: key.expires_at ? new Date(key.expires_at).toISOString().slice(0, 16) : '',
    rate_limit_5h: key.rate_limit_5h,
    rate_limit_1d: key.rate_limit_1d,
    rate_limit_7d: key.rate_limit_7d,
    ip_whitelist: (key.ip_whitelist ?? []).join(', '),
    ip_blacklist: (key.ip_blacklist ?? []).join(', '),
    reset_quota: false,
    reset_rate_limit_usage: false,
  })
  error.value = ''
  dialogOpen.value = true
}

async function save() {
  if (!channelId.value) return
  saving.value = true
  error.value = ''
  try {
    if (editingKey.value) {
      await updateUpstreamAPIKey(channelId.value, editingKey.value.id, {
        name: form.name,
        group_id: form.group_id,
        status: form.status,
        quota: form.quota,
        expires_at: form.expires_at ? new Date(form.expires_at).toISOString() : null,
        ip_whitelist: splitList(form.ip_whitelist),
        ip_blacklist: splitList(form.ip_blacklist),
        reset_quota: form.reset_quota,
        reset_rate_limit_usage: form.reset_rate_limit_usage,
        rate_limit_5h: form.rate_limit_5h,
        rate_limit_1d: form.rate_limit_1d,
        rate_limit_7d: form.rate_limit_7d,
      })
      notice.value = text('Key 已更新', 'Key updated')
    } else {
      await createUpstreamAPIKey(channelId.value, {
        name: form.name,
        group_id: form.group_id,
        custom_key: form.custom_key.trim() || undefined,
        quota: form.quota,
        expires_in_days: form.expires_in_days ?? undefined,
        rate_limit_5h: form.rate_limit_5h,
        rate_limit_1d: form.rate_limit_1d,
        rate_limit_7d: form.rate_limit_7d,
        ip_whitelist: splitList(form.ip_whitelist),
        ip_blacklist: splitList(form.ip_blacklist),
      })
      notice.value = text('Key 已创建', 'Key created')
    }
    dialogOpen.value = false
    await load()
  } catch (err) {
    error.value = reason(err, text('保存失败', 'Save failed'))
  } finally {
    saving.value = false
  }
}

async function reveal(key: UpstreamAPIKey) {
  if (!channelId.value) return
  try {
    const result = await revealUpstreamAPIKey(channelId.value, key.id)
    revealed.value = result.key
  } catch (err) {
    error.value = reason(err, text('读取明文 Key 失败', 'Failed to reveal the key'))
  }
}

function askDelete(key: UpstreamAPIKey) {
  deleteTarget.value = key
}

async function confirmDelete() {
  if (!channelId.value || !deleteTarget.value) return
  deleting.value = true
  error.value = ''
  try {
    await deleteUpstreamAPIKey(channelId.value, deleteTarget.value.id)
    notice.value = text('Key 已删除', 'Key deleted')
    deleteTarget.value = null
    await load()
  } catch (err) {
    error.value = reason(err, text('删除失败', 'Delete failed'))
  } finally {
    deleting.value = false
  }
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(channelId, () => {
  page.value = 1
  void loadGroups()
  void load(1)
})
watch(statusFilter, () => {
  page.value = 1
  void load(1)
})
watch(search, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    page.value = 1
    void load(1)
  }, 300)
})

onMounted(async () => {
  await loadChannels()
  await loadGroups()
  await load(1)
})
</script>
