<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5">
      <div class="flex items-center justify-between gap-4"><div><h1 class="text-2xl font-semibold">{{ text('安全记录', 'Security records') }}</h1><p class="mt-2 text-sm text-gray-500">{{ text('查看自己的 API 安全限制、原因与解除时间；网站登录不受这些限制影响。', 'Review your API restrictions, reasons and expiry. These restrictions do not disable website sign-in.') }}</p></div><button class="btn btn-secondary" :disabled="loading" @click="load">{{ text('刷新', 'Refresh') }}</button></div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-950">{{ error }}</p>
      <section v-if="summary" class="card space-y-3 p-5"><h2 class="font-semibold">{{ summary.restricted ? text('当前存在 API 限制', 'API access is currently restricted') : text('当前没有生效的 API 限制', 'No active API restrictions') }}</h2><p v-if="summary.observe_only" class="text-sm text-gray-500">{{ text('当前规则为观察模式，仅记录，不限制请求。', 'The current policy records events without restricting requests.') }}</p><div v-for="state in summary.states" :key="state.scope + state.subject_key" class="rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700"><div class="flex flex-wrap justify-between gap-3"><span>{{ scopeLabel(state.scope) }} · {{ state.count }} / {{ summary.threshold }}</span><span>{{ actionLabel(state.action) }}</span></div><p class="mt-2">{{ text('原因', 'Reason') }}: {{ reasonLabel(state.reason) }}</p><p v-if="state.blocked_until || state.route_until" class="mt-1 text-gray-500">{{ text('解除时间', 'Expires') }}: {{ date(state.blocked_until || state.route_until) }}</p></div></section>
      <section class="card overflow-hidden"><h2 class="p-5 font-semibold">{{ text('历史记录', 'History') }}</h2><div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead class="bg-gray-50 text-gray-500 dark:bg-dark-800"><tr><th class="p-3">{{ text('时间', 'Time') }}</th><th class="p-3">{{ text('原因', 'Reason') }}</th><th class="p-3">{{ text('范围', 'Scope') }}</th><th class="p-3">{{ text('累计', 'Strikes') }}</th><th class="p-3">{{ text('请求', 'Request') }}</th></tr></thead><tbody><tr v-for="event in events" :key="event.id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-3 whitespace-nowrap">{{ date(event.created_at) }}</td><td class="p-3">{{ reasonLabel(event.reason) }}</td><td class="p-3">{{ scopeLabel(event.scope) }}</td><td class="p-3">{{ event.count }}</td><td class="p-3"><code class="break-all text-xs">{{ event.request_id }}</code></td></tr><tr v-if="!events.length"><td colspan="5" class="p-8 text-center text-gray-500">{{ text('暂无安全记录', 'No security records') }}</td></tr></tbody></table></div><div class="flex justify-end gap-3 p-4"><button class="btn btn-secondary" :disabled="loading || page <= 1" @click="page--; load()">{{ text('上一页', 'Previous') }}</button><span class="self-center">{{ page }}</span><button class="btn btn-secondary" :disabled="loading || page * 20 >= total" @click="page++; load()">{{ text('下一页', 'Next') }}</button></div></section>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { myPenaltySummary, myPenaltyEvents, type PenaltySummary, type PenaltyEvent } from '@/api/securityPenalties'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const summary = ref<PenaltySummary>(), events = ref<PenaltyEvent[]>([]), page = ref(1), total = ref(0), loading = ref(false), error = ref('')
let disposed = false
const date = (value?: string) => value ? new Date(value).toLocaleString() : '—'
const scopeLabel = (scope: string) => scope === 'user' ? text('全部 API Key', 'All API keys') : scope === 'session' ? text('该会话（跨 Key）', 'Conversation across keys') : text('会话级策略：无标识，仅记录', 'Session policy: no identity; recorded only')
const actionLabel = (action: string) => action === 'block' ? text('禁止 API 请求', 'API requests blocked') : action === 'route' ? text('仅允许受限分组', 'Restricted group only') : text('仅记录', 'Recorded')
const reasonLabel = (reason: string) => reason === 'prompt_audit_blocked' ? text('提示词安全审计命中', 'Prompt safety audit finding') : reason === 'content_policy_violation' ? text('内容审核命中', 'Content moderation finding') : reason || '—'
async function load() { if (loading.value) return; loading.value = true; error.value = ''; try { const [state, records] = await Promise.all([myPenaltySummary(), myPenaltyEvents(page.value)]); if (!disposed) { summary.value = state; events.value = records.items; total.value = records.total } } catch (cause) { if (!disposed) error.value = (cause as Error).message || text('加载失败', 'Could not load records') } finally { if (!disposed) loading.value = false } }
onMounted(load)
onUnmounted(() => { disposed = true })
</script>
