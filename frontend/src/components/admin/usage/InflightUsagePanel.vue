<template>
  <section
    class="inflight-panel mb-4 min-w-0 overflow-hidden rounded-xl border bg-white dark:bg-dark-800"
    :class="open ? 'border-amber-200 dark:border-amber-800/60' : 'border-gray-200 dark:border-dark-600'"
    data-testid="inflight-panel"
  >
    <div class="flex items-center gap-2 pr-3" :class="open ? 'bg-amber-50/60 dark:bg-amber-950/20' : ''">
      <button
        :id="`${panelId}-heading`"
        type="button"
        class="flex min-w-0 flex-1 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-amber-50/70 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-amber-500 dark:hover:bg-amber-950/30"
        :aria-expanded="open"
        :aria-controls="panelId"
        data-testid="inflight-toggle"
        @click="emit('toggle')"
      >
        <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-400" aria-hidden="true">
          <Icon name="clock" size="sm" />
        </span>
        <span class="min-w-0 flex-1 text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('usage.inflightFold') }}</span>
        <span v-if="loaded" class="shrink-0 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold tabular-nums text-amber-800 dark:bg-amber-500/15 dark:text-amber-300" aria-live="polite">
          {{ t('usage.inflightCount', { count: total }) }}
        </span>
        <Icon name="chevronDown" size="sm" class="shrink-0 text-gray-400 transition-transform motion-reduce:transition-none" :class="open ? '' : '-rotate-90'" aria-hidden="true" />
      </button>
      <button
        v-if="open"
        type="button"
        class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-white hover:text-amber-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-amber-500 disabled:cursor-wait disabled:opacity-50 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-amber-300"
        :disabled="loading"
        :title="t('usage.inflightRefresh')"
        :aria-label="t('usage.inflightRefresh')"
        data-testid="inflight-refresh"
        @click="emit('refresh')"
      >
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin motion-reduce:animate-none' : ''" />
      </button>
    </div>
    <div v-if="open" :id="panelId" role="region" :aria-labelledby="`${panelId}-heading`" :aria-busy="loading">
      <p class="border-t border-amber-100 px-4 py-2 text-xs leading-relaxed text-gray-500 dark:border-amber-900/40 dark:text-gray-400">
        {{ t('usage.inflightRunningHint') }}
      </p>
      <p v-if="error" role="alert" class="mx-4 my-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-300">{{ error }}</p>
      <div v-if="shown > 0" class="max-h-[32rem] overflow-auto overscroll-contain p-3 sm:p-0">
        <slot />
      </div>
      <div v-else-if="loading" role="status" class="flex items-center justify-center gap-2 px-4 py-8 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="refresh" size="sm" class="animate-spin motion-reduce:animate-none" />
        {{ t('common.loading') }}
      </div>
      <div v-else-if="loaded && !error" class="flex flex-col items-center gap-2 px-4 py-8 text-center" data-testid="inflight-empty">
        <Icon name="checkCircle" size="lg" class="text-gray-300 dark:text-gray-600" aria-hidden="true" />
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('usage.inflightEmpty') }}</p>
      </div>
      <div v-if="shown > 0" class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 px-4 py-2.5 dark:border-dark-700">
        <span class="text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ t('usage.inflightShowing', { shown, total }) }}</span>
        <button
          v-if="hasMore"
          type="button"
          class="inline-flex items-center gap-1 rounded-lg px-2 py-1 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-50 disabled:cursor-wait disabled:opacity-50 dark:text-amber-400 dark:hover:bg-amber-500/10"
          :disabled="loading"
          data-testid="inflight-more"
          @click="emit('more')"
        >
          {{ loading ? t('common.loading') : t('usage.inflightMore') }}
          <Icon name="chevronDown" size="xs" aria-hidden="true" />
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

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
