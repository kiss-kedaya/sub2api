<template>
  <!--
    Record-list card: a titled surface around TransactionList.
    Props are data + copy only; the page keeps ownership of i18n and actions.
    Slots: actions (header right), toolbar (above rows, e.g. filters),
    item-details / empty-actions (forwarded to TransactionList), footer (below rows).
  -->
  <UCard
    :data-testid="testId"
    class="shadow-xs dark:shadow-none"
    :ui="{ root: 'transaction-list-card', header: 'p-4 sm:p-5', body: 'p-0', footer: 'px-4 py-3 sm:px-5' }"
  >
    <template #header>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-highlighted">{{ title }}</h2>
          <p v-if="subtitle" class="mt-1 text-xs leading-relaxed text-muted">{{ subtitle }}</p>
        </div>
        <div v-if="$slots.actions" class="flex shrink-0 flex-wrap items-center gap-2">
          <slot name="actions" />
        </div>
      </div>
    </template>

    <div v-if="$slots.toolbar" class="border-b border-default px-4 py-3 sm:px-5">
      <slot name="toolbar" />
    </div>

    <TransactionList
      :items="items"
      :loading="loading"
      :skeleton-rows="skeletonRows"
      :empty-icon="emptyIcon"
      :empty-title="emptyTitle"
      :empty-description="emptyDescription"
      :empty-test-id="emptyTestId"
      :loading-label="loadingLabel"
      :interactive="interactive"
      @select="emit('select', $event)"
    >
      <template v-if="$slots['item-details']" #item-details="slotProps">
        <slot name="item-details" v-bind="slotProps" />
      </template>
      <template v-if="$slots['empty-actions']" #empty-actions>
        <slot name="empty-actions" />
      </template>
    </TransactionList>

    <template v-if="$slots.footer" #footer>
      <slot name="footer" />
    </template>
  </UCard>
</template>

<script setup lang="ts" generic="T extends TransactionItem">
import TransactionList from './TransactionList.vue'
import type { TransactionItem } from './transactionList'

withDefaults(defineProps<{
  title: string
  subtitle?: string
  items: T[]
  loading?: boolean
  skeletonRows?: number
  emptyIcon?: string
  emptyTitle?: string
  emptyDescription?: string
  emptyTestId?: string
  loadingLabel?: string
  /** Makes rows clickable / keyboard selectable and emits `select`. */
  interactive?: boolean
  /** `data-testid` for the card root, used by page specs. */
  testId?: string
}>(), {
  loading: false,
  skeletonRows: 3,
  emptyIcon: 'i-lucide-inbox',
  emptyTitle: '',
  emptyDescription: '',
  emptyTestId: 'transaction-list-empty',
  loadingLabel: '',
  interactive: false,
  testId: 'transaction-list-card'
})

const emit = defineEmits<{
  select: [item: T]
}>()

defineSlots<{
  actions?: () => unknown
  toolbar?: () => unknown
  'item-details'?: (props: { item: T }) => unknown
  'empty-actions'?: () => unknown
  footer?: () => unknown
}>()
</script>
