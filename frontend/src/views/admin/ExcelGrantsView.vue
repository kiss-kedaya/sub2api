<template>
  <AppLayout>
    <div class="space-y-5">
      <AdminPageHeader title="Excel / BPS" :description="text('为现有 OpenAI OAuth 账号单独授权，按模型选择 BPS 通道。', 'Authorize an independent grant for an existing OpenAI OAuth account and select BPS models explicitly.')"><template #actions><button class="btn btn-secondary" :disabled="busy" @click="refresh">{{ text('刷新', 'Refresh') }}</button><button class="btn btn-primary" :disabled="busy" @click="edit()">{{ text('添加账号', 'Add account') }}</button></template></AdminPageHeader>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20">{{ error }}</p>
      <p class="text-xs text-gray-500">{{ text('默认关闭。只对所选模型生效，Codex 主凭证独立保存。授权完成后在模型设置中开启通道。', 'Disabled by default. Only selected models use this channel; primary Codex credentials remain separate. Enable routing in model settings after authorization.') }}</p>
      <div v-if="!rows.length && !busy" class="rounded-xl border border-dashed border-gray-300 p-10 text-center dark:border-dark-600"><p class="font-medium">{{ text('尚未配置 BPS 账号', 'No BPS accounts configured') }}</p><p class="mt-2 text-sm text-gray-500">{{ text('添加已有账号，选择模型，再完成官方授权。', 'Add an existing account, select exact models, then complete official authorization.') }}</p></div>
      <div v-else class="overflow-x-auto rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900"><table class="w-full min-w-[650px] text-left text-sm"><thead class="bg-gray-50 text-gray-500 dark:bg-dark-800"><tr><th class="p-4">{{ text('账号', 'Account') }}</th><th class="p-4">{{ text('通道', 'Channel') }}</th><th class="p-4">{{ text('模型', 'Models') }}</th><th class="p-4">{{ text('授权', 'Authorization') }}</th><th class="p-4">{{ text('操作', 'Actions') }}</th></tr></thead><tbody><tr v-for="row in rows" :key="row.account_id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-4"><p class="font-medium">{{ row.account_name }}</p><p class="text-xs text-gray-500">#{{ row.account_id }}</p></td><td class="p-4"><span class="badge" :class="row.config.enabled ? 'badge-success' : 'badge-gray'">{{ row.config.enabled ? text('已开启', 'Enabled') : text('已关闭', 'Disabled') }}</span></td><td class="max-w-xs p-4"><span v-for="model in row.config.models" :key="model" class="badge badge-gray mr-1">{{ model }}</span></td><td class="p-4">{{ row.configured ? text('已授权', 'Authorized') : text('待授权', 'Not authorized') }}</td><td class="p-4"><div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="busy" @click="edit(row)">{{ text('模型设置', 'Model settings') }}</button><button class="btn btn-primary btn-sm" :disabled="busy" @click="authorize(row)">{{ text('官方授权', 'Authorize') }}</button></div></td></tr></tbody></table></div>
    </div>
    <BaseDialog :show="settingsDialog" :title="text('BPS 模型设置', 'BPS model settings')" @close="settingsDialog = false"><div class="space-y-4"><label class="block text-sm">{{ text('现有账号 ID', 'Existing account ID') }}<input v-model.number="accountID" type="number" min="1" :disabled="!!editing" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 text-sm"><input v-model="enabled" type="checkbox" />{{ text('为下列模型开启 BPS 通道', 'Use BPS for the following models') }}</label><label class="block text-sm">{{ text('精确模型名（每行一个）', 'Exact model names (one per line)') }}<textarea v-model="modelsText" class="input mt-1 w-full font-mono text-xs" rows="5" /></label><p class="text-xs text-gray-500">{{ text('不支持通配符，也不会替换模型名称。搜索、生图等原生托管工具按兼容能力回到原 Codex 通道。', 'Wildcards and model renaming are not used. Hosted tools such as search and image generation use native Codex when the BPS bridge cannot support them.') }}</p><p v-if="dialogError" role="alert" class="text-sm text-red-600">{{ dialogError }}</p></div><template #footer><button class="btn btn-primary" :disabled="busy" @click="save">{{ text('保存', 'Save') }}</button></template></BaseDialog>
    <BaseDialog :show="!!auth" :title="text('完成官方 Excel 授权', 'Complete official Excel authorization')" @close="closeAuth"><div class="space-y-4"><p class="text-sm text-gray-500">{{ text('打开官方授权页，确认登录的是当前账号。授权跳转完成后，将浏览器地址栏中的完整回调地址粘贴到下方。地址有效期为 10 分钟，只能使用一次。', 'Open the official authorization page and sign in to the selected account. Paste the complete callback URL from the browser address bar below. It expires in 10 minutes and can only be used once.') }}</p><a v-if="auth" :href="auth.auth_url" target="_blank" rel="noopener noreferrer" class="btn btn-primary">{{ text('打开官方授权页', 'Open official authorization') }}</a><label class="block text-sm">{{ text('完整回调地址', 'Complete callback URL') }}<textarea v-model="callbackURL" class="input mt-1 w-full font-mono text-xs" rows="4" autocomplete="off" spellcheck="false" /></label><p v-if="authError" role="alert" class="text-sm text-red-600">{{ authError }}</p></div><template #footer><button class="btn btn-primary" :disabled="busy || !callbackURL.trim()" @click="complete">{{ text('验证并保存授权', 'Verify and save authorization') }}</button></template></BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
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
