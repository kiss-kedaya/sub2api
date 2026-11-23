<template>
  <section class="overview-status" :aria-label="t('admin.crypto.overview.statusTitle')">
    <UPageCard variant="subtle" :ui="{ container: 'gap-y-3' }">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-highlighted">{{ t('admin.crypto.overview.statusTitle') }}</h2>
          <p class="mt-1 text-sm text-muted">{{ t('admin.crypto.overview.statusHint') }}</p>
        </div>
        <!-- One refresh for the whole page: status sources and statistics move together. -->
        <UButton
          color="neutral"
          variant="outline"
          size="sm"
          icon="i-lucide-refresh-cw"
          data-testid="crypto-overview-refresh"
          :loading="refreshing"
          :disabled="refreshing"
          @click="emit('refresh')"
        >{{ refreshing ? t('common.loading') : t('admin.crypto.overview.refreshAll') }}</UButton>
      </div>
      <div class="grid gap-2 sm:grid-cols-3" data-testid="crypto-overview-status">
        <div
          v-for="segment in segments"
          :key="segment.id"
          class="overview-status__segment flex min-w-0 flex-col gap-1 rounded-lg border p-3"
          :class="segment.ok ? 'border-default bg-success/5' : 'border-error/40 bg-error/5'"
          :data-testid="`overview-status-${segment.id}`"
          :data-state="segment.ok ? 'ok' : 'error'"
        >
          <div class="flex min-w-0 items-center gap-2">
            <UIcon
              :name="segment.ok ? 'i-lucide-circle-check' : 'i-lucide-circle-alert'"
              class="size-4 shrink-0"
              :class="segment.ok ? 'text-success' : 'text-error'"
            />
            <span class="truncate text-sm font-medium text-highlighted">{{ t(segment.titleKey) }}</span>
            <UBadge :color="segment.ok ? 'success' : 'error'" variant="subtle" size="sm">{{ t(segment.ok ? 'admin.crypto.overview.available' : 'admin.crypto.overview.abnormal') }}</UBadge>
          </div>
          <p class="text-xs leading-relaxed text-muted">{{ t(segment.hintKey) }}</p>
          <UButton
            v-if="!segment.ok && segment.actionLabel"
            color="error"
            variant="soft"
            size="xs"
            class="mt-1 w-fit"
            :data-testid="`overview-status-${segment.id}-action`"
            @click="emit('segment-action', segment.id)"
          >{{ t(segment.actionLabel) }}</UButton>
        </div>
      </div>
    </UPageCard>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { OverviewStatusSegment } from '@/components/crypto/overviewStatus'

defineProps<{
  segments: OverviewStatusSegment[]
  refreshing?: boolean
}>()

const emit = defineEmits<{
  (event: 'refresh'): void
  (event: 'segment-action', id: OverviewStatusSegment['id']): void
}>()

const { t } = useI18n()
</script>
