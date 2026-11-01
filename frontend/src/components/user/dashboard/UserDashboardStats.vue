<template>
  <section class="signal-stats min-w-0" :aria-label="t('dashboard.title')">
    <UPageGrid v-if="section !== 'platforms'" :class="statGridClass">
      <DashboardStat v-if="!isSimple" icon="i-lucide-wallet" class="signal-metric signal-metric--balance" :class="corner('balance')">
        <template #title>{{ t('dashboard.balance') }} <span class="signal-unit">USD</span></template>
        <template #value>$<AnimatedNumber :value="balance" :format="formatBalance" /></template>
        <template #sub>
          <div class="flex flex-wrap gap-x-3"><span>{{ t('dashboard.recentTotal') }} / {{ t('dashboard.actual') }} <span class="text-highlighted">${{ formatCost(stats?.total_actual_cost || 0) }}</span></span> <span>{{ t('dashboard.standard') }} ${{ formatCost(stats?.total_cost || 0) }}</span></div>
        </template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-circle-dollar-sign" :title="t('dashboard.todayCost')" class="signal-metric signal-metric--cost" :class="corner('cost')">
        <template #value><span :title="t('dashboard.actual')">$<AnimatedNumber :value="stats?.today_actual_cost || 0" :format="formatCost" :duration="480" /></span></template>
        <template #sub>{{ t('dashboard.standard') }} <span class="text-highlighted">${{ formatCost(stats?.today_cost || 0) }}</span></template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-coins" :title="t('dashboard.todayTokens')" class="signal-metric signal-metric--tokens" :class="corner('tokens')">
        <template #value><AnimatedNumber :value="stats?.today_tokens || 0" :format="formatTokens" /></template>
        <template #sub>
          <div class="flex flex-wrap gap-x-3"><span>{{ t('dashboard.input') }} {{ formatTokens(stats?.today_input_tokens || 0) }}</span> <span>{{ t('dashboard.output') }} {{ formatTokens(stats?.today_output_tokens || 0) }}</span> <span>{{ t('dashboard.cache') }} {{ formatTokens((stats?.today_cache_creation_tokens || 0) + (stats?.today_cache_read_tokens || 0)) }}</span></div>
        </template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-chart-column" :title="t('dashboard.todayRequests')" class="signal-metric" :class="corner('requests')">
        <template #value><AnimatedNumber :value="stats?.today_requests || 0" :decimals="0" /></template>
        <template #sub>{{ t('dashboard.recentTotal') }} <span class="text-highlighted">{{ formatNumber(stats?.total_requests || 0) }}</span></template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-layers" :title="t('dashboard.recentTokens')" class="signal-metric signal-metric--total" :class="corner('total')">
        <template #value><AnimatedNumber :value="stats?.total_tokens || 0" :format="formatTokens" /></template>
        <template #sub>
          <dl class="signal-token-split flex flex-wrap gap-x-3 gap-y-1">
            <div class="flex gap-1"><dt>{{ t('dashboard.input') }}</dt><dd class="text-highlighted">{{ formatTokens(stats?.total_input_tokens || 0) }}</dd></div>
            <div class="flex gap-1"><dt>{{ t('dashboard.output') }}</dt><dd class="text-highlighted">{{ formatTokens(stats?.total_output_tokens || 0) }}</dd></div>
            <div class="flex gap-1"><dt>{{ t('dashboard.cache') }}</dt><dd class="text-highlighted">{{ formatTokens((stats?.total_cache_creation_tokens || 0) + (stats?.total_cache_read_tokens || 0)) }}</dd></div>
          </dl>
        </template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-key-round" :title="t('dashboard.apiKeys')" class="signal-metric" :class="corner('keys')">
        <template #value><AnimatedNumber :value="stats?.total_api_keys || 0" :decimals="0" /></template>
        <template #badge>
          <UBadge class="signal-active" color="success" variant="subtle">{{ stats?.active_api_keys || 0 }} {{ t('common.active') }}</UBadge>
        </template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-zap" :title="t('dashboard.performance')" class="signal-reading" :class="corner('performance')">
        <template #value>{{ formatTokens(stats?.rpm || 0) }} <span class="text-sm font-normal text-muted">RPM</span></template>
        <template #sub><span class="text-highlighted">{{ formatTokens(stats?.tpm || 0) }}</span> TPM</template>
      </DashboardStat>
      <DashboardStat icon="i-lucide-clock" :title="t('dashboard.avgResponse')" :value="formatDuration(stats?.average_duration_ms || 0)" class="signal-reading" :class="corner('response')" />
    </UPageGrid>

    <section v-if="section !== 'summary' && !isSimple && platformCards.length > 0" class="signal-platforms">
      <header class="flex flex-wrap items-baseline justify-between gap-2 pb-3 pt-4">
        <h2 class="text-sm font-semibold text-highlighted">{{ t('dashboard.last30Days') }}</h2>
        <span class="text-xs text-muted">{{ t('dashboard.platformCount', { count: platformCards.length }) }}</span>
      </header>
      <div class="grid grid-cols-1 gap-x-6 min-[421px]:grid-cols-2 min-[1101px]:grid-cols-4">
        <article v-for="item in platformCards" :key="item.platform" class="signal-platform">
          <header class="signal-platform-heading">
            <span class="signal-platform-name">
              <PlatformIcon :platform="item.platform as GroupPlatform" size="lg" />
              <span>{{ platformLabel(item.platform) }}</span>
            </span>
            <span class="signal-spend" :title="t('dashboard.actual')">${{ formatCost(item.total_actual_cost) }}</span>
          </header>
          <dl class="signal-platform-ledger">
            <div><dt>{{ t('dashboard.todayCost') }}</dt><dd>${{ formatCost(item.today_actual_cost) }}</dd></div>
            <div><dt>{{ t('dashboard.requests') }}</dt><dd>{{ item.total_requests > 0 ? formatNumber(item.total_requests) : '-' }}</dd></div>
            <div><dt>{{ t('dashboard.tokens') }}</dt><dd>{{ item.total_tokens > 0 ? formatTokens(item.total_tokens) : '-' }}</dd></div>
          </dl>
          <div v-if="hasAnyLimit(item.quota)" class="signal-quota">
            <p class="signal-label">{{ t('dashboard.platformQuota.title') }}</p>
            <template v-for="w in (['daily', 'weekly', 'monthly'] as const)" :key="w">
              <div v-if="quotaVal(item.quota, `${w}_limit_usd`) != null" class="signal-quota-window">
                <template v-if="(quotaVal(item.quota, `${w}_limit_usd`) as number) === 0">
                  <div class="signal-quota-label"><span>{{ t(`dashboard.platformQuota.${w}`) }}</span><span class="signal-disabled">{{ t('dashboard.platformQuota.disabled') }}</span></div>
                  <div class="signal-quota-track"><div class="signal-quota-fill bg-red-500" style="width: 100%" /></div>
                </template>
                <template v-else>
                  <div class="signal-quota-label">
                    <span>{{ t(`dashboard.platformQuota.${w}`) }}</span>
                    <span>${{ formatUsd((quotaVal(item.quota, `${w}_usage_usd`) as number) ?? 0) }} / ${{ formatUsd(quotaVal(item.quota, `${w}_limit_usd`) as number) }}</span>
                  </div>
                  <div class="signal-quota-track">
                    <div class="signal-quota-fill"
                      :class="quotaBarClass(calcPercent((quotaVal(item.quota, `${w}_usage_usd`) as number) ?? 0, quotaVal(item.quota, `${w}_limit_usd`) as number))"
                      :style="{ width: calcPercent((quotaVal(item.quota, `${w}_usage_usd`) as number) ?? 0, quotaVal(item.quota, `${w}_limit_usd`) as number) + '%' }" />
                  </div>
                  <p v-if="quotaVal(item.quota, `${w}_window_resets_at`)" class="signal-reset">{{ t('dashboard.platformQuota.resetsAt', { time: formatResetTime(quotaVal(item.quota, `${w}_window_resets_at`) as string) }) }}</p>
                </template>
              </div>
            </template>
          </div>
        </article>
      </div>
    </section>
    <p v-else-if="section === 'platforms' && !isSimple" class="signal-platform-empty">{{ t('dashboard.platformBreakdownEmpty') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AnimatedNumber from '@/components/common/AnimatedNumber.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import DashboardStat from '@/components/dashboard/DashboardStat.vue'
import { statCorners, statGridClass } from '@/components/dashboard/dashboardUi'
import type { UserDashboardStats as UserStatsType } from '@/api/usage'
import type { PlatformQuotaItem, GroupPlatform } from '@/types'

interface FusedPlatformCard {
  platform: string
  total_actual_cost: number
  today_actual_cost: number
  total_requests: number
  total_tokens: number
  quota?: PlatformQuotaItem
}

const props = defineProps<{
  stats: UserStatsType
  balance: number
  isSimple: boolean
  platformQuotas?: PlatformQuotaItem[] | null
  section?: 'summary' | 'platforms'
}>()
const { t } = useI18n()

// Tile order of the joined stat grid (two rows of four; simple mode has no balance tile).
const tiles = computed(() => [...(props.isSimple ? [] : ['balance']), 'cost', 'tokens', 'requests', 'total', 'keys', 'performance', 'response'])
const corner = (tile: string) => statCorners(tiles.value.indexOf(tile), tiles.value.length)

const PLATFORM_LABELS: Record<string, string> = {
  anthropic: 'Claude',
  openai: 'OpenAI',
  gemini: 'Gemini',
  antigravity: 'Antigravity',
  grok: 'Grok',
  kimi: 'Kimi',
  zhipu: 'Zhipu GLM',
  deepseek: 'DeepSeek',
  minimax: 'MiniMax',
}

const platformLabel = (p: string) => PLATFORM_LABELS[p] ?? p

const platformCards = computed<FusedPlatformCard[]>(() => {
  // 建立 by_platform Map
  const byPlat = new Map<string, NonNullable<UserStatsType['by_platform']>[number]>()
  for (const item of props.stats?.by_platform ?? []) byPlat.set(item.platform, item)

  // 建立 quota Map
  const byQuota = new Map<string, PlatformQuotaItem>()
  for (const q of props.platformQuotas ?? []) byQuota.set(q.platform, q)

  // Platform windows can differ from totals. Never synthesize spend from their difference.
  const platforms = new Set<string>([...byPlat.keys(), ...byQuota.keys()])

  const PLATFORM_ORDER = ['anthropic', 'openai', 'gemini', 'antigravity', 'grok']
  const cards: FusedPlatformCard[] = []

  for (const p of platforms) {
    const stat = byPlat.get(p)
    cards.push({
      platform: p,
      total_actual_cost: stat?.total_actual_cost ?? 0,
      today_actual_cost: stat?.today_actual_cost ?? 0,
      total_requests: stat?.total_requests ?? 0,
      total_tokens: stat?.total_tokens ?? 0,
      quota: byQuota.get(p),
    })
  }

  // 排序：按 PLATFORM_ORDER，未知平台按名称排序
  cards.sort((a, b) => {
    const ai = PLATFORM_ORDER.indexOf(a.platform)
    const bi = PLATFORM_ORDER.indexOf(b.platform)
    if (ai === -1 && bi === -1) return a.platform.localeCompare(b.platform)
    if (ai === -1) return 1
    if (bi === -1) return -1
    return ai - bi
  })

  return cards
})

// Quota helpers

type QuotaWindow = 'daily' | 'weekly' | 'monthly'
type QuotaField = `${QuotaWindow}_limit_usd` | `${QuotaWindow}_usage_usd` | `${QuotaWindow}_window_resets_at`

function quotaVal(q: PlatformQuotaItem | undefined, key: QuotaField): PlatformQuotaItem[QuotaField] {
  return q?.[key]
}

function hasAnyLimit(q: PlatformQuotaItem | undefined): boolean {
  if (!q) return false
  return q.daily_limit_usd != null || q.weekly_limit_usd != null || q.monthly_limit_usd != null
}

function calcPercent(usage: number, limit: number): number {
  if (!limit || limit <= 0) return 0
  return Math.min(100, Math.max(0, Math.round((usage / limit) * 100)))
}

function quotaBarClass(p: number): string {
  if (p >= 95) return 'bg-red-500'
  if (p >= 75) return 'bg-amber-500'
  return 'bg-green-500'
}

// 与 formatBalance 一致使用 Intl.NumberFormat 做半偶舍入，避免 toFixed 在不同 JS 引擎
// 下偶发截断而非四舍五入（与后端展示精度不一致）。
const usdFormatter = new Intl.NumberFormat('en-US', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})
function formatUsd(n: number): string {
  if (!Number.isFinite(n)) return '0.00'
  return usdFormatter.format(n)
}

function formatResetTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

const formatBalance = (b: number) =>
  new Intl.NumberFormat('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(b)

const formatNumber = (n: number) => n.toLocaleString()
const formatCost = (c: number) => c.toFixed(4)
const formatTokens = (t: number) => {
  if (t >= 1_000_000_000) return `${(t / 1_000_000_000).toFixed(2)}B`
  if (t >= 1_000_000) return `${(t / 1_000_000).toFixed(1)}M`
  if (t >= 1000) return `${(t / 1000).toFixed(1)}K`
  return t.toString()
}
const formatDuration = (ms: number) => ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms.toFixed(0)}ms`
</script>

<style scoped>
/* Stat tiles are DashboardStat (UPageCard); these rules only serve the platform panel. */
.signal-label { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 500; color: var(--ui-text-muted); }
.signal-spend { color: var(--signal-amber, #906727); }
.signal-unit { color: var(--ui-text-dimmed); }

.signal-platform-empty { padding: 40px 0; color: var(--ui-text-muted); font-size: 14px; text-align: center; }
.signal-platform { min-width: 0; padding: 16px 0; border-top: 1px solid var(--ui-border); }
.signal-platform-heading { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 10px; font-size: 13px; font-weight: 600; color: var(--ui-text-highlighted); font-variant-numeric: tabular-nums; }
.signal-platform-name { display: inline-flex; align-items: center; gap: 8px; min-width: 0; overflow-wrap: anywhere; }
.signal-platform-name :deep(svg) { flex-shrink: 0; }
.signal-platform-ledger { margin-top: 12px; }
.signal-platform-ledger > div, .signal-quota-label { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; font-size: 12px; line-height: 1.6; font-variant-numeric: tabular-nums; }
.signal-platform-ledger dt { color: var(--ui-text-muted); }
.signal-platform-ledger dd, .signal-quota-label > span:last-child { min-width: 0; text-align: right; overflow-wrap: anywhere; color: var(--ui-text-highlighted); }
.signal-quota { margin-top: 12px; padding-top: 10px; border-top: 1px solid var(--ui-border); }
.signal-quota-window { margin-top: 8px; }
.signal-quota-label > span:first-child { color: var(--ui-text-muted); }
.signal-quota-track { height: 4px; margin-top: 4px; overflow: hidden; border-radius: 999px; background: var(--ui-bg-accented); }
.signal-quota-fill { height: 100%; border-radius: inherit; }
.signal-quota-fill.bg-green-500 { background: var(--signal-accent, #171717); }
.signal-quota-fill.bg-amber-500 { background: var(--signal-amber, #906727); }
.signal-quota-fill.bg-red-500 { background: var(--signal-danger, #dc4545); }
.signal-quota-label > .signal-disabled { color: var(--signal-danger, #dc4545); }
.signal-reset { margin-top: 4px; font-size: 11px; color: var(--ui-text-muted); }
@media (max-width: 420px) {
  .signal-platform-ledger { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
  .signal-platform-ledger > div { display: block; }
  .signal-platform-ledger dd { text-align: left; }
}
</style>
