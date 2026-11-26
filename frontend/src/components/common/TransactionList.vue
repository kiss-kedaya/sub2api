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
      class="flex items-center gap-3 px-4 py-4 sm:px-5"
    >
      <USkeleton v-if="showIcon" class="size-9 shrink-0 rounded-full" />
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
    :ui="{ root: 'gap-3 px-4 py-10', leading: 'mb-1' }"
    :data-testid="emptyTestId"
  >
    <template v-if="$slots['empty-actions']" #actions>
      <slot name="empty-actions" />
    </template>
  </UEmpty>

  <component
    :is="animateItems ? TransitionGroup : 'ul'"
    v-else
    v-bind="animateItems ? { name: 'tx-list', tag: 'ul', onLeave: completeLeaveImmediately } : {}"
    class="record-divide relative"
    :class="{ 'tx-list-group--moving': hasFreshItems }"
    data-testid="transaction-list"
  >
    <li
      v-for="item in items"
      :key="item.id"
      :data-tone="transactionToneAttribute(item)"
      :data-fresh="item.isNew ? 'true' : undefined"
      :data-testid="item.testId || 'transaction-row'"
      class="tx-list-row flex items-start gap-3 px-4 py-4 sm:px-5"
      :class="{
        'cursor-pointer transition-colors hover:bg-elevated/50': interactive,
        'tx-list-row--fresh': item.isNew
      }"
      :role="interactive ? 'button' : undefined"
      :tabindex="interactive ? 0 : undefined"
      @click="interactive && emit('select', item)"
      @keydown.enter="interactive && emit('select', item)"
    >
      <span
        v-if="showIcon"
        :class="transactionOrbClass(item)"
        class="relative z-[1] mt-0.5"
        aria-hidden="true"
        data-testid="transaction-icon"
      >
        <UIcon :name="transactionIcon(item)" class="size-4" />
      </span>

      <div class="relative z-[1] min-w-0 flex-1">
        <div class="flex items-start justify-between gap-3 sm:gap-4">
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-semibold text-highlighted">{{ item.title }}</p>
            <p v-if="item.subtitle" class="mt-0.5 text-xs leading-relaxed text-muted tabular-nums">
              {{ item.subtitle }}
            </p>
          </div>

          <div class="flex shrink-0 flex-col items-end gap-1.5 pt-0.5">
            <span
              :class="transactionAmountClass(item)"
              class="amount text-[15px] leading-snug tracking-tight"
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

        <slot name="item-details" :item="item" />
        <p v-if="item.meta" class="mt-1.5 break-all font-mono text-xs leading-relaxed text-dimmed">
          {{ item.meta }}
        </p>
      </div>
    </li>
  </component>
</template>

<script setup lang="ts" generic="T extends TransactionItem">
import { computed, TransitionGroup } from 'vue'
import {
  formatTransactionAmount,
  transactionAmountClass,
  transactionIcon,
  transactionOrbClass,
  transactionToneAttribute,
  type TransactionItem
} from './transactionList'

const props = withDefaults(defineProps<{
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
  /** Renders the circular leading type icon plate on each row. */
  showIcon?: boolean
  /** Animates newly added rows popping in and existing rows gliding into place. */
  animateItems?: boolean
}>(), {
  loading: false,
  skeletonRows: 3,
  emptyIcon: 'i-lucide-inbox',
  emptyTitle: '',
  emptyDescription: '',
  emptyTestId: 'transaction-list-empty',
  loadingLabel: '',
  interactive: false,
  showIcon: true,
  animateItems: false
})

const emit = defineEmits<{
  select: [item: T]
}>()

defineSlots<{
  'item-details'?: (props: { item: T }) => unknown
  'empty-actions'?: () => unknown
}>()

// Glide existing rows only while newly arrived items are entering so local
// filter switches remain instantaneous without lagging rows around.
const hasFreshItems = computed(() => props.animateItems && props.items.some(item => item.isNew === true))

// Filter switches and list replacements should remove unmatched rows immediately
// without lingering leave frames, while newly added rows still animate in and
// existing rows smoothly glide into position via `.tx-list-move`.
function completeLeaveImmediately(_el: Element, done: () => void) {
  done()
}
</script>

<style scoped>
.tx-list-row {
  position: relative;
  transition: background-color 160ms ease;
}

@media (hover: hover) and (pointer: fine) {
  .tx-list-row:hover {
    background-color: color-mix(in oklab, var(--ui-bg-elevated) 45%, transparent);
  }
}

/* Keep the 380ms pop-in animation on the row itself and place the 2.2s wash on
   ::after so Vue's TransitionGroup finishes enter cleanly at 380ms instead of
   holding enter-active for the entire glow decay. */
.tx-list-row--fresh {
  animation: tx-row-pop-in 380ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.tx-list-row--fresh::before {
  content: '';
  position: absolute;
  z-index: 1;
  inset-block: 0.625rem;
  inset-inline-start: 0;
  width: 3px;
  border-radius: 0 999px 999px 0;
  background-color: var(--ui-primary);
  animation: tx-row-fresh-bar 2200ms ease-out both;
  pointer-events: none;
}

.tx-list-row--fresh::after {
  content: '';
  position: absolute;
  z-index: 0;
  inset: 0;
  background-color: color-mix(in oklab, var(--ui-primary) 12%, transparent);
  animation: tx-row-fresh-glow 2200ms ease-out both;
  pointer-events: none;
}

.tx-list-group--moving .tx-list-move {
  transition: transform 360ms cubic-bezier(0.22, 1, 0.36, 1);
}

@keyframes tx-row-pop-in {
  0% {
    opacity: 0;
    transform: translateY(-14px) scale(0.99);
  }
  70% {
    opacity: 1;
    transform: translateY(1px) scale(1);
  }
  100% {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@keyframes tx-row-fresh-glow {
  0% {
    opacity: 1;
  }
  35% {
    opacity: 0.72;
  }
  100% {
    opacity: 0;
  }
}

@keyframes tx-row-fresh-bar {
  0%,
  45% {
    opacity: 1;
  }
  100% {
    opacity: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .tx-list-row,
  .tx-list-group--moving .tx-list-move {
    transition: none;
  }
  .tx-list-row--fresh,
  .tx-list-row--fresh::before,
  .tx-list-row--fresh::after {
    animation: none;
  }
}

:global(html.dark) .tx-list-row--fresh::after {
  background-color: color-mix(in oklab, var(--ui-primary) 16%, transparent);
}
</style>
