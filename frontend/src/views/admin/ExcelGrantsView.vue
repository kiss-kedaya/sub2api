<template>
  <AppLayout>
    <div class="space-y-5">
      <AdminPageHeader title="Excel / BPS" :description="text('为现有 OpenAI OAuth 账号单独授权，按模型选择 BPS 通道。', 'Authorize an independent grant for an existing OpenAI OAuth account and select BPS models explicitly.')">
        <template #actions>
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :disabled="busy" :label="text('刷新', 'Refresh')" @click="refresh" />
          <UButton icon="i-lucide-plus" :disabled="busy" :label="text('添加账号', 'Add account')" @click="edit()" />
        </template>
      </AdminPageHeader>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
      <p class="text-xs text-muted">{{ text('默认关闭。只对所选模型生效，Codex 主凭证独立保存。授权完成后在模型设置中开启通道。', 'Disabled by default. Only selected models use this channel; primary Codex credentials remain separate. Enable routing in model settings after authorization.') }}</p>
      <div v-if="!rows.length && !busy" class="py-10 text-center">
        <p class="font-medium text-highlighted">{{ text('尚未配置 BPS 账号', 'No BPS accounts configured') }}</p>
        <p class="mt-2 text-sm text-muted">{{ text('添加已有账号，选择模型，再完成官方授权。', 'Add an existing account, select exact models, then complete official authorization.') }}</p>
      </div>
      <UTable v-else :data="rows" :columns="columns" :loading="busy" class="min-w-[650px]">
        <template #account-cell="{ row }">
          <p class="font-medium text-highlighted">{{ row.original.account_name }}</p>
          <p class="text-xs text-muted">#{{ row.original.account_id }}</p>
        </template>
        <template #channel-cell="{ row }">
          <UBadge :color="row.original.config.enabled ? 'success' : 'neutral'" variant="subtle" :label="row.original.config.enabled ? text('已开启', 'Enabled') : text('已关闭', 'Disabled')" />
        </template>
        <template #models-cell="{ row }">
          <div class="flex max-w-xs flex-wrap gap-1">
            <UBadge v-for="model in row.original.config.models" :key="model" color="neutral" variant="outline" :label="model" />
          </div>
        </template>
        <template #authorization-cell="{ row }">
          <UBadge :color="row.original.configured ? 'success' : 'warning'" variant="subtle" :label="row.original.configured ? text('已授权', 'Authorized') : text('待授权', 'Not authorized')" />
        </template>
        <template #actions-cell="{ row }">
          <div class="flex gap-1.5">
            <UButton color="neutral" variant="outline" size="sm" :disabled="busy" :label="text('模型设置', 'Model settings')" @click="edit(row.original)" />
            <UButton size="sm" :disabled="busy" :label="text('官方授权', 'Authorize')" @click="authorize(row.original)" />
          </div>
        </template>
      </UTable>
    </div>
    <BaseDialog :show="settingsDialog" :title="text('BPS 模型设置', 'BPS model settings')" @close="settingsDialog = false">
      <div class="space-y-4">
        <UFormField :label="text('现有账号 ID', 'Existing account ID')">
          <UInput v-model.number="accountID" type="number" min="1" :disabled="!!editing" class="w-full" />
        </UFormField>
        <USwitch v-model="enabled" :label="text('为下列模型开启 BPS 通道', 'Use BPS for the following models')" />
        <UFormField
          :label="text('精确模型名（每行一个）', 'Exact model names (one per line)')"
          :help="text('不支持通配符，也不会替换模型名称。搜索、生图等原生托管工具按兼容能力回到原 Codex 通道。', 'Wildcards and model renaming are not used. Hosted tools such as search and image generation use native Codex when the BPS bridge cannot support them.')"
        >
          <UTextarea v-model="modelsText" :rows="5" class="w-full" :ui="{ base: 'font-mono text-xs' }" />
        </UFormField>
        <p v-if="dialogError" role="alert" class="text-sm text-error">{{ dialogError }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="text('取消', 'Cancel')" @click="settingsDialog = false" />
          <UButton :disabled="busy" :label="text('保存', 'Save')" @click="save" />
        </div>
      </template>
    </BaseDialog>
    <BaseDialog :show="!!auth" :title="text('完成官方 Excel 授权', 'Complete official Excel authorization')" @close="closeAuth">
      <div class="space-y-4">
        <p class="text-sm text-muted">{{ text('打开官方授权页，确认登录的是当前账号。授权跳转完成后，将浏览器地址栏中的完整回调地址粘贴到下方。地址有效期为 10 分钟，只能使用一次。', 'Open the official authorization page and sign in to the selected account. Paste the complete callback URL from the browser address bar below. It expires in 10 minutes and can only be used once.') }}</p>
        <UButton v-if="auth" :href="auth.auth_url" target="_blank" rel="noopener noreferrer" icon="i-lucide-external-link" :label="text('打开官方授权页', 'Open official authorization')" />
        <UFormField :label="text('完整回调地址', 'Complete callback URL')">
          <UTextarea v-model="callbackURL" :rows="4" autocomplete="off" spellcheck="false" class="w-full" :ui="{ base: 'font-mono text-xs' }" />
        </UFormField>
        <p v-if="authError" role="alert" class="text-sm text-error">{{ authError }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :disabled="busy" :label="text('取消', 'Cancel')" @click="closeAuth" />
          <UButton :disabled="busy || !callbackURL.trim()" :label="text('验证并保存授权', 'Verify and save authorization')" @click="complete" />
        </div>
      </template>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp } from '@/composables/useStepUp'
import { excelGrantsAPI, type ExcelGrantRecord } from '@/api/admin/excelGrants'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const stepUp = useStepUp()
const columns = computed<TableColumn<ExcelGrantRecord>[]>(() => [
  { id: 'account', header: text('账号', 'Account') },
  { id: 'channel', header: text('通道', 'Channel') },
  { id: 'models', header: text('模型', 'Models') },
  { id: 'authorization', header: text('授权', 'Authorization') },
  { id: 'actions', header: text('操作', 'Actions') }
])
const rows = ref<ExcelGrantRecord[]>([])
const busy = ref(false)
const error = ref('')
const dialogError = ref('')
const authError = ref('')
const settingsDialog = ref(false)
const editing = ref<ExcelGrantRecord>()
const accountID = ref<number>()
const enabled = ref(false)
const modelsText = ref('')
const auth = ref<{ account_id: number; auth_url: string; session_id: string }>()
const callbackURL = ref('')
const message = (cause: unknown) => (cause as { message?: string })?.message || text('操作失败，请重试', 'Operation failed. Please retry.')
async function refresh() { busy.value = true; try { rows.value = (await excelGrantsAPI.list()).items; error.value = '' } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
function edit(row?: ExcelGrantRecord) { editing.value = row; accountID.value = row?.account_id; enabled.value = row?.config.enabled || false; modelsText.value = row?.config.models.join('\n') || ''; dialogError.value = ''; settingsDialog.value = true }
async function save() { if (!accountID.value) { dialogError.value = text('请输入账号 ID', 'Enter an account ID'); return } busy.value = true; try { await excelGrantsAPI.save(accountID.value, enabled.value, modelsText.value.split(/\r?\n/).map(value => value.trim()).filter(Boolean), editing.value?.revision || ''); settingsDialog.value = false; await refresh() } catch (cause) { dialogError.value = message(cause) } finally { busy.value = false } }
async function authorize(row: ExcelGrantRecord) { busy.value = true; authError.value = ''; try { auth.value = { account_id: row.account_id, ...await stepUp.run(() => excelGrantsAPI.authorize(row.account_id)) }; callbackURL.value = '' } catch (cause) { error.value = message(cause) } finally { busy.value = false } }
function closeAuth() { if (busy.value) return; auth.value = undefined; callbackURL.value = ''; authError.value = '' }
async function complete() { const current = auth.value; if (!current) return; busy.value = true; try { await stepUp.run(() => excelGrantsAPI.callback(current.account_id, current.session_id, callbackURL.value.trim())); busy.value = false; closeAuth(); await refresh() } catch (cause) { authError.value = message(cause) } finally { busy.value = false } }
onMounted(refresh)
onUnmounted(() => { auth.value = undefined; callbackURL.value = '' })
</script>
