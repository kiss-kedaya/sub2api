<template>
  <AppLayout>
    <div class="flex flex-col gap-4">
      <AdminPageHeader :title="text('凭证守护', 'Credential inspection')" :description="text('按账号开启巡检，失效凭证按需恢复；风控验证交由人工处理。', 'Opt in per account to inspect credentials and recover expired sessions. Security verification always requires an operator.')">
        <template #actions>
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="text('刷新', 'Refresh')" :disabled="busy" @click="refresh" />
          <UButton icon="i-lucide-plus" :label="text('添加账号', 'Add account')" :disabled="busy" @click="openNew" />
        </template>
      </AdminPageHeader>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      <PageToolbar>
        <template #left><UInput v-model="search" icon="i-lucide-search" class="w-64 max-w-full" :placeholder="text('搜索账号名称或 ID', 'Search account name or ID')" :aria-label="text('搜索账号', 'Search accounts')" /></template>
        <template #right><USelect v-model="stateFilter" :items="stateItems" class="min-w-36" :aria-label="text('状态', 'Status')" /></template>
      </PageToolbar>
      <UTable :data="visibleRows" :columns="columns" :loading="busy" :ui="tableUi">
        <template #account-cell="{ row }">
          <p class="font-medium text-highlighted">{{ row.original.account_name }}</p>
          <p class="text-xs text-muted">#{{ row.original.account_id }}</p>
        </template>
        <template #status-cell="{ row }">
          <UBadge :color="statusColor(row.original)" variant="subtle">{{ statusLabel(row.original) }}</UBadge>
          <p v-if="codeLabel(row.original.code)" class="mt-1 max-w-xs whitespace-normal text-xs text-muted">{{ codeLabel(row.original.code) }}</p>
        </template>
        <template #checked-cell="{ row }">{{ row.original.last_checked_at ? new Date(row.original.last_checked_at).toLocaleString() : '—' }}</template>
        <template #failures-cell="{ row }">{{ row.original.failures }} / {{ row.original.config.max_failures }}</template>
        <template #actions-cell="{ row }">
          <div class="flex items-center justify-end gap-1">
            <UButton icon="i-lucide-scan-search" color="neutral" variant="ghost" :aria-label="text('立即检测', 'Inspect now')" :title="text('立即检测', 'Inspect now')" :disabled="busy" @click="probe(row.original)" />
            <UButton icon="i-lucide-settings-2" color="neutral" variant="ghost" :aria-label="text('设置', 'Settings')" :title="text('设置', 'Settings')" :disabled="busy" @click="edit(row.original)" />
            <UDropdownMenu v-if="canRelogin(row.original)" :items="rowMenu(row.original)" :content="{ align: 'end' }">
              <UButton icon="i-lucide-ellipsis-vertical" color="neutral" variant="ghost" :aria-label="text('更多操作', 'More actions')" />
            </UDropdownMenu>
          </div>
        </template>
        <template #empty>
          <div v-if="!busy" class="space-y-1">
            <p class="font-medium text-highlighted">{{ rows.length ? text('没有匹配的账号', 'No matching accounts') : text('还没有开启凭证巡检', 'No accounts are being inspected') }}</p>
            <p v-if="!rows.length">{{ text('添加账号后默认保持关闭，保存并开启才会运行。', 'New inspection settings are disabled by default. Save and enable them when ready.') }}</p>
          </div>
        </template>
      </UTable>
      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4 text-sm text-muted">
        <span>{{ text(`${visibleRows.length} / ${rows.length} 个账号`, `${visibleRows.length} of ${rows.length} accounts`) }}</span>
        <span class="sm:text-right">{{ text('保留手工停调及已有错误状态。需要恢复这类账号时，先检查原停调原因；本功能不会直接重新开启。', 'Manual scheduling choices and existing error states are preserved. Review their original cause before restoring an account; this feature never overrides them.') }}</span>
      </div>
    </div>
    <BaseDialog :show="dialog" :title="text('凭证巡检设置', 'Credential inspection settings')" @close="closeDialog">
      <div class="space-y-4">
        <UFormField v-if="!editingID" :label="text('账号 ID', 'Account ID')"><UInput v-model.number="accountID" type="number" min="1" class="w-full" /></UFormField>
        <p v-else class="text-sm font-medium text-highlighted">{{ editingName }} · #{{ editingID }}</p>
        <USwitch v-model="draft.enabled" :label="text('开启定时巡检', 'Enable scheduled inspection')" />
        <div class="grid grid-cols-2 gap-3">
          <UFormField :label="text('检测间隔（秒）', 'Inspection interval (seconds)')"><UInput v-model.number="draft.interval_seconds" type="number" min="60" max="86400" class="w-full" /></UFormField>
          <UFormField :label="text('失败上限', 'Failure budget')"><UInput v-model.number="draft.max_failures" type="number" min="1" max="5" class="w-full" /></UFormField>
        </div>
        <UCollapsible v-model:open="advancedOpen" class="flex flex-col gap-4">
          <UButton color="neutral" variant="ghost" class="-mx-2.5 w-fit" :label="text('自动重登（可选）', 'Automatic re-login (optional)')" :trailing-icon="advancedOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" />
          <template #content>
            <div class="space-y-4">
              <USwitch v-model="draft.auto_relogin" :label="text('失效后自动重新登录', 'Automatically re-login after credential expiry')" :description="text('使用你自行信任的 HTTPS 登录服务。保存的密码和验证密钥会加密，页面不回显。403、安全验证或失败超限后停止自动恢复。', 'Use your own trusted HTTPS login service. Passwords and verification secrets are encrypted and never returned. Recovery stops after a 403, security verification or exhausted failure budget.')" />
              <UFormField :label="text('登录服务地址', 'Login service URL')"><UInput v-model="draft.relogin_endpoint" type="url" autocomplete="off" class="w-full" placeholder="https://login.example.com/api/relogin" /></UFormField>
              <UFormField :label="text('明确允许的服务来源', 'Explicitly allowed service origin')"><UInput v-model="draft.allowed_origin" type="url" autocomplete="off" class="w-full" placeholder="https://login.example.com" /></UFormField>
              <UFormField :label="text('登录邮箱', 'Login email')"><UInput v-model="draft.login_email" type="email" autocomplete="off" class="w-full" /></UFormField>
              <UFormField :label="text('密码（留空保留）', 'Password (blank preserves saved value)')"><UInput v-model="draft.password" type="password" autocomplete="new-password" class="w-full" /></UFormField>
              <UFormField :label="text('账号 TOTP 密钥（可选）', 'Account TOTP secret (optional)')"><UInput v-model="draft.totp_secret" type="password" autocomplete="new-password" class="w-full" /></UFormField>
              <UFormField :label="text('登录服务 API Token（可选）', 'Login service API token (optional)')"><UInput v-model="draft.api_token" type="password" autocomplete="new-password" class="w-full" /></UFormField>
              <UFormField :label="text('重登冷却（秒）', 'Re-login cooldown (seconds)')"><UInput v-model.number="draft.cooldown_seconds" type="number" min="300" max="604800" class="w-full" /></UFormField>
              <UCheckbox v-model="draft.clear_secrets" :label="text('清除已保存登录凭据', 'Clear saved login credentials')" />
              <UCheckbox v-model="draft.reset_failures" :label="text('问题已处理，重置失败次数和人工阻断', 'Issue resolved: reset failures and manual block')" />
            </div>
          </template>
        </UCollapsible>
        <UAlert v-if="dialogError" role="alert" color="error" variant="subtle" :title="dialogError" />
      </div>
      <template #footer>
        <UButton color="neutral" variant="subtle" :label="text('取消', 'Cancel')" :disabled="busy" @click="closeDialog" />
        <UButton :label="text('保存', 'Save')" :disabled="busy" :loading="busy" @click="save" />
      </template>
    </BaseDialog>
    <ConfirmDialog :show="!!confirmLogin" :title="text('确认重新登录', 'Confirm re-login')" :message="text('将向已配置的登录服务发送此账号的已保存登录凭据。只有当前凭证明确失效时才会执行。', 'Saved login credentials will be sent to your configured login service. This only runs if the current credential is explicitly invalid.')" @cancel="confirmLogin = undefined" @confirm="relogin" />
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp } from '@/composables/useStepUp'
import { credentialGuardAPI, type CredentialGuardRecord, type CredentialGuardInput } from '@/api/admin/credentialGuard'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const stepUp = useStepUp()
const rows = ref<CredentialGuardRecord[]>([])
const busy = ref(false)
const error = ref('')
const dialogError = ref('')
const dialog = ref(false)
const advancedOpen = ref(false)
const accountID = ref<number>()
const editingID = ref<number>()
const editingName = ref('')
const confirmLogin = ref<CredentialGuardRecord>()
const search = ref('')
type StateGroup = 'all' | 'healthy' | 'invalid' | 'transient' | 'blocked' | 'disabled'
const stateFilter = ref<StateGroup>('all')
const defaults = (): CredentialGuardInput => ({ enabled: false, auto_relogin: false, interval_seconds: 1800, cooldown_seconds: 1800, max_failures: 3, relogin_endpoint: '', allowed_origin: '', login_email: '', revision: '', password: '', totp_secret: '', api_token: '', clear_secrets: false, reset_failures: false })
const draft = ref<CredentialGuardInput>(defaults())
const message = (cause: unknown) => (cause as { message?: string })?.message || text('操作失败，请重试', 'Operation failed. Please retry.')
function stateGroup(row: CredentialGuardRecord): StateGroup | 'other' { return row.blocked ? 'blocked' : !row.config.enabled ? 'disabled' : row.state === 'healthy' || row.state === 'invalid' || row.state === 'transient' ? row.state : 'other' }
const stateItems = computed(() => [
  { label: text('全部状态', 'All statuses'), value: 'all' }, { label: text('正常', 'Healthy'), value: 'healthy' }, { label: text('凭证失效', 'Expired'), value: 'invalid' },
  { label: text('暂时不可用', 'Temporarily unavailable'), value: 'transient' }, { label: text('需要人工处理', 'Operator required'), value: 'blocked' }, { label: text('已关闭', 'Disabled'), value: 'disabled' },
])
const visibleRows = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return rows.value.filter(row => (stateFilter.value === 'all' || stateGroup(row) === stateFilter.value) && (!needle || `${row.account_name} #${row.account_id}`.toLowerCase().includes(needle)))
})
const columns = computed<TableColumn<CredentialGuardRecord>[]>(() => [
  { id: 'account', header: text('账号', 'Account') },
  { id: 'status', header: text('状态', 'Status') },
  { id: 'checked', header: text('最近检测', 'Last checked') },
  { id: 'failures', header: text('重登失败', 'Login failures'), meta: { class: { th: 'w-32', td: 'w-32' } } },
  { id: 'actions', header: () => text('操作', 'Actions'), meta: { class: { th: 'w-36 text-right', td: 'w-36' } } },
])
const tableUi = { base: 'table-fixed border-separate border-spacing-0', thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none', tbody: '[&>tr]:last:[&>td]:border-b-0', th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r', td: 'border-b border-default', separator: 'h-0' }
function statusColor(row: CredentialGuardRecord) { const group = stateGroup(row); return group === 'blocked' ? 'error' as const : group === 'healthy' ? 'success' as const : group === 'invalid' || group === 'transient' ? 'warning' as const : 'neutral' as const }
const canRelogin = (row: CredentialGuardRecord) => !!(row.secrets_configured && row.config.relogin_endpoint)
function rowMenu(row: CredentialGuardRecord): DropdownMenuItem[] { return [{ label: text('重新登录', 'Re-login'), icon: 'i-lucide-log-in', disabled: busy.value || row.blocked, onSelect: () => { confirmLogin.value = row } }] }
function statusLabel(row: CredentialGuardRecord) { return row.blocked ? text('需要人工处理', 'Operator required') : !row.config.enabled ? text('已关闭', 'Disabled') : ({ healthy: text('正常', 'Healthy'), invalid: text('凭证失效', 'Expired'), transient: text('暂时不可用', 'Temporarily unavailable'), skipped: text('已跳过', 'Skipped') }[row.state] || text('待检测', 'Pending')) }
function codeLabel(code: string) { const labels: Record<string, string> = { credential_invalid: text('上游明确拒绝当前凭证', 'The upstream rejected this credential'), policy_blocked: text('上游拒绝或安全策略阻断', 'Upstream policy denied the request'), security_verification_required: text('请人工完成安全验证', 'Complete security verification manually'), relogin_succeeded: text('凭证已恢复并验证通过', 'Credentials recovered and verified'), account_disabled_by_operator: text('保留管理员停调设置', 'Operator scheduling choice preserved'), relogin_budget_or_cooldown: text('达到失败上限或仍在冷却', 'Failure budget exhausted or cooldown active'), identity_mismatch: text('新凭证身份不匹配，未写回', 'Replacement identity mismatch; no credential was saved') }; return labels[code] || (code ? text('查看最近一次检测结果', 'Review the latest inspection result') : '') }
async function refresh() { busy.value = true; try { rows.value = (await credentialGuardAPI.list()).items; error.value = '' } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
function openNew() { editingID.value = undefined; editingName.value = ''; accountID.value = undefined; draft.value = defaults(); dialogError.value = ''; advancedOpen.value = false; dialog.value = true }
function edit(row: CredentialGuardRecord) { editingID.value = row.account_id; editingName.value = row.account_name; accountID.value = row.account_id; draft.value = { ...defaults(), ...row.config, revision: row.revision }; dialogError.value = ''; advancedOpen.value = false; dialog.value = true }
function closeDialog() { if (busy.value) return; draft.value = defaults(); dialog.value = false }
async function save() { if (!accountID.value) { dialogError.value = text('请输入账号 ID', 'Enter an account ID'); return } busy.value = true; try { await stepUp.run(() => credentialGuardAPI.save(accountID.value!, draft.value)); busy.value = false; closeDialog(); await refresh() } catch (cause) { dialogError.value = message(cause) } finally { busy.value = false } }
async function probe(row: CredentialGuardRecord) { busy.value = true; try { await credentialGuardAPI.probe(row.account_id); await refresh() } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
async function relogin() { const row = confirmLogin.value; confirmLogin.value = undefined; if (!row) return; busy.value = true; try { await stepUp.run(() => credentialGuardAPI.relogin(row.account_id)); await refresh() } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
onMounted(refresh)
</script>
