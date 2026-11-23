<template>
  <!--
    Bare record list: skeleton → empty → rows. No card chrome, so it can sit
    inside a page card (TransactionListCard) or a dialog (admin balance history).
    Every text prop is passed in by the caller: the list owns layout only, the
    page owns i18n.
  -->
  <ul
    v-if="loading"
    class="record-divide"
    :aria-label="loadingLabel || undefined"
    aria-busy="true"
    data-testid="transaction-list-loading"
  >
    <li
      v-for="row in skeletonRows"
      :key="row"
      class="flex items-center gap-3 px-4 py-3.5 first:pt-0 last:pb-0 sm:px-5"
    >
      <USkeleton class="size-9 shrink-0 rounded-full" />
      <div class="min-w-0 flex-1 space-y-2">
        <USkeleton class="h-3.5 w-2/5" />
        <USkeleton class="h-3 w-3/5" />
      </div>
      <USkeleton class="h-4 w-16 shrink-0" />
    </li>
  </ul>

  <UEmpty
    v-else-if="items.length === 0"
    variant="naked"
    :icon="emptyIcon"
    :title="emptyTitle"
    :description="emptyDescription || undefined"
    class="py-2"
    :ui="{ root: 'gap-3 px-4 py-8', leading: 'mb-1' }"
    :data-testid="emptyTestId"
  >
    <template v-if="$slots['empty-actions']" #actions>
      <slot name="empty-actions" />
    </template>
  </UEmpty>

  <ul v-else class="record-divide" data-testid="transaction-list">
    <li
      v-for="item in items"
      :key="item.id"
      :data-tone="transactionToneAttribute(item)"
      :data-testid="item.testId || 'transaction-row'"
      class="flex items-start gap-3 px-4 py-3.5 first:pt-0 last:pb-0 sm:px-5"
      :class="{ 'cursor-pointer transition-colors hover:bg-elevated/50': interactive }"
      :role="interactive ? 'button' : undefined"
      :tabindex="interactive ? 0 : undefined"
      @click="interactive && emit('select', item)"
      @keydown.enter="interactive && emit('select', item)"
    >
      <span
        :class="transactionOrbClass(item)"
        class="mt-0.5"
        aria-hidden="true"
        data-testid="transaction-icon"
      >
        <UIcon :name="transactionIcon(item)" class="size-4" />
      </span>

      <div class="flex min-w-0 flex-1 items-start justify-between gap-3">
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium text-highlighted">{{ item.title }}</p>
          <p v-if="item.subtitle" class="mt-0.5 text-xs leading-relaxed text-muted">
            {{ item.subtitle }}
          </p>
          <slot name="item-details" :item="item" />
          <p v-if="item.meta" class="mt-1 break-all font-mono text-xs leading-relaxed text-dimmed">
            {{ item.meta }}
          </p>
        </div>

        <div class="flex shrink-0 flex-col items-end gap-1">
          <span
            :class="transactionAmountClass(item)"
            class="amount text-sm"
            data-testid="transaction-amount"
          >{{ formatTransactionAmount(item) ?? '—' }}</span>
          <UBadge
            v-if="item.status"
            :color="item.status.color ?? 'neutral'"
            variant="subtle"
            size="sm"
            :data-testid="item.status.testId"
          >
            {{ item.status.label }}
          </UBadge>
        </div>
      </div>
    </li>
  </ul>
</template>

<script setup lang="ts" generic="T extends TransactionItem">
import {
  formatTransactionAmount,
  transactionAmountClass,
  transactionIcon,
  transactionOrbClass,
  transactionToneAttribute,
  type TransactionItem
} from './transactionList'

withDefaults(defineProps<{
  /** Rows to render, newest first. Empty array shows the empty state. */
  items: T[]
  loading?: boolean
  /** Placeholder rows while loading. */
  skeletonRows?: number
  emptyIcon?: string
  emptyTitle?: string
  emptyDescription?: string
  /** `data-testid` for the empty state, so pages keep their own selector. */
  emptyTestId?: string
  /** Accessible name for the skeleton list while data is in flight. */
  loadingLabel?: string
  /** Makes rows clickable / keyboard selectable and emits `select`. */
  interactive?: boolean
}>(), {
  loading: false,
  skeletonRows: 3,
  emptyIcon: 'i-lucide-inbox',
  emptyTitle: '',
  emptyDescription: '',
  emptyTestId: 'transaction-list-empty',
  loadingLabel: '',
  interactive: false
})

const emit = defineEmits<{
  select: [item: T]
}>()

defineSlots<{
  'item-details'?: (props: { item: T }) => unknown
  'empty-actions'?: () => unknown
}>()
</script>
