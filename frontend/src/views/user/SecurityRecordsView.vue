<template>
  <AppLayout>
    <div class="mx-auto flex w-full max-w-5xl flex-col gap-4 sm:gap-6">
      <UserPageHeader flush :title="text('安全记录', 'Security records')" :description="text('查看自己的 API 安全限制、原因与解除时间；网站登录不受这些限制影响。', 'Review your API restrictions, reasons and expiry. These restrictions do not disable website sign-in.')" />

      <div class="flex flex-wrap items-center justify-end gap-1.5">
        <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :ui="{ leadingIcon: loading ? 'animate-spin' : '' }" :disabled="loading" @click="load">{{ text('刷新', 'Refresh') }}</UButton>
      </div>

      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />

      <UAlert
        v-if="summary"
        :color="summary.restricted ? 'warning' : 'success'"
        variant="subtle"
        :icon="summary.restricted ? 'i-lucide-shield-alert' : 'i-lucide-shield-check'"
        :title="summary.restricted ? text('当前存在 API 限制', 'API access is currently restricted') : text('当前没有生效的 API 限制', 'No active API restrictions')"
        :description="summary.observe_only ? text('当前规则为观察模式，仅记录，不限制请求。', 'The current policy records events without restricting requests.') : undefined"
      />

      <UPageCard v-if="summary?.states.length" variant="subtle" :ui="{ container: 'divide-y divide-default p-0 sm:p-0 gap-y-0' }">
        <div v-for="state in summary.states" :key="state.scope + state.subject_key" class="flex flex-wrap items-start justify-between gap-3 px-4 py-3 text-sm sm:px-6">
          <div class="min-w-0">
            <p class="font-medium text-highlighted">{{ scopeLabel(state.scope) }} <span class="tabular-nums text-muted">· {{ state.count }} / {{ summary.threshold }}</span></p>
            <p class="mt-0.5 text-muted">{{ text('原因', 'Reason') }}: {{ reasonLabel(state.reason) }}</p>
            <p v-if="state.blocked_until || state.route_until" class="mt-0.5 text-muted">{{ text('解除时间', 'Expires') }}: {{ date(state.blocked_until || state.route_until) }}</p>
          </div>
          <UBadge :color="state.action === 'block' ? 'error' : state.action === 'route' ? 'warning' : 'neutral'" variant="subtle">{{ actionLabel(state.action) }}</UBadge>
        </div>
      </UPageCard>

      <section class="flex flex-col gap-3">
        <h2 class="text-sm font-medium text-highlighted">{{ text('历史记录', 'History') }}</h2>
        <UTable
          :data="events"
          :columns="columns"
          :loading="loading"
          :ui="{
            root: 'rounded-lg ring ring-default',
            base: 'border-separate border-spacing-0',
            thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
            tbody: '[&>tr]:last:[&>td]:border-b-0',
            th: 'py-2 border-b border-default',
            td: 'border-b border-default',
            separator: 'h-0'
          }"
        >
          <template #created_at-cell="{ row }"><span class="whitespace-nowrap">{{ date(row.original.created_at) }}</span></template>
          <template #reason-cell="{ row }">{{ reasonLabel(row.original.reason) }}</template>
          <template #scope-cell="{ row }">{{ scopeLabel(row.original.scope) }}</template>
          <template #count-cell="{ row }"><span class="tabular-nums">{{ row.original.count }}</span></template>
          <template #request_id-cell="{ row }"><code class="break-all font-mono text-xs text-muted">{{ row.original.request_id }}</code></template>
          <template #empty><p class="py-6 text-center text-muted">{{ text('暂无安全记录', 'No security records') }}</p></template>
        </UTable>
        <div class="flex items-center justify-end gap-1.5 border-t border-default pt-4">
          <UButton color="neutral" variant="outline" icon="i-lucide-chevron-left" :disabled="loading || page <= 1" @click="page--; load()">{{ text('上一页', 'Previous') }}</UButton>
          <span class="px-1 text-sm tabular-nums text-muted">{{ page }}</span>
          <UButton color="neutral" variant="outline" trailing-icon="i-lucide-chevron-right" :disabled="loading || page * 20 >= total" @click="page++; load()">{{ text('下一页', 'Next') }}</UButton>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import { myPenaltySummary, myPenaltyEvents, type PenaltySummary, type PenaltyEvent } from '@/api/securityPenalties'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const summary = ref<PenaltySummary>(), events = ref<PenaltyEvent[]>([]), page = ref(1), total = ref(0), loading = ref(false), error = ref('')
let disposed = false
const columns = computed<TableColumn<PenaltyEvent>[]>(() => [
  { accessorKey: 'created_at', header: text('时间', 'Time') },
  { accessorKey: 'reason', header: text('原因', 'Reason') },
  { accessorKey: 'scope', header: text('范围', 'Scope') },
  { accessorKey: 'count', header: text('累计', 'Strikes') },
  { accessorKey: 'request_id', header: text('请求', 'Request') }
])
const date = (value?: string) => value ? new Date(value).toLocaleString() : '—'
const scopeLabel = (scope: string) => scope === 'user' ? text('全部 API Key', 'All API keys') : scope === 'session' ? text('该会话（跨 Key）', 'Conversation across keys') : text('会话级策略：无标识，仅记录', 'Session policy: no identity; recorded only')
const actionLabel = (action: string) => action === 'block' ? text('禁止 API 请求', 'API requests blocked') : action === 'route' ? text('仅允许受限分组', 'Restricted group only') : text('仅记录', 'Recorded')
const reasonLabel = (reason: string) => reason === 'prompt_audit_blocked' ? text('提示词安全审计命中', 'Prompt safety audit finding') : reason === 'content_policy_violation' ? text('内容审核命中', 'Content moderation finding') : reason || '—'
async function load() { if (loading.value) return; loading.value = true; error.value = ''; try { const [state, records] = await Promise.all([myPenaltySummary(), myPenaltyEvents(page.value)]); if (!disposed) { summary.value = state; events.value = records.items; total.value = records.total } } catch (cause) { if (!disposed) error.value = (cause as Error).message || text('加载失败', 'Could not load records') } finally { if (!disposed) loading.value = false } }
onMounted(load)
onUnmounted(() => { disposed = true })
</script>
