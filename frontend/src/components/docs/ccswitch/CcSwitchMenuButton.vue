<template>
  <span ref="triggerRef" class="inline-flex" @click.stop>
    <button
      type="button"
      :aria-label="label"
      :aria-expanded="open"
      class="inline-flex shrink-0 items-center justify-center rounded-md text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
      :class="[large ? 'h-8 w-8' : 'h-7 w-7', open ? 'bg-[var(--app-selected)] text-[var(--ccs-fg)]' : '']"
      @mouseenter="tooltip.show($event, label)"
      @mouseleave="tooltip.hide()"
      @click="toggle"
    >
      <CcSwitchLucideIcon name="ellipsis" :class-name="large ? 'h-4 w-4' : 'h-[15px] w-[15px]'" :stroke-width="1.6" />
    </button>
    <CcSwitchPopover
      :anchor="triggerRef"
      :open="open"
      :align="align"
      :panel-class="width"
      @close="open = false"
    >
      <div role="menu" class="flex flex-col">
        <template v-for="(item, idx) in items" :key="`${item.label}-${idx}`">
          <div v-if="item.separatorBefore" class="mx-1.5 my-1 h-px bg-[var(--ccs-border)]" />
          <button
            type="button"
            role="menuitem"
            :disabled="item.disabled"
            class="flex min-h-8 w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-start text-[13px]"
            :class="[
              item.disabled ? 'cursor-default text-[var(--app-fg3)]' : 'hover:bg-[var(--app-subtle)]',
              item.danger && !item.disabled ? 'text-[var(--app-danger-text)]' : '',
            ]"
            @click="onSelect(item)"
          >
            <CcSwitchLucideIcon v-if="item.icon" :name="item.icon" class-name="h-3.5 w-3.5 shrink-0" />
            <span class="flex min-w-0 flex-1 flex-col">
              <span class="truncate">{{ item.label }}</span>
              <span v-if="item.hint" class="text-xs text-[var(--app-fg2)]">{{ item.hint }}</span>
            </span>
            <span v-if="item.rightText" class="rounded-full border border-[var(--app-border-strong)] px-1.5 text-[11px] text-[var(--app-fg2)]">
              {{ item.rightText }}
            </span>
            <CcSwitchLucideIcon
              v-if="item.checked !== undefined"
              name="check"
              class-name="h-3.5 w-3.5 shrink-0"
              :class="!item.checked ? 'invisible' : ''"
              :stroke-width="2"
            />
          </button>
        </template>
      </div>
    </CcSwitchPopover>
  </span>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useCcSwitchTooltip } from './ccswitchContext'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchPopover from './CcSwitchPopover.vue'
import type { CcsMenuItem } from './ccswitchTypes'

withDefaults(
  defineProps<{
    label: string
    items: CcsMenuItem[]
    large?: boolean
    width?: string
    align?: 'start' | 'end'
  }>(),
  {
    large: false,
    width: 'w-[220px]',
    align: 'end',
  },
)

const tooltip = useCcSwitchTooltip()
const triggerRef = ref<HTMLElement | null>(null)
const open = ref(false)

function toggle() {
  tooltip.hide()
  open.value = !open.value
}

function onSelect(item: CcsMenuItem) {
  if (item.disabled) return
  item.onSelect?.()
  open.value = false
}
</script>
