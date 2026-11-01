<template>
  <section
    class="inflight-panel mb-4 min-w-0 overflow-hidden rounded-lg ring"
    :class="open ? 'bg-warning/5 ring-warning/25' : 'bg-default ring-default'"
    data-testid="inflight-panel"
  >
    <div class="flex items-center gap-2 pr-2">
      <UButton
        :id="`${panelId}-heading`"
        type="button"
        color="neutral"
        variant="ghost"
        class="min-w-0 flex-1 justify-start gap-3 rounded-none px-4 py-3 text-left"
        :aria-expanded="open"
        :aria-controls="panelId"
        data-testid="inflight-toggle"
        @click="emit('toggle')"
      >
        <UIcon name="i-lucide-clock" class="size-4 shrink-0 text-warning" aria-hidden="true" />
        <span class="min-w-0 flex-1 text-sm font-semibold text-highlighted">{{ t('usage.inflightFold') }}</span>
        <UBadge v-if="loaded" color="warning" variant="subtle" size="sm" class="shrink-0 tabular-nums" aria-live="polite">
          {{ t('usage.inflightCount', { count: total }) }}
        </UBadge>
        <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-dimmed transition-transform motion-reduce:transition-none" :class="open ? '' : '-rotate-90'" aria-hidden="true" />
      </UButton>
      <UButton
        v-if="open"
        type="button"
        color="neutral"
        variant="ghost"
        size="sm"
        square
        :disabled="loading"
        :title="t('usage.inflightRefresh')"
        :aria-label="t('usage.inflightRefresh')"
        data-testid="inflight-refresh"
        @click="emit('refresh')"
      >
        <UIcon name="i-lucide-refresh-cw" class="size-4" :class="loading ? 'animate-spin motion-reduce:animate-none' : ''" />
      </UButton>
    </div>
    <div v-if="open" :id="panelId" role="region" :aria-labelledby="`${panelId}-heading`" :aria-busy="loading">
      <p class="border-t border-default px-4 py-2 text-xs leading-relaxed text-muted">
        {{ t('usage.inflightRunningHint') }}
      </p>
      <p v-if="error" role="alert" class="mx-4 my-3 rounded-md bg-error/10 px-3 py-2 text-sm text-error">{{ error }}</p>
      <div v-if="shown > 0" class="max-h-128 overflow-auto overscroll-contain p-3 sm:p-0">
        <slot />
      </div>
      <div v-else-if="loading" role="status" class="flex items-center justify-center gap-2 px-4 py-8 text-sm text-muted">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin motion-reduce:animate-none" />
        {{ t('common.loading') }}
      </div>
      <div v-else-if="loaded && !error" class="flex flex-col items-center gap-2 px-4 py-8 text-center" data-testid="inflight-empty">
        <UIcon name="i-lucide-circle-check" class="size-6 text-dimmed" aria-hidden="true" />
        <p class="text-sm text-muted">{{ t('usage.inflightEmpty') }}</p>
      </div>
      <div v-if="shown > 0" class="flex flex-wrap items-center justify-between gap-3 border-t border-default px-4 py-2.5">
        <span class="text-xs tabular-nums text-muted">{{ t('usage.inflightShowing', { shown, total }) }}</span>
        <UButton
          v-if="hasMore"
          type="button"
          color="neutral"
          variant="ghost"
          size="xs"
          trailing-icon="i-lucide-chevron-down"
          :disabled="loading"
          data-testid="inflight-more"
          :label="loading ? t('common.loading') : t('usage.inflightMore')"
          @click="emit('more')"
        />
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'


withDefaults(defineProps<{
  open: boolean
  total: number
  shown: number
  loading?: boolean
  loaded?: boolean
  hasMore?: boolean
  error?: string
}>(), {
  loading: false,
  loaded: false,
  hasMore: false,
  error: '',
})

const emit = defineEmits<{
  toggle: []
  refresh: []
  more: []
}>()
const { t } = useI18n()
const panelId = `inflight-${useId()}`
</script>

<style scoped>
.inflight-panel :deep(thead) {
  position: sticky;
  top: 0;
  z-index: 1;
}
</style>
