<template>
  <AppLayout>
    <div class="flex flex-col gap-4">
      <AdminPageHeader :title="text('分层安全限制', 'Layered API restrictions')" :description="text('复用现有内容审核和提示词审计，不增加扫描器或收费；只限制 API，不关闭用户的网站登录。', 'Reuses existing moderation and prompt audits. Adds no scanner or charge. Only API access is restricted; website sign-in remains available.')" />
      <PageToolbar>
        <template #left><UTabs v-model="tab" :items="tabs" :content="false" variant="link" class="-mx-1" :ui="{ list: 'border-b-0' }" /></template>
        <template v-if="tab === 'records'" #right>
          <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="page = 1; loadEvents()">
            <UInput v-model.number="userID" type="number" min="1" icon="i-lucide-search" class="w-48" :placeholder="text('用户 ID，可留空', 'User ID, optional')" :aria-label="text('用户 ID', 'User ID')" />
            <UButton type="submit" color="neutral" variant="outline" :label="text('查询', 'Search')" :disabled="busy" />
            <UButton type="button" icon="i-lucide-unlock" color="error" variant="soft" :label="text('解除此用户的 API 限制', 'Release this user’s API restrictions')" :disabled="busy || !userID" @click="releaseOpen = true" />
          </form>
        </template>
      </PageToolbar>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      <UAlert v-if="success" role="status" color="success" variant="subtle" icon="i-lucide-circle-check" :title="success" />

      <div v-show="tab === 'rules'" class="mx-auto w-full lg:max-w-2xl">
        <form v-if="config" id="penalty-config" @submit.prevent="save">
          <UPageCard :title="text('限制规则', 'Restriction policy')" :description="text('会话级策略按用户身份跨 Key 累计；没有明确会话标识时仅记录，不扩大封禁范围。用户级策略始终按用户全部 Key 累计，不依赖会话标识。观察模式切换到执行后，现有累计仍可能生效。', 'Session policies count across keys for the same user; missing conversation identity records only without widening scope. User policies count all keys even without a conversation identity. Existing strikes may become active when leaving observe mode.')" variant="naked" orientation="horizontal" class="mb-4" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
            <UButton type="submit" form="penalty-config" :label="text('保存', 'Save')" :disabled="busy" :loading="busy" class="w-fit lg:ms-auto" />
          </UPageCard>
          <UAlert v-if="!config.risk_control_enabled" color="warning" variant="subtle" icon="i-lucide-triangle-alert" class="mb-4" :title="text('风控总开关未开启，本规则不会执行。', 'The main risk-control switch is off; this policy is inactive.')" />
          <UPageCard variant="subtle">
            <UFormField :label="text('启用分层限制', 'Enable layered restrictions')" class="flex items-center justify-between gap-4"><USwitch v-model="config.enabled" data-testid="penalty-enabled" /></UFormField>
            <USeparator />
            <UFormField :label="text('只观察，不执行限制', 'Observe without enforcement')" class="flex items-center justify-between gap-4"><USwitch v-model="config.observe_only" data-testid="penalty-observe" /></UFormField>
            <USeparator />
            <UFormField :label="text('处罚方式', 'Policy')" class="flex max-sm:flex-col justify-between items-start gap-4">
              <USelect v-model="config.policy" :items="policyItems" class="w-full sm:w-72" />
            </UFormField>
            <template v-if="config.policy === 'session_route_then_block'">
              <USeparator />
              <UFormField :label="text('受限分组', 'Restricted group')" :description="text('首次命中后只接受用户原本有权使用的受限分组，不自动授予分组、改倍率或迁移订阅；达到阈值后封禁该会话。', 'After the first strike, only the configured group is admitted, subject to existing permissions. No group access, rate or subscription is granted or changed. The conversation is blocked at the threshold.')" required class="flex max-sm:flex-col justify-between items-start gap-4">
                <USelect :model-value="config.target_group_id ?? undefined" :items="groupItems" :placeholder="text('请选择分组', 'Select a group')" class="w-full sm:w-72" @update:model-value="config.target_group_id = $event ?? null" />
              </UFormField>
            </template>
            <USeparator />
            <UFormField :label="text('违规次数阈值', 'Strike threshold')" required class="flex max-sm:flex-col justify-between items-start gap-4"><UInput v-model.number="config.threshold" type="number" min="1" max="1000" required class="w-full sm:w-40" /></UFormField>
            <USeparator />
            <UFormField :label="text('累计窗口（小时）', 'Counting window (hours)')" required class="flex max-sm:flex-col justify-between items-start gap-4"><UInput v-model.number="config.window_hours" type="number" min="1" max="87600" required class="w-full sm:w-40" /></UFormField>
            <USeparator />
            <UFormField :label="text('限制有效期（小时）', 'Restriction duration (hours)')" required class="flex max-sm:flex-col justify-between items-start gap-4"><UInput v-model.number="config.duration_hours" type="number" min="1" max="87600" required class="w-full sm:w-40" /></UFormField>
            <USeparator />
            <UFormField :label="text('统计全部分组的违规', 'Count findings from all groups')" class="flex items-center justify-between gap-4"><USwitch v-model="config.all_groups" data-testid="penalty-all-groups" /></UFormField>
            <template v-if="!config.all_groups">
              <USeparator />
              <UFormField :label="text('统计以下分组的违规', 'Count findings from selected groups')" :ui="{ container: 'w-full' }"><GroupSelector v-model="config.group_ids" :groups="groups" :searchable="true" /></UFormField>
            </template>
            <USeparator />
            <UFormField :label="text('豁免用户 ID（逗号分隔）', 'Exempt user IDs (comma separated)')" class="flex max-sm:flex-col justify-between items-start gap-4"><UInput v-model="exemptIDs" class="w-full sm:w-72" /></UFormField>
          </UPageCard>
        </form>
      </div>

      <div v-show="tab === 'records'" class="flex flex-col gap-4">
        <UTable :data="events" :columns="columns" :ui="tableUi">
          <template #created_at-cell="{ row }">{{ new Date(row.original.created_at).toLocaleString() }}</template>
          <template #user_id-cell="{ row }">#{{ row.original.user_id }}</template>
          <template #reason-cell="{ row }"><span class="whitespace-normal text-default">{{ row.original.reason }}</span></template>
          <template #request_id-cell="{ row }"><code class="break-all text-xs">{{ row.original.request_id }}</code></template>
          <template #empty>{{ text('暂无记录', 'No records') }}</template>
        </UTable>
        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4">
          <span class="text-sm text-muted">{{ text(`共 ${total} 条记录`, `${total} records`) }}</span>
          <UPagination :page="page" :total="total" :items-per-page="20" :disabled="busy" @update:page="(value: number) => { page = value; loadEvents() }" />
        </div>
      </div>
    </div>
    <BaseDialog :show="releaseOpen" width="narrow" :title="text('解除此用户的 API 限制', 'Release this user’s API restrictions')" @close="releaseOpen = false">
      <form id="penalty-release" class="space-y-4" @submit.prevent="release">
        <p class="text-sm text-muted">#{{ userID }} · {{ text('确认解除该用户的全部 API 限制？历史记录仍会保留。', 'Release all API restrictions for this user? Historical events are retained.') }}</p>
        <UFormField :label="text('解除原因（必填）', 'Release reason (required)')" required><UTextarea v-model.trim="releaseReason" :rows="3" maxlength="1000" required class="w-full" data-testid="penalty-release-reason" /></UFormField>
      </form>
      <template #footer>
        <UButton color="neutral" variant="subtle" :label="text('取消', 'Cancel')" :disabled="busy" @click="releaseOpen = false" />
        <UButton type="submit" form="penalty-release" color="error" :label="text('解除限制', 'Release')" :disabled="busy || !userID || !releaseReason" :loading="busy" />
      </template>
    </BaseDialog>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import { getAll as getGroups } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { getPenaltyConfig, savePenaltyConfig, adminPenaltyEvents, releasePenalty, type PenaltyConfig, type PenaltyEvent } from '@/api/securityPenalties'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const groups = ref<AdminGroup[]>([])
const config = ref<PenaltyConfig>(), exemptIDs = ref(''), userID = ref<number>(), releaseReason = ref(''), events = ref<PenaltyEvent[]>([]), page = ref(1), total = ref(0), busy = ref(false), error = ref(''), success = ref('')
const tab = ref<'rules' | 'records'>('rules'), releaseOpen = ref(false)
const tabs = computed(() => [
  { label: text('限制规则', 'Restriction policy'), value: 'rules', icon: 'i-lucide-shield-check' },
  { label: text('安全记录与人工解除', 'Security records and manual release'), value: 'records', icon: 'i-lucide-list-filter' },
])
const policyItems = computed(() => [
  { label: text('达到阈值后禁止该会话', 'Block conversation at threshold'), value: 'session_block' as const },
  { label: text('全部 Key 累计，达到阈值后禁止用户 API', 'Count across all keys and block user API'), value: 'user_block' as const },
  { label: text('先限制分组，再禁止该会话', 'Restrict group, then block conversation'), value: 'session_route_then_block' as const },
])
const groupItems = computed(() => groups.value.map(group => ({ label: `${group.name} · ${group.platform}`, value: group.id })))
const columns = computed<TableColumn<PenaltyEvent>[]>(() => [
  { accessorKey: 'created_at', header: text('时间', 'Time') },
  { accessorKey: 'user_id', header: text('用户', 'User') },
  { accessorKey: 'reason', header: text('原因', 'Reason') },
  { accessorKey: 'count', header: text('累计', 'Strikes') },
  { accessorKey: 'request_id', header: 'Request ID' },
])
const tableUi = { base: 'border-separate border-spacing-0', thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none', tbody: '[&>tr]:last:[&>td]:border-b-0', th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r', td: 'border-b border-default', separator: 'h-0' }
let disposed = false
function parseIDs(value: string) { const ids = value.trim() ? value.split(/[\s,，]+/).map(Number) : []; if (ids.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new Error(text('ID 必须是正整数', 'IDs must be positive integers')); return [...new Set(ids)] }
async function loadEvents() { try { const data = await adminPenaltyEvents(page.value, userID.value || undefined); if (!disposed) { events.value = data.items; total.value = data.total } } catch (cause) { if (!disposed) error.value = (cause as Error).message } }
async function save() {
  if (!config.value || busy.value) return
  // USelect has no native `required`: keep the restricted-group requirement the native select used to enforce.
  if (config.value.policy === 'session_route_then_block' && config.value.target_group_id == null) { success.value = ''; error.value = text('请选择受限分组', 'Select a restricted group'); return }
  busy.value = true; error.value = ''; success.value = ''
  try { const input = { ...config.value, group_ids: [...config.value.group_ids], exempt_user_ids: parseIDs(exemptIDs.value) }; config.value = await savePenaltyConfig(input); success.value = text('已保存，新请求按新规则检查。', 'Saved. New requests use the updated policy.') } catch (cause) { error.value = (cause as Error).message } finally { busy.value = false }
}
// The release dialog is the confirmation step (it states the same warning the old window.confirm showed).
async function release() { if (!userID.value || !releaseReason.value || busy.value) return; busy.value = true; error.value = ''; try { await releasePenalty(userID.value, releaseReason.value); releaseReason.value = ''; releaseOpen.value = false; success.value = text('已解除，后续违规重新累计。', 'Released. Future findings start a new count.'); await loadEvents() } catch (cause) { error.value = (cause as Error).message } finally { busy.value = false } }
onMounted(async () => { try { const [data, options] = await Promise.all([getPenaltyConfig(), getGroups()]); if (!disposed) { config.value = data; groups.value = options; exemptIDs.value = data.exempt_user_ids.join(', ') } } catch (cause) { if (!disposed) error.value = (cause as Error).message } await loadEvents() })
onUnmounted(() => { disposed = true })
</script>
