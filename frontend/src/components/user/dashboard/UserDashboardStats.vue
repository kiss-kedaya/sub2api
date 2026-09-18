<template>
  <section class="signal-stats" :aria-label="t('dashboard.title')">
    <div v-if="section !== 'platforms'" class="signal-stat-strip" :class="{ 'signal-stat-strip--simple': isSimple }">
      <div v-if="!isSimple" class="signal-metric signal-metric--balance">
        <p class="signal-label"><Icon name="dollar" size="sm" />{{ t('dashboard.balance') }}</p>
        <p class="signal-value">${{ formatBalance(balance) }}</p>
        <p class="signal-detail">{{ t('common.available') }}</p>
      </div>
      <div class="signal-metric signal-metric--cost">
        <p class="signal-label"><Icon name="dollar" size="sm" />{{ t('dashboard.todayCost') }}</p>
        <p class="signal-value" :title="t('dashboard.actual')">${{ formatCost(stats?.today_actual_cost || 0) }}</p>
        <p class="signal-detail"><span>{{ t('dashboard.standard') }}</span> <span>${{ formatCost(stats?.today_cost || 0) }}</span></p>
        <p class="signal-detail signal-detail--wrap">
          <span>{{ t('dashboard.last30Days') }}:</span>
          <span class="signal-spend" :title="t('dashboard.actual')">${{ formatCost(stats?.total_actual_cost || 0) }}</span>
          <span :title="t('dashboard.standard')">/ ${{ formatCost(stats?.total_cost || 0) }}</span>
        </p>
      </div>
      <div class="signal-metric">
        <p class="signal-label"><Icon name="chart" size="sm" />{{ t('dashboard.todayRequests') }}</p>
        <p class="signal-value">{{ stats?.today_requests || 0 }}</p>
        <p class="signal-detail">{{ t('dashboard.last30Days') }}: {{ formatNumber(stats?.total_requests || 0) }}</p>
      </div>
      <div class="signal-metric">
        <p class="signal-label"><Icon name="key" size="sm" />{{ t('dashboard.apiKeys') }}</p>
        <p class="signal-value">{{ stats?.total_api_keys || 0 }}</p>
        <p class="signal-detail signal-active">{{ stats?.active_api_keys || 0 }} {{ t('common.active') }}</p>
      </div>
    </div>

    <div v-if="section !== 'platforms'" class="signal-telemetry">
      <div class="signal-reading">
        <p class="signal-label">{{ t('dashboard.todayTokens') }}</p>
        <p class="signal-reading-value">{{ formatTokens(stats?.today_tokens || 0) }}</p>
        <p class="signal-detail signal-detail--wrap">
          <span>{{ t('dashboard.input') }}: {{ formatTokens(stats?.today_input_tokens || 0) }}</span>
          <span>{{ t('dashboard.output') }}: {{ formatTokens(stats?.today_output_tokens || 0) }}</span>
          <span>{{ t('dashboard.cache') }}: {{ formatTokens((stats?.today_cache_creation_tokens || 0) + (stats?.today_cache_read_tokens || 0)) }}</span>
        </p>
      </div>
      <div class="signal-reading">
        <p class="signal-label">{{ t('dashboard.totalTokens') }}</p>
        <p class="signal-reading-value">{{ formatTokens(stats?.total_tokens || 0) }}</p>
        <p class="signal-detail signal-detail--wrap">
          <span>{{ t('dashboard.input') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
          <span>{{ t('dashboard.output') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
          <span>{{ t('dashboard.cache') }}: {{ formatTokens((stats?.total_cache_creation_tokens || 0) + (stats?.total_cache_read_tokens || 0)) }}</span>
        </p>
      </div>
      <div class="signal-reading">
        <p class="signal-label">{{ t('dashboard.performance') }}</p>
        <p class="signal-reading-value">{{ formatTokens(stats?.rpm || 0) }} <span>RPM</span></p>
        <p class="signal-detail">{{ formatTokens(stats?.tpm || 0) }} TPM</p>
      </div>
      <div class="signal-reading">
        <p class="signal-label">{{ t('dashboard.avgResponse') }}</p>
        <p class="signal-reading-value">{{ formatDuration(stats?.average_duration_ms || 0) }}</p>
        <p class="signal-detail">{{ t('dashboard.averageTime') }}</p>
      </div>
    </div>

    <section v-if="section !== 'summary' && !isSimple && platformCards.length > 0" class="signal-platforms">
      <header class="signal-section-heading">
        <h2>{{ t('dashboard.platformBreakdown') }}</h2>
        <span>{{ t('dashboard.platformCount', { count: sortedPlatforms.length }) }}</span>
      </header>
      <div class="signal-platform-grid">
        <article v-for="item in platformCards" :key="item.platform" class="signal-platform" :class="{ 'signal-platform--other': item.isOther }">
          <header class="signal-platform-heading">
            <span class="signal-platform-name">
              <Icon v-if="item.isOther" name="grid" size="md" />
              <PlatformIcon v-else :platform="item.platform as GroupPlatform" size="lg" />
              <span>{{ item.isOther ? t('dashboard.platformOther') : platformLabel(item.platform) }}</span>
            </span>
            <span class="signal-spend" :title="t('dashboard.actual')">${{ formatCost(item.total_actual_cost) }}</span>
          </header>
          <dl class="signal-platform-ledger">
            <div><dt>{{ t('dashboard.todayCost') }}</dt><dd>${{ formatCost(item.today_actual_cost) }}</dd></div>
            <div><dt>{{ t('dashboard.requests') }}</dt><dd>{{ item.total_requests > 0 ? formatNumber(item.total_requests) : '-' }}</dd></div>
            <div><dt>{{ t('dashboard.tokens') }}</dt><dd>{{ item.total_tokens > 0 ? formatTokens(item.total_tokens) : '-' }}</dd></div>
          </dl>
          <div v-if="hasAnyLimit(item.quota) && !item.isOther" class="signal-quota">
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
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { UserDashboardStats as UserStatsType } from '@/api/usage'
import type { PlatformQuotaItem, GroupPlatform } from '@/types'

interface FusedPlatformCard {
  platform: string
  total_actual_cost: number
  today_actual_cost: number
  total_requests: number
  total_tokens: number
  isOther?: boolean
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

const sortedPlatforms = computed(() => {
  const list = props.stats?.by_platform ?? []
  return [...list].sort((a, b) => b.total_actual_cost - a.total_actual_cost)
})

// 处理"各平台之和 < 总值"的差值：后端按平台聚合时过滤了无法归属平台的行
// （group 与 account 都缺 platform）。这里把差值作为"其他"卡片显式展示，
// 避免 Row 1 总值与 Row 3 平台拆分加总对不上、用户困惑。
const OTHER_THRESHOLD = 0.0001
const platformCards = computed<FusedPlatformCard[]>(() => {
  // 建立 by_platform Map
  const byPlat = new Map<string, (typeof sortedPlatforms.value)[number]>()
  for (const item of props.stats?.by_platform ?? []) byPlat.set(item.platform, item)

  // 建立 quota Map
  const byQuota = new Map<string, PlatformQuotaItem>()
  for (const q of props.platformQuotas ?? []) byQuota.set(q.platform, q)

  // union 平台集合。后端 by_platform / quota 接口均不会返回 platform='__other__'，
  // 无需显式排除；__other__ 由下方差值补差逻辑单独追加。
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

  // __other__ 补差逻辑：只对 by_platform 有 usage 数据的总和计算
  const total = props.stats?.total_actual_cost ?? 0
  const today = props.stats?.today_actual_cost ?? 0
  const sumTotal = cards.reduce((s, c) => s + c.total_actual_cost, 0)
  const sumToday = cards.reduce((s, c) => s + c.today_actual_cost, 0)
  const diffTotal = Math.max(0, total - sumTotal)
  const diffToday = Math.max(0, today - sumToday)

  if (diffTotal > OTHER_THRESHOLD || diffToday > OTHER_THRESHOLD) {
    cards.push({
      platform: '__other__',
      total_actual_cost: diffTotal,
      today_actual_cost: diffToday,
      total_requests: 0,
      total_tokens: 0,
      isOther: true,
    })
  }

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
  if (t >= 1_000_000) return `${(t / 1_000_000).toFixed(1)}M`
  if (t >= 1000) return `${(t / 1000).toFixed(1)}K`
  return t.toString()
}
const formatDuration = (ms: number) => ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms.toFixed(0)}ms`
</script>

<style scoped>
.signal-stats { min-width: 0; }
.signal-platform-empty { padding: 48px 0; color: var(--signal-muted); font-size: 14px; text-align: center; }
.signal-stat-strip, .signal-telemetry {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  border-bottom: 1px solid var(--signal-line, #dce2df);
}
.signal-stat-strip { background: var(--signal-surface, #fff); border-top: 1px solid var(--signal-line, #dce2df); box-shadow: inset 0 1px 0 color-mix(in srgb, var(--signal-accent, #087f68) 12%, transparent); }
.signal-stat-strip--simple { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.signal-metric, .signal-reading { min-width: 0; padding: 20px; }
.signal-metric + .signal-metric, .signal-reading + .signal-reading { border-left: 1px solid var(--signal-line, #dce2df); }
.signal-label { display: flex; align-items: center; gap: 7px; font-size: 12px; font-weight: 500; color: var(--signal-muted, #65716a); }
.signal-value { margin: 10px 0 5px; font-size: 28px; line-height: 1.25; font-weight: 650; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.signal-metric--balance .signal-value, .signal-active { color: var(--signal-accent, #087f68); }
.signal-metric--cost .signal-value, .signal-spend { color: var(--signal-amber, #99640c); }
.signal-detail { margin-top: 4px; font-size: 12px; line-height: 1.6; color: var(--signal-muted, #65716a); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.signal-detail--wrap { display: flex; flex-wrap: wrap; gap: 0 8px; }
.signal-detail.signal-active { color: var(--signal-accent, #087f68); }
.signal-reading { padding-top: 16px; padding-bottom: 16px; }
.signal-reading-value { margin-top: 7px; font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.signal-reading-value span { font-size: 11px; color: var(--signal-muted, #65716a); font-weight: 500; }
.signal-section-heading { display: flex; justify-content: space-between; align-items: baseline; flex-wrap: wrap; gap: 8px; padding: 20px 0 12px; }
.signal-section-heading h2 { font-size: 14px; font-weight: 600; }
.signal-section-heading > span { font-size: 12px; color: var(--signal-muted, #65716a); }
.signal-platform-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 0 24px; }
.signal-platform { min-width: 0; padding: 16px 0; border-top: 1px solid var(--signal-line, #dce2df); }
.signal-platform--other { border-top-style: dashed; }
.signal-platform-heading { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 10px; font-size: 13px; font-weight: 600; font-variant-numeric: tabular-nums; }
.signal-platform-name { display: inline-flex; align-items: center; gap: 8px; min-width: 0; overflow-wrap: anywhere; }
.signal-platform-name :deep(svg) { color: var(--signal-text, #202423); flex-shrink: 0; }
.signal-platform-ledger { margin-top: 12px; }
.signal-platform-ledger > div, .signal-quota-label { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; font-size: 12px; line-height: 1.6; font-variant-numeric: tabular-nums; }
.signal-platform-ledger dt { color: var(--signal-muted, #65716a); }
.signal-platform-ledger dd, .signal-quota-label > span:last-child { text-align: right; overflow-wrap: anywhere; min-width: 0; }
.signal-quota { margin-top: 12px; border-top: 1px solid var(--signal-line, #dce2df); padding-top: 10px; }
.signal-quota-window { margin-top: 8px; }
.signal-quota-track { height: 4px; margin-top: 4px; overflow: hidden; background: var(--signal-line, #dce2df); }
.signal-quota-fill { height: 100%; }
.signal-quota-fill.bg-green-500 { background: var(--signal-accent, #087f68); }
.signal-quota-fill.bg-amber-500 { background: var(--signal-amber, #99640c); }
.signal-quota-fill.bg-red-500 { background: var(--signal-danger, #dc4545); }
.signal-disabled { color: var(--signal-danger, #dc4545); }
.signal-reset { margin-top: 4px; font-size: 11px; color: var(--signal-muted, #65716a); }
@media (max-width: 1100px) {
  .signal-platform-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .signal-metric, .signal-reading { padding: 16px 12px; }
}
@media (max-width: 640px) {
  .signal-stat-strip, .signal-stat-strip--simple, .signal-telemetry { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .signal-metric:nth-child(odd), .signal-reading:nth-child(odd) { border-left: 0; }
  .signal-metric:nth-child(n+3), .signal-reading:nth-child(n+3) { border-top: 1px solid var(--signal-line, #dce2df); }
  .signal-value { font-size: 24px; }
  .signal-platform-grid { gap: 0 16px; }
}
@media (max-width: 420px) {
  .signal-platform-grid { grid-template-columns: minmax(0, 1fr); }
  .signal-platform-ledger { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
  .signal-platform-ledger > div { display: block; }
  .signal-platform-ledger dd { text-align: left; }
}
</style>
