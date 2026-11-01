<template>
  <AppLayout>
    <div class="space-y-5">
      <AdminPageHeader :title="text('凭证守护', 'Credential inspection')" :description="text('按账号开启巡检，失效凭证按需恢复；风控验证交由人工处理。', 'Opt in per account to inspect credentials and recover expired sessions. Security verification always requires an operator.')">
        <template #actions><button class="btn btn-secondary" :disabled="busy" @click="refresh">{{ text('刷新', 'Refresh') }}</button><button class="btn btn-primary" :disabled="busy" @click="openNew">{{ text('添加账号', 'Add account') }}</button></template>
      </AdminPageHeader>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20">{{ error }}</p>
      <p class="text-xs text-gray-500">{{ text('保留手工停调及已有错误状态。需要恢复这类账号时，先检查原停调原因；本功能不会直接重新开启。', 'Manual scheduling choices and existing error states are preserved. Review their original cause before restoring an account; this feature never overrides them.') }}</p>
      <div v-if="!rows.length && !busy" class="rounded-xl border border-dashed border-gray-300 p-10 text-center dark:border-dark-600"><p class="font-medium">{{ text('还没有开启凭证巡检', 'No accounts are being inspected') }}</p><p class="mt-2 text-sm text-gray-500">{{ text('添加账号后默认保持关闭，保存并开启才会运行。', 'New inspection settings are disabled by default. Save and enable them when ready.') }}</p></div>
      <div v-else class="overflow-x-auto rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
        <table class="w-full min-w-[700px] text-left text-sm"><thead class="bg-gray-50 text-gray-500 dark:bg-dark-800"><tr><th class="p-4">{{ text('账号', 'Account') }}</th><th class="p-4">{{ text('状态', 'Status') }}</th><th class="p-4">{{ text('最近检测', 'Last checked') }}</th><th class="p-4">{{ text('重登失败', 'Login failures') }}</th><th class="p-4">{{ text('操作', 'Actions') }}</th></tr></thead>
          <tbody><tr v-for="row in rows" :key="row.account_id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-4"><p class="font-medium">{{ row.account_name }}</p><p class="text-xs text-gray-500">#{{ row.account_id }}</p></td><td class="p-4"><span class="badge" :class="row.blocked ? 'badge-danger' : row.state === 'healthy' ? 'badge-success' : 'badge-gray'">{{ statusLabel(row) }}</span><p class="mt-1 max-w-xs text-xs text-gray-500">{{ codeLabel(row.code) }}</p></td><td class="p-4">{{ row.last_checked_at ? new Date(row.last_checked_at).toLocaleString() : '—' }}</td><td class="p-4">{{ row.failures }} / {{ row.config.max_failures }}</td><td class="p-4"><div class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" :disabled="busy" @click="edit(row)">{{ text('设置', 'Settings') }}</button><button class="btn btn-secondary btn-sm" :disabled="busy" @click="probe(row)">{{ text('立即检测', 'Inspect now') }}</button><button v-if="row.secrets_configured && row.config.relogin_endpoint" class="btn btn-secondary btn-sm" :disabled="busy || row.blocked" @click="confirmLogin = row">{{ text('重新登录', 'Re-login') }}</button></div></td></tr></tbody>
        </table>
      </div>
    </div>
    <BaseDialog :show="dialog" :title="text('凭证巡检设置', 'Credential inspection settings')" @close="closeDialog">
      <div class="space-y-4">
        <label v-if="!editingID" class="block text-sm">{{ text('账号 ID', 'Account ID') }}<input v-model.number="accountID" type="number" min="1" class="input mt-1 w-full" /></label>
        <p v-else class="text-sm font-medium">{{ editingName }} · #{{ editingID }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" />{{ text('开启定时巡检', 'Enable scheduled inspection') }}</label>
        <div class="grid grid-cols-2 gap-3"><label class="text-sm">{{ text('检测间隔（秒）', 'Inspection interval (seconds)') }}<input v-model.number="draft.interval_seconds" type="number" min="60" max="86400" class="input mt-1 w-full" /></label><label class="text-sm">{{ text('失败上限', 'Failure budget') }}<input v-model.number="draft.max_failures" type="number" min="1" max="5" class="input mt-1 w-full" /></label></div>
        <details class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"><summary class="cursor-pointer text-sm font-medium">{{ text('自动重登（可选）', 'Automatic re-login (optional)') }}</summary>
          <div class="mt-4 space-y-3">
            <p class="text-xs text-gray-500">{{ text('使用你自行信任的 HTTPS 登录服务。保存的密码和验证密钥会加密，页面不回显。403、安全验证或失败超限后停止自动恢复。', 'Use your own trusted HTTPS login service. Passwords and verification secrets are encrypted and never returned. Recovery stops after a 403, security verification or exhausted failure budget.') }}</p>
            <label class="flex items-center gap-2 text-sm"><input v-model="draft.auto_relogin" type="checkbox" />{{ text('失效后自动重新登录', 'Automatically re-login after credential expiry') }}</label>
            <label class="block text-sm">{{ text('登录服务地址', 'Login service URL') }}<input v-model="draft.relogin_endpoint" type="url" autocomplete="off" class="input mt-1 w-full" placeholder="https://login.example.com/api/relogin" /></label>
            <label class="block text-sm">{{ text('明确允许的服务来源', 'Explicitly allowed service origin') }}<input v-model="draft.allowed_origin" type="url" autocomplete="off" class="input mt-1 w-full" placeholder="https://login.example.com" /></label>
            <label class="block text-sm">{{ text('登录邮箱', 'Login email') }}<input v-model="draft.login_email" type="email" autocomplete="off" class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ text('密码（留空保留）', 'Password (blank preserves saved value)') }}<input v-model="draft.password" type="password" autocomplete="new-password" class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ text('账号 TOTP 密钥（可选）', 'Account TOTP secret (optional)') }}<input v-model="draft.totp_secret" type="password" autocomplete="new-password" class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ text('登录服务 API Token（可选）', 'Login service API token (optional)') }}<input v-model="draft.api_token" type="password" autocomplete="new-password" class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ text('重登冷却（秒）', 'Re-login cooldown (seconds)') }}<input v-model.number="draft.cooldown_seconds" type="number" min="300" max="604800" class="input mt-1 w-full" /></label>
            <label class="flex items-center gap-2 text-sm"><input v-model="draft.clear_secrets" type="checkbox" />{{ text('清除已保存登录凭据', 'Clear saved login credentials') }}</label>
            <label class="flex items-center gap-2 text-sm"><input v-model="draft.reset_failures" type="checkbox" />{{ text('问题已处理，重置失败次数和人工阻断', 'Issue resolved: reset failures and manual block') }}</label>
          </div>
        </details>
        <p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p>
      </div>
      <template #footer><button class="btn btn-secondary" :disabled="busy" @click="closeDialog">{{ text('取消', 'Cancel') }}</button><button class="btn btn-primary" :disabled="busy" @click="save">{{ text('保存', 'Save') }}</button></template>
    </BaseDialog>
    <ConfirmDialog :show="!!confirmLogin" :title="text('确认重新登录', 'Confirm re-login')" :message="text('将向已配置的登录服务发送此账号的已保存登录凭据。只有当前凭证明确失效时才会执行。', 'Saved login credentials will be sent to your configured login service. This only runs if the current credential is explicitly invalid.')" @cancel="confirmLogin = undefined" @confirm="relogin" />
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
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
const accountID = ref<number>()
const editingID = ref<number>()
const editingName = ref('')
const confirmLogin = ref<CredentialGuardRecord>()
const defaults = (): CredentialGuardInput => ({ enabled: false, auto_relogin: false, interval_seconds: 1800, cooldown_seconds: 1800, max_failures: 3, relogin_endpoint: '', allowed_origin: '', login_email: '', revision: '', password: '', totp_secret: '', api_token: '', clear_secrets: false, reset_failures: false })
const draft = ref<CredentialGuardInput>(defaults())
const message = (cause: unknown) => (cause as { message?: string })?.message || text('操作失败，请重试', 'Operation failed. Please retry.')
function statusLabel(row: CredentialGuardRecord) { return row.blocked ? text('需要人工处理', 'Operator required') : !row.config.enabled ? text('已关闭', 'Disabled') : ({ healthy: text('正常', 'Healthy'), invalid: text('凭证失效', 'Expired'), transient: text('暂时不可用', 'Temporarily unavailable'), skipped: text('已跳过', 'Skipped') }[row.state] || text('待检测', 'Pending')) }
function codeLabel(code: string) { const labels: Record<string, string> = { credential_invalid: text('上游明确拒绝当前凭证', 'The upstream rejected this credential'), policy_blocked: text('上游拒绝或安全策略阻断', 'Upstream policy denied the request'), security_verification_required: text('请人工完成安全验证', 'Complete security verification manually'), relogin_succeeded: text('凭证已恢复并验证通过', 'Credentials recovered and verified'), account_disabled_by_operator: text('保留管理员停调设置', 'Operator scheduling choice preserved'), relogin_budget_or_cooldown: text('达到失败上限或仍在冷却', 'Failure budget exhausted or cooldown active'), identity_mismatch: text('新凭证身份不匹配，未写回', 'Replacement identity mismatch; no credential was saved') }; return labels[code] || (code ? text('查看最近一次检测结果', 'Review the latest inspection result') : '') }
async function refresh() { busy.value = true; try { rows.value = (await credentialGuardAPI.list()).items; error.value = '' } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
function openNew() { editingID.value = undefined; editingName.value = ''; accountID.value = undefined; draft.value = defaults(); dialogError.value = ''; dialog.value = true }
function edit(row: CredentialGuardRecord) { editingID.value = row.account_id; editingName.value = row.account_name; accountID.value = row.account_id; draft.value = { ...defaults(), ...row.config, revision: row.revision }; dialogError.value = ''; dialog.value = true }
function closeDialog() { if (busy.value) return; draft.value = defaults(); dialog.value = false }
async function save() { if (!accountID.value) { dialogError.value = text('请输入账号 ID', 'Enter an account ID'); return } busy.value = true; try { await stepUp.run(() => credentialGuardAPI.save(accountID.value!, draft.value)); busy.value = false; closeDialog(); await refresh() } catch (cause) { dialogError.value = message(cause) } finally { busy.value = false } }
async function probe(row: CredentialGuardRecord) { busy.value = true; try { await credentialGuardAPI.probe(row.account_id); await refresh() } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
async function relogin() { const row = confirmLogin.value; confirmLogin.value = undefined; if (!row) return; busy.value = true; try { await stepUp.run(() => credentialGuardAPI.relogin(row.account_id)); await refresh() } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
onMounted(refresh)
</script>
