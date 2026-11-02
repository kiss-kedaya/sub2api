<template>
  <section class="inflight-panel mb-4 min-w-0" data-testid="inflight-panel">
    <!-- Collapsed state: one narrow, centred bar. Opening it only affects the panel below. -->
    <!-- pt-1 keeps a hover ring or a focus outline from ever reaching the row above. -->
    <div class="flex justify-center px-1 pt-1" data-testid="inflight-toggle-row">
      <button
        :id="`${panelId}-heading`"
        type="button"
        class="inline-flex max-w-full items-center gap-2 rounded-full border px-3.5 py-1.5 text-xs font-medium transition-[background-color,border-color,color] duration-150 motion-reduce:transition-none hover:ring-1 hover:ring-inset focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-amber-500"
        :class="open
          ? 'border-amber-300 bg-amber-50 text-amber-800 hover:bg-amber-100 hover:ring-amber-200/70 dark:border-amber-700/60 dark:bg-amber-500/10 dark:text-amber-300 dark:hover:ring-amber-700/40'
          : 'border-gray-200 bg-white text-gray-600 hover:border-amber-300 hover:text-amber-700 hover:ring-amber-200/70 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-amber-700/60 dark:hover:text-amber-300 dark:hover:ring-amber-700/40'"
        :aria-expanded="open"
        :aria-controls="panelId"
        data-testid="inflight-toggle"
        @click="emit('toggle')"
      >
        <Icon name="clock" size="xs" aria-hidden="true" />
        <span class="truncate">{{ t('usage.inflightFold') }}</span>
        <span
          v-if="loaded"
          class="shrink-0 rounded-full bg-amber-100 px-1.5 py-0.5 text-[11px] font-semibold tabular-nums text-amber-800 ring-1 ring-inset ring-amber-200/70 dark:bg-amber-500/15 dark:text-amber-300 dark:ring-amber-500/25"
          aria-live="polite"
        >
          {{ t('usage.inflightCount', { count: total }) }}
        </span>
        <Icon
          name="chevronDown"
          size="xs"
          class="shrink-0 transition-transform motion-reduce:transition-none"
          :class="open ? 'rotate-180' : ''"
          aria-hidden="true"
        />
      </button>
    </div>

    <!-- Expanded: full-width panel so the table can use every column. -->
    <div
      v-if="open"
      :id="panelId"
      role="region"
      :aria-labelledby="`${panelId}-heading`"
      :aria-busy="loading"
      class="mt-2 w-full overflow-hidden rounded-xl border border-amber-200 bg-white dark:border-amber-800/60 dark:bg-dark-800"
    >
      <div class="flex items-center justify-between gap-3 border-b border-amber-100 px-4 py-1.5 dark:border-amber-900/40">
        <p class="min-w-0 truncate text-xs leading-relaxed text-gray-500 dark:text-gray-400">
          {{ t('usage.inflightRunningHint') }}
        </p>
        <div class="flex shrink-0 items-center gap-2">
          <span class="text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ t('usage.inflightShowing', { shown, total }) }}</span>
          <button
            type="button"
            class="flex h-7 w-7 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-amber-50 hover:text-amber-700 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-amber-500 disabled:cursor-wait disabled:opacity-50 dark:text-gray-400 dark:hover:bg-amber-500/10 dark:hover:text-amber-300"
            :disabled="loading"
            :title="t('usage.inflightRefresh')"
            :aria-label="t('usage.inflightRefresh')"
            data-testid="inflight-refresh"
            @click="emit('refresh')"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin motion-reduce:animate-none' : ''" />
          </button>
        </div>
      </div>

      <p v-if="error" role="alert" class="mx-4 my-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-300">{{ error }}</p>

      <div v-if="shown > 0" class="max-h-128 w-full overflow-auto overscroll-contain p-3 sm:p-0">
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
