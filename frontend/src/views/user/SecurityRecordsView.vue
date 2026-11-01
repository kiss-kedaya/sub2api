<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5">
      <UserPageHeader flush :title="text('安全记录', 'Security records')" :description="text('查看自己的 API 安全限制、原因与解除时间；网站登录不受这些限制影响。', 'Review your API restrictions, reasons and expiry. These restrictions do not disable website sign-in.')"><template #actions><UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :disabled="loading" @click="load">{{ text('刷新', 'Refresh') }}</UButton></template></UserPageHeader>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
      <section v-if="summary" class="space-y-3 rounded-lg bg-default p-5 shadow-xs ring ring-default dark:shadow-none"><h2 class="text-base font-semibold text-highlighted">{{ summary.restricted ? text('当前存在 API 限制', 'API access is currently restricted') : text('当前没有生效的 API 限制', 'No active API restrictions') }}</h2><p v-if="summary.observe_only" class="text-sm text-gray-500">{{ text('当前规则为观察模式，仅记录，不限制请求。', 'The current policy records events without restricting requests.') }}</p><div v-for="state in summary.states" :key="state.scope + state.subject_key" class="rounded-md border border-default p-3 text-sm"><div class="flex flex-wrap justify-between gap-3"><span>{{ scopeLabel(state.scope) }} · {{ state.count }} / {{ summary.threshold }}</span><span>{{ actionLabel(state.action) }}</span></div><p class="mt-2">{{ text('原因', 'Reason') }}: {{ reasonLabel(state.reason) }}</p><p v-if="state.blocked_until || state.route_until" class="mt-1 text-gray-500">{{ text('解除时间', 'Expires') }}: {{ date(state.blocked_until || state.route_until) }}</p></div></section>
      <section class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none"><h2 class="border-b border-default px-5 py-4 text-base font-semibold text-highlighted">{{ text('历史记录', 'History') }}</h2><div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead class="bg-muted text-muted"><tr><th class="p-3">{{ text('时间', 'Time') }}</th><th class="p-3">{{ text('原因', 'Reason') }}</th><th class="p-3">{{ text('范围', 'Scope') }}</th><th class="p-3">{{ text('累计', 'Strikes') }}</th><th class="p-3">{{ text('请求', 'Request') }}</th></tr></thead><tbody><tr v-for="event in events" :key="event.id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-3 whitespace-nowrap">{{ date(event.created_at) }}</td><td class="p-3">{{ reasonLabel(event.reason) }}</td><td class="p-3">{{ scopeLabel(event.scope) }}</td><td class="p-3">{{ event.count }}</td><td class="p-3"><code class="break-all text-xs">{{ event.request_id }}</code></td></tr><tr v-if="!events.length"><td colspan="5" class="p-8 text-center text-gray-500">{{ text('暂无安全记录', 'No security records') }}</td></tr></tbody></table></div><div class="flex justify-end gap-2 border-t border-default px-5 py-3"><UButton color="neutral" variant="outline" :disabled="loading || page <= 1" @click="page--; load()">{{ text('上一页', 'Previous') }}</UButton><span class="self-center px-1 text-sm tabular-nums text-muted">{{ page }}</span><UButton color="neutral" variant="outline" :disabled="loading || page * 20 >= total" @click="page++; load()">{{ text('下一页', 'Next') }}</UButton></div></section>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
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
