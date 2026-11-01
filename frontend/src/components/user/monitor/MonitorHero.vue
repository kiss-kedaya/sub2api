<template>
  <section class="py-3 md:py-4">
    <div class="flex items-center justify-end gap-3 flex-wrap">
      <UFieldGroup role="tablist">
        <UButton
          v-for="opt in windowOptions"
          :key="opt.value"
          color="neutral"
          size="sm"
          role="tab"
          :variant="window === opt.value ? 'subtle' : 'outline'"
          :class="window === opt.value ? 'font-semibold text-highlighted' : 'text-muted'"
          :aria-selected="window === opt.value"
          @click="emit('update:window', opt.value)"
        >
          {{ opt.label }}
        </UButton>
      </UFieldGroup>

      <UBadge :color="overallStatus === 'operational' ? 'success' : 'warning'" variant="subtle" class="uppercase">
        <span class="mr-1 size-1.5 animate-pulse rounded-full bg-current" />
        {{ overallLabel }}
      </UBadge>

      <UButton
        color="neutral"
        variant="ghost"
        icon="i-lucide-refresh-cw"
        :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
        :disabled="loading"
        :title="t('common.refresh')"
        :aria-label="t('common.refresh')"
        @click="emit('refresh')"
      />

      <AutoRefreshButton
        v-if="autoRefresh"
        :enabled="autoRefresh.enabled.value"
        :interval-seconds="autoRefresh.intervalSeconds.value"
        :countdown="autoRefresh.countdown.value"
        :intervals="autoRefresh.intervals"
        @update:enabled="autoRefresh.setEnabled"
        @update:interval="autoRefresh.setInterval"
      />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AutoRefreshButton from '@/components/common/AutoRefreshButton.vue'
export type MonitorWindow = '7d' | '15d' | '30d'
export type OverallStatus = 'operational' | 'degraded'

const props = defineProps<{
  overallStatus: OverallStatus
  intervalSeconds: number
  window: MonitorWindow
  loading: boolean
  autoRefresh?: {
    enabled: { value: boolean }
    intervalSeconds: { value: number }
    countdown: { value: number }
    intervals: readonly number[]
    setEnabled: (v: boolean) => void
    setInterval: (v: number) => void
  }
}>()

const emit = defineEmits<{
  (e: 'update:window', value: MonitorWindow): void
  (e: 'refresh'): void
}>()

const { t } = useI18n()

const windowOptions = computed<{ value: MonitorWindow; label: string }[]>(() => [
  { value: '7d', label: t('channelStatus.windowTab.7d') },
  { value: '15d', label: t('channelStatus.windowTab.15d') },
  { value: '30d', label: t('channelStatus.windowTab.30d') },
])

const overallLabel = computed(() => t(`channelStatus.overall.${props.overallStatus}`))

</script>
