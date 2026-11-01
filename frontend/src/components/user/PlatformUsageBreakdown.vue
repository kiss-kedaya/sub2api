<template>
  <div class="group/usage relative text-sm">
    <div class="flex items-center gap-1.5">
      <span class="text-muted">{{ t('admin.users.today') }}:</span>
      <span class="font-medium tabular-nums text-highlighted">${{ today.toFixed(4) }}</span>
      <UIcon v-if="hasBreakdown" name="i-lucide-info" class="size-3.5 text-dimmed" />
    </div>
    <div class="mt-0.5 flex items-center gap-1.5">
      <span class="text-muted">{{ t('admin.users.total') }}:</span>
      <span class="font-medium tabular-nums text-highlighted">${{ total.toFixed(4) }}</span>
    </div>

    <!-- Hover breakdown, styled like UTooltip (kept in place: the admin users table renders
         many of these cells and a tooltip provider is not guaranteed around them). -->
    <div
      v-if="hasBreakdown"
      class="pointer-events-none absolute left-full top-0 z-50 ml-2 min-w-[220px] whitespace-nowrap rounded-sm bg-default px-2.5 py-2 text-xs text-highlighted opacity-0 shadow-sm ring ring-default transition-opacity duration-100 group-hover/usage:opacity-100"
    >
      <div class="mb-1.5 flex items-center justify-between gap-3 border-b border-default pb-1 text-muted">
        <span>{{ t('admin.users.platformBreakdown') }}</span>
        <span class="font-mono">{{ t('admin.users.today') }} / {{ t('admin.users.total') }}</span>
      </div>
      <div
        v-for="item in sortedBreakdown"
        :key="item.platform"
        class="flex items-center justify-between gap-3 py-0.5"
        :class="{ 'italic text-muted': item.isOther }"
      >
        <span class="capitalize">
          {{ item.isOther ? t('admin.users.platformOther') : platformLabel(item.platform) }}
        </span>
        <span class="font-mono tabular-nums">
          ${{ item.today_actual_cost.toFixed(4) }}
          <span class="text-dimmed">/</span>
          ${{ item.total_actual_cost.toFixed(4) }}
        </span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlatformUsage } from '@/api/admin/dashboard'

const props = defineProps<{
  today: number
  total: number
  byPlatform?: PlatformUsage[]
}>()

const { t } = useI18n()

// 与 UserDashboardStats 保持一致：把"总值 - 各平台之和"的差作为"其他"行展示，
// 避免 tooltip 内各平台费用加总与列首总值对不上。
const OTHER_THRESHOLD = 0.0001

interface BreakdownRow {
  platform: string
  today_actual_cost: number
  total_actual_cost: number
  isOther?: boolean
}

const sortedBreakdown = computed<BreakdownRow[]>(() => {
  const list = props.byPlatform ?? []
  const rows: BreakdownRow[] = [...list]
    .sort((a, b) => b.total_actual_cost - a.total_actual_cost)
    .map((p) => ({ ...p }))

  const sumTotal = rows.reduce((s, r) => s + r.total_actual_cost, 0)
  const sumToday = rows.reduce((s, r) => s + r.today_actual_cost, 0)
  const diffTotal = Math.max(0, props.total - sumTotal)
  const diffToday = Math.max(0, props.today - sumToday)
  if (diffTotal > OTHER_THRESHOLD || diffToday > OTHER_THRESHOLD) {
    rows.push({
      platform: '__other__',
      today_actual_cost: diffToday,
      total_actual_cost: diffTotal,
      isOther: true
    })
  }
  return rows
})

const hasBreakdown = computed(() => sortedBreakdown.value.length > 0)

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

function platformLabel(platform: string): string {
  return PLATFORM_LABELS[platform] ?? platform
}
</script>
