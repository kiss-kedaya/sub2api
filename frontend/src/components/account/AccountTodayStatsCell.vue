<template>
  <div>
    <!-- Loading state -->
    <div v-if="props.loading && !props.stats" class="space-y-1">
      <USkeleton class="h-3 w-12" />
      <USkeleton class="h-3 w-16" />
      <USkeleton class="h-3 w-10" />
    </div>

    <!-- Error state -->
    <div v-else-if="props.error && !props.stats" class="text-xs text-error">
      {{ props.error }}
    </div>

    <!-- Stats data: plain label / value lines -->
    <div v-else-if="props.stats" class="space-y-0.5 text-xs tabular-nums">
      <div class="flex items-center gap-1">
        <span class="text-muted">{{ t('admin.accounts.stats.requests') }}:</span>
        <span class="text-highlighted">{{ formatNumber(props.stats.requests) }}</span>
      </div>
      <div class="flex items-center gap-1">
        <span class="text-muted">{{ t('admin.accounts.stats.tokens') }}:</span>
        <span class="text-highlighted">{{ formatTokens(props.stats.tokens) }}</span>
      </div>
      <div class="flex items-center gap-1">
        <span class="text-muted">{{ t('usage.accountBilled') }}:</span>
        <span class="text-highlighted">{{ formatCurrency(props.stats.cost) }}</span>
      </div>
      <div v-if="props.stats.user_cost != null" class="flex items-center gap-1">
        <span class="text-muted">{{ t('usage.userBilled') }}:</span>
        <span class="text-highlighted">{{ formatCurrency(props.stats.user_cost) }}</span>
      </div>
    </div>

    <!-- No data -->
    <div v-else class="text-xs text-dimmed">-</div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { WindowStats } from '@/types'
import { formatNumber, formatCurrency } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    stats?: WindowStats | null
    loading?: boolean
    error?: string | null
  }>(),
  {
    stats: null,
    loading: false,
    error: null
  }
)

const { t } = useI18n()

// Format large token numbers (e.g., 1234567 -> 1.23M)
const formatTokens = (tokens: number): string => {
  if (tokens >= 1000000) {
    return `${(tokens / 1000000).toFixed(2)}M`
  } else if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(1)}K`
  }
  return tokens.toString()
}
</script>
