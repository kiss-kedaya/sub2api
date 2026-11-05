<template>
  <div class="flex flex-col gap-4">
    <PageToolbar>
      <template #left>
        <UInput
          v-model="search"
          icon="i-lucide-search"
          class="w-64 max-w-full"
          :placeholder="text('搜索名称、站点或账号', 'Search name, site or account')"
          :aria-label="text('搜索上游', 'Search upstream channels')"
        />
      </template>
      <template #right>
        <USelect v-model="statusFilter" :items="statusItems" class="min-w-32" :aria-label="text('状态', 'Status')" />
        <UButton icon="i-lucide-plus" :label="text('新增上游', 'Add upstream')" @click="openCreate" />
      </template>
    </PageToolbar>

    <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
    <UAlert v-if="notice" color="success" variant="subtle" icon="i-lucide-check-circle-2" :title="notice" />

    <div class="card overflow-x-auto">
      <table class="w-full min-w-[960px] text-sm">
        <thead>
          <tr class="border-b border-default text-left text-xs text-muted">
            <th class="p-3 font-medium">{{ text('上游', 'Upstream') }}</th>
            <th class="p-3 font-medium">{{ text('账号', 'Account') }}</th>
            <th class="p-3 font-medium">{{ text('状态', 'Status') }}</th>
            <th class="p-3 font-medium">{{ text('会话 / 同步', 'Session / sync') }}</th>
            <th class="p-3 text-right font-medium">{{ text('操作', 'Actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="channel in rows" :key="channel.id" class="border-b border-default last:border-b-0 align-top">
            <td class="p-3">
              <p class="font-medium text-highlighted">{{ channel.name }}</p>
              <p class="truncate text-xs text-muted">{{ channel.site_url }}</p>
              <p v-if="channel.tags?.length" class="mt-1 flex flex-wrap gap-1">
                <UBadge v-for="tag in channel.tags" :key="tag" size="sm" variant="subtle" color="neutral">{{ tag }}</UBadge>
              </p>
            </td>
            <td class="p-3">
              <p>{{ channel.username || '—' }}</p>
              <p class="text-xs text-muted">
                {{ channel.has_password ? text('已存口令', 'password stored') : text('未存口令', 'no password') }}
                <span v-if="channel.turnstile_enabled"> · {{ text('登录打码', 'captcha') }} {{ channel.captcha_provider || '—' }}</span>
              </p>
            </td>
            <td class="p-3">
              <UBadge :color="channel.status === 'active' ? 'success' : 'neutral'" variant="subtle" size="sm">
                {{ channel.status === 'active' ? text('启用', 'active') : text('停用', 'disabled') }}
              </UBadge>
              <p v-if="channel.session_error" class="mt-1 text-xs text-red-500">{{ channel.session_error }}</p>
            </td>
            <td class="p-3 text-xs text-muted">
              <p>
                {{ text('会话', 'session') }}:
                <span :class="channel.has_session ? 'text-emerald-600 dark:text-emerald-400' : ''">
                  {{ channel.has_session ? text('已登录', 'signed in') : text('未登录', 'signed out') }}
                </span>
                <span v-if="channel.session_expires_at"> · {{ formatTime(channel.session_expires_at) }}</span>
              </p>
              <p>{{ text('最近登录', 'last login') }}: {{ channel.last_login_at ? formatTime(channel.last_login_at) : '—' }}</p>
              <p v-if="channel.last_login_error" class="text-red-500">{{ channel.last_login_error }}</p>
              <p>
                {{ text('公告同步', 'announcement sync') }}:
                {{ channel.sync_announcements ? text('开', 'on') : text('关', 'off') }}
                <span v-if="channel.last_sync_at"> · {{ formatTime(channel.last_sync_at) }}</span>
              </p>
              <p v-if="channel.last_sync_error" class="text-red-500">{{ channel.last_sync_error }}</p>
            </td>
            <td class="p-3">
              <div class="flex flex-wrap justify-end gap-1">
                <UButton size="xs" color="neutral" variant="ghost" :loading="busyId === channel.id && busyAction === 'login'" :label="text('登录', 'Sign in')" @click="signIn(channel)" />
                <UButton size="xs" color="neutral" variant="ghost" :loading="busyId === channel.id && busyAction === 'balance'" :label="text('余额', 'Balance')" @click="readBalance(channel)" />
                <UButton size="xs" color="neutral" variant="ghost" :label="text('编辑', 'Edit')" @click="openEdit(channel)" />
                <UButton size="xs" color="error" variant="ghost" :label="text('删除', 'Delete')" @click="askDelete(channel)" />
              </div>
            </td>
          </tr>
          <tr v-if="!rows.length && !loading">
            <td colspan="5" class="p-8 text-center text-muted">
              {{ text('还没有配置上游。先点右上角「新增上游」保存站点和账号口令。', 'No upstream configured yet. Use "Add upstream" to store a site and its credentials.') }}
            </td>
          </tr>
        </tbody>
      </table>
      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default p-3 text-sm text-muted">
        <span>{{ text(`${total} 个上游`, `${total} upstreams`) }}</span>
        <div class="flex items-center gap-2">
          <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" :disabled="loading || page <= 1" :aria-label="text('上一页', 'Previous page')" @click="goPage(page - 1)" />
          <span class="tabular-nums">{{ page }} / {{ pages || 1 }}</span>
          <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" :disabled="loading || page >= pages" :aria-label="text('下一页', 'Next page')" @click="goPage(page + 1)" />
        </div>
      </div>
    </div>

    <BaseDialog :show="dialogOpen" :title="editing ? text('编辑上游', 'Edit upstream') : text('新增上游', 'Add upstream')" @close="closeDialog">
      <div class="space-y-4">
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-xs text-muted">
            {{ text('名称', 'Name') }}
            <UInput v-model="form.name" class="mt-1 w-full" :placeholder="text('例如 sub.kedaya.xyz', 'e.g. sub.kedaya.xyz')" />
          </label>
          <label class="text-xs text-muted">
            {{ text('站点地址', 'Site URL') }}
            <UInput v-model="form.site_url" class="mt-1 w-full" placeholder="https://" />
          </label>
          <label class="text-xs text-muted">
            {{ text('登录账号', 'Username') }}
            <UInput v-model="form.username" class="mt-1 w-full" autocomplete="off" />
          </label>
          <label class="text-xs text-muted">
            {{ text('登录口令', 'Password') }}
            <UInput v-model="form.password" type="password" class="mt-1 w-full" autocomplete="new-password" :placeholder="editing ? text('留空表示不修改', 'leave blank to keep') : ''" />
          </label>
          <label class="text-xs text-muted">
            {{ text('状态', 'Status') }}
            <USelect v-model="form.status" :items="formStatusItems" class="mt-1 w-full" />
          </label>
          <label class="text-xs text-muted">
            {{ text('代理 URL（可选）', 'Proxy URL (optional)') }}
            <UInput v-model="form.proxy_url" class="mt-1 w-full" placeholder="http://127.0.0.1:7890" />
          </label>
        </div>

        <div class="rounded-lg border border-default p-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <label class="inline-flex items-center gap-2 text-sm">
              <USwitch v-model="form.turnstile_enabled" />
              <span>{{ text('登录需要 Turnstile 打码', 'Login requires Turnstile captcha') }}</span>
            </label>
            <label class="inline-flex items-center gap-2 text-sm">
              <USwitch v-model="form.sync_announcements" />
              <span>{{ text('自动同步公告', 'Auto sync announcements') }}</span>
            </label>
          </div>
          <div v-if="form.turnstile_enabled" class="mt-3 grid gap-3 sm:grid-cols-2">
            <label class="text-xs text-muted">
              {{ text('打码服务', 'Captcha provider') }}
              <USelect v-model="form.captcha_provider" :items="captchaItems" class="mt-1 w-full" />
            </label>
            <label class="text-xs text-muted">
              {{ text('打码 API Key', 'Captcha API key') }}
              <UInput v-model="form.captcha_api_key" type="password" class="mt-1 w-full" autocomplete="new-password" :placeholder="editing && channelHasCaptchaKey ? text('留空表示不修改', 'leave blank to keep') : ''" />
            </label>
            <label class="text-xs text-muted sm:col-span-2">
              {{ text('打码接口地址（可选，自建服务用）', 'Captcha endpoint (optional, for self-hosted)') }}
              <UInput v-model="form.captcha_endpoint" class="mt-1 w-full" placeholder="https://api.capsolver.com" />
            </label>
          </div>
          <p class="mt-2 text-xs text-muted">
            {{ text('打码 key 与登录口令都会加密保存，列表只回显是否已保存，不回显内容。', 'Captcha keys and passwords are stored encrypted; the list only reports whether one exists.') }}
          </p>
        </div>

        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-xs text-muted">
            {{ text('标签（逗号分隔）', 'Tags (comma separated)') }}
            <UInput v-model="form.tags" class="mt-1 w-full" :placeholder="text('主力, 备用', 'primary, backup')" />
          </label>
          <label class="text-xs text-muted">
            {{ text('备注', 'Notes') }}
            <UInput v-model="form.notes" class="mt-1 w-full" />
          </label>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" :label="text('取消', 'Cancel')" @click="closeDialog" />
          <UButton :loading="saving" :label="editing ? text('保存', 'Save') : text('创建', 'Create')" @click="save" />
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="deleteTarget !== null" :title="text('删除上游', 'Delete upstream')" @close="deleteTarget = null">
      <p class="text-sm">
        {{ text(`确认删除「${deleteTarget?.name ?? ''}」？已同步的公告会一起删除，上游站点本身不受影响。`, `Delete "${deleteTarget?.name ?? ''}"? Synced announcements are removed too; the upstream site is untouched.`) }}
      </p>
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
  channelBalance,
  createChannel,
  deleteChannel,
  listChannels,
  loginChannel,
  updateChannel,
  type UpstreamChannel,
} from '@/api/admin/upstreamChannel'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const rows = ref<UpstreamChannel[]>([])
const total = ref(0)
const page = ref(1)
const pages = ref(1)
const pageSize = 20
const search = ref('')
const statusFilter = ref('all')
const loading = ref(false)
const error = ref('')
const notice = ref('')

const dialogOpen = ref(false)
const editing = ref<UpstreamChannel | null>(null)
const channelHasCaptchaKey = ref(false)
const saving = ref(false)
const deleteTarget = ref<UpstreamChannel | null>(null)
const deleting = ref(false)
const busyId = ref<number | null>(null)
const busyAction = ref<'login' | 'balance' | ''>('')

const form = reactive({
  name: '',
  site_url: '',
  username: '',
  password: '',
  status: 'active',
  proxy_url: '',
  turnstile_enabled: false,
  sync_announcements: true,
  captcha_provider: '',
  captcha_api_key: '',
  captcha_endpoint: '',
  tags: '',
  notes: '',
})

const statusItems = [
  { label: text('全部状态', 'All status'), value: 'all' },
  { label: text('启用', 'Active'), value: 'active' },
  { label: text('停用', 'Disabled'), value: 'disabled' },
]
const formStatusItems = statusItems.slice(1)
const captchaItems = [
  { label: text('不填（跳过打码）', 'None'), value: '' },
  { label: 'CapSolver', value: 'capsolver' },
  { label: '2Captcha', value: '2captcha' },
  { label: 'AntiCaptcha', value: 'anticaptcha' },
  { label: 'YesCaptcha', value: 'yescaptcha' },
]

function formatTime(value?: string | null): string {
  return value ? new Date(value).toLocaleString() : '—'
}
function reason(err: unknown, fallback: string): string {
  const candidate = err as { message?: string }
  return candidate?.message || fallback
}

async function load(pageValue = page.value) {
  loading.value = true
  error.value = ''
  try {
    const result = await listChannels({
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
    error.value = reason(err, text('读取上游列表失败', 'Failed to load upstreams'))
  } finally {
    loading.value = false
  }
}

function goPage(next: number) {
  if (next < 1 || (pages.value && next > pages.value)) return
  void load(next)
}

function resetForm() {
  Object.assign(form, {
    name: '', site_url: '', username: '', password: '', status: 'active', proxy_url: '',
    turnstile_enabled: false, sync_announcements: true, captcha_provider: '',
    captcha_api_key: '', captcha_endpoint: '', tags: '', notes: '',
  })
}

function openCreate() {
  editing.value = null
  channelHasCaptchaKey.value = false
  resetForm()
  error.value = ''
  dialogOpen.value = true
}

function openEdit(channel: UpstreamChannel) {
  editing.value = channel
  channelHasCaptchaKey.value = channel.has_captcha_key
  Object.assign(form, {
    name: channel.name,
    site_url: channel.site_url,
    username: channel.username,
    password: '',
    status: channel.status === 'disabled' ? 'disabled' : 'active',
    proxy_url: channel.proxy_url ?? '',
    turnstile_enabled: channel.turnstile_enabled,
    sync_announcements: channel.sync_announcements,
    captcha_provider: channel.captcha_provider ?? '',
    captcha_api_key: '',
    captcha_endpoint: channel.captcha_endpoint ?? '',
    tags: (channel.tags ?? []).join(', '),
    notes: channel.notes ?? '',
  })
  error.value = ''
  dialogOpen.value = true
}

function closeDialog() {
  dialogOpen.value = false
  saving.value = false
}

async function save() {
  if (!form.name.trim() || !form.site_url.trim()) {
    error.value = text('名称和站点地址必填', 'Name and site URL are required')
    return
  }
  saving.value = true
  error.value = ''
  const payload = {
    name: form.name.trim(),
    site_url: form.site_url.trim(),
    username: form.username.trim(),
    password: form.password || undefined,
    turnstile_enabled: form.turnstile_enabled,
    captcha_provider: form.turnstile_enabled ? form.captcha_provider : '',
    captcha_api_key: form.captcha_api_key || undefined,
    captcha_endpoint: form.captcha_endpoint.trim(),
    proxy_url: form.proxy_url.trim(),
    sync_announcements: form.sync_announcements,
    status: form.status,
    tags: form.tags.split(',').map(tag => tag.trim()).filter(Boolean),
    notes: form.notes.trim(),
  }
  try {
    if (editing.value) {
      await updateChannel(editing.value.id, payload)
      notice.value = text('上游已更新', 'Upstream updated')
    } else {
      await createChannel(payload)
      notice.value = text('上游已创建', 'Upstream created')
    }
    dialogOpen.value = false
    await load()
  } catch (err) {
    error.value = reason(err, text('保存失败', 'Save failed'))
  } finally {
    saving.value = false
  }
}

function askDelete(channel: UpstreamChannel) {
  deleteTarget.value = channel
}

async function confirmDelete() {
  if (!deleteTarget.value) return
  deleting.value = true
  error.value = ''
  try {
    await deleteChannel(deleteTarget.value.id)
    notice.value = text('上游已删除', 'Upstream deleted')
    deleteTarget.value = null
    await load()
  } catch (err) {
    error.value = reason(err, text('删除失败', 'Delete failed'))
  } finally {
    deleting.value = false
  }
}

async function signIn(channel: UpstreamChannel) {
  busyId.value = channel.id
  busyAction.value = 'login'
  error.value = ''
  try {
    await loginChannel(channel.id)
    notice.value = text(`${channel.name} 登录成功`, `${channel.name} signed in`)
    await load()
  } catch (err) {
    error.value = reason(err, text('登录失败：检查账号口令或打码配置', 'Sign-in failed: check credentials or captcha settings'))
  } finally {
    busyId.value = null
    busyAction.value = ''
  }
}

async function readBalance(channel: UpstreamChannel) {
  busyId.value = channel.id
  busyAction.value = 'balance'
  error.value = ''
  try {
    const result = await channelBalance(channel.id)
    notice.value = text(`${channel.name} 余额 ${result.balance}`, `${channel.name} balance ${result.balance}`)
  } catch (err) {
    error.value = reason(err, text('读取余额失败，先确认已登录', 'Balance read failed; sign in first'))
  } finally {
    busyId.value = null
    busyAction.value = ''
  }
}

let searchTimer: ReturnType<typeof setTimeout> | undefined
watch([statusFilter], () => {
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

onMounted(() => {
  void load()
})
</script>
