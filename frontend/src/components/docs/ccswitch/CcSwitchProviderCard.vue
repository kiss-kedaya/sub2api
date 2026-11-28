<template>
  <div
    class="group flex min-h-[64px] items-center gap-3 rounded-xl border px-3 py-2.5 transition-colors sm:px-4"
    :class="[
      current ? toneStyles[current].card : 'border-[var(--ccs-border)] bg-[var(--ccs-card)] hover:border-[var(--app-border-strong)]',
      highlightRing ? 'ccs-spotlight-ring' : '',
    ]"
  >
    <!-- Provider Icon Box -->
    <div
      class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-[var(--ccs-border)]"
      :class="provider.logoOnWhite ? 'bg-white' : 'bg-[var(--ccs-bg)]'"
    >
      <span
        v-if="provider.iconSvg"
        class="inline-flex h-5 w-5 shrink-0 items-center justify-center [&>svg]:block [&>svg]:h-full [&>svg]:w-full"
        :style="{ color: provider.iconColor && provider.iconColor !== 'currentColor' ? provider.iconColor : undefined }"
        role="img"
        :aria-label="provider.name"
        v-html="provider.iconSvg"
      />
      <img
        v-else-if="provider.icon"
        :src="provider.icon"
        :alt="provider.name"
        class="h-5 w-5 rounded-[3px] object-contain"
      />
      <span v-else class="text-sm font-semibold">{{ provider.name.slice(0, 1) }}</span>
    </div>

    <!-- Provider Title, Chips & Subtitle -->
    <div class="min-w-0 flex-1">
      <div class="flex min-w-0 flex-wrap items-center gap-1.5">
        <span class="truncate text-[15px] font-medium leading-tight text-[var(--ccs-fg)]">
          {{ provider.name }}
        </span>
        <span
          v-for="chip in chips"
          :key="chip.label"
          class="inline-block shrink-0 whitespace-nowrap rounded-full border px-1.5 text-[11px] leading-[18px]"
          :class="chip.tone ? toneStyles[chip.tone].chip : 'border-[var(--ccs-border)] text-[var(--ccs-muted-fg)]'"
        >
          {{ chip.label }}
        </span>
        <span
          v-if="provider.modelBadge"
          class="hidden shrink-0 whitespace-nowrap rounded-md bg-[var(--app-subtle)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--app-fg2)] lg:inline-block"
        >
          {{ provider.modelBadge }}
        </span>
      </div>

      <p
        v-if="provider.caption"
        class="mt-1 truncate text-[13px] text-[var(--ccs-muted-fg)]"
      >
        {{ provider.caption[lang] }}
      </p>
      <div v-else class="mt-1 flex min-w-0 items-center gap-2 text-[13px] text-[var(--ccs-muted-fg)]">
        <a
          v-if="resolvedHref"
          :href="resolvedHref"
          target="_blank"
          rel="noopener noreferrer"
          class="truncate transition-colors hover:text-[var(--ccs-fg)] hover:underline"
        >
          {{ provider.subtitle }}
        </a>
        <span v-else class="truncate font-mono text-[12.5px]">{{ provider.subtitle }}</span>
        <span
          v-if="provider.maskedKeyPreview"
          class="hidden shrink-0 rounded bg-[var(--app-subtle)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--app-fg2)] sm:inline-block"
        >
          {{ provider.maskedKeyPreview }}
        </span>
      </div>
    </div>

    <!-- Right Telemetry (Quota Tiers / Balance / Aside) -->
    <span
      v-if="provider.aside"
      class="hidden shrink-0 whitespace-nowrap text-right text-[13px] tabular-nums text-[var(--ccs-muted-fg)] md:block"
    >
      {{ provider.aside[lang] }}
    </span>
    <div
      v-else-if="quotaRows.length > 0"
      class="hidden shrink-0 flex-col items-end gap-0.5 text-right text-[13px] tabular-nums text-[var(--ccs-muted-fg)] md:flex"
    >
      <span v-for="(row, rIdx) in quotaRows" :key="rIdx" class="whitespace-nowrap">
        <template v-for="(tier, tIdx) in row" :key="tier.key">
          <template v-if="tIdx > 0"> · </template>
          <span>{{ tier.before }}</span>
          <span :class="quotaColorClass(tier.tone)">{{ tier.value }}%</span>
          <span>{{ tier.after }}</span>
        </template>
      </span>
    </div>
    <div
      v-else-if="provider.remaining"
      class="hidden shrink-0 text-right text-[13px] tabular-nums text-[var(--ccs-muted-fg)] md:block"
    >
      <span class="whitespace-nowrap">{{ fmt(msg.window.quota.balance, { value: `${provider.remaining} USD` }) }}</span>
    </div>

    <!-- Actions Column -->
    <div class="flex shrink-0 items-center gap-1">
      <div class="flex min-w-[32px] justify-end">
        <span
          v-if="status"
          class="inline-flex items-center gap-1.5 whitespace-nowrap px-1 text-[13px] font-medium text-[var(--ccs-fg)]"
        >
          <span
            aria-hidden="true"
            class="h-1.5 w-1.5 rounded-full"
            :class="status.tone === 'muted' ? 'bg-[var(--ccs-muted-fg)]' : toneStyles[status.tone].dot"
          />
          <span class="hidden sm:inline">{{ status.label }}</span>
        </span>
        <button
          v-else-if="actionButton"
          type="button"
          :data-testid="provider.isSiteProvider ? 'demo-activate-site-provider' : provider.official ? 'demo-activate-official-provider' : undefined"
          :aria-label="actionButton.label"
          :aria-disabled="Boolean(actionButton.disabledReason)"
          class="flex h-8 w-8 items-center justify-center rounded-md transition-colors"
          :class="[
            actionButton.disabledReason
              ? 'cursor-not-allowed text-[var(--ccs-muted-fg)]/40'
              : 'text-[var(--ccs-muted-fg)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)] active:scale-95',
            spotlightAction ? 'ccs-spotlight-ring bg-[var(--app-action)]/12 text-[var(--app-action)]' : '',
          ]"
          @mouseenter="tooltip.show($event, actionButton.disabledReason ? `${actionButton.label}\n${actionButton.disabledReason}` : actionButton.label)"
          @mouseleave="tooltip.hide()"
          @click="handleAction"
        >
          <CcSwitchLucideIcon :name="actionIconName" class-name="h-4 w-4" :stroke-width="1.85" />
        </button>
      </div>

      <button
        type="button"
        :aria-label="msg.window.edit"
        class="hidden h-8 w-8 items-center justify-center rounded-md text-[var(--ccs-muted-fg)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)] sm:flex"
        @mouseenter="tooltip.show($event, msg.window.edit)"
        @mouseleave="tooltip.hide()"
        @click="emit('edit', provider)"
      >
        <CcSwitchLucideIcon name="pencil" class-name="h-4 w-4" :stroke-width="1.75" />
      </button>

      <CcSwitchMenuButton
        large
        :label="msg.window.more"
        width="w-[184px]"
        :items="moreMenuItems"
        class="hidden sm:inline-flex"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip, useCcSwitchWorkbench } from './ccswitchContext'
import { quotaTone } from './ccswitchDemoData'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchMenuButton from './CcSwitchMenuButton.vue'
import type { CcsMenuItem, CcsMode, CcsProviderItem } from './ccswitchTypes'

export interface ProviderCardChip {
  label: string
  tone?: CcsMode
}

export interface ProviderCardAction {
  key: 'switch' | 'exitAndUse' | 'routeHere' | 'add' | 'remove' | 'enable' | 'openTerminal'
  label: string
  disabledReason?: string
  onClick: () => void
}

const props = withDefaults(
  defineProps<{
    provider: CcsProviderItem
    current?: CcsMode
    chips?: ProviderCardChip[]
    status?: { label: string; tone: CcsMode | 'muted' }
    actionButton?: ProviderCardAction
    spotlightAction?: boolean
    highlightRing?: boolean
  }>(),
  {
    chips: () => [],
    spotlightAction: false,
    highlightRing: false,
  },
)

const emit = defineEmits<{
  edit: [provider: CcsProviderItem]
}>()

const { lang, msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()
const workbench = useCcSwitchWorkbench()

const toneStyles: Record<CcsMode, { card: string; dot: string; chip: string }> = {
  direct: {
    card: 'border-[var(--app-direct)] bg-[var(--app-direct-soft)]',
    dot: 'bg-[var(--app-direct)]',
    chip: 'border-[var(--app-direct)] text-[var(--app-direct-text)]',
  },
  route: {
    card: 'border-[var(--app-route)] bg-[var(--app-route-soft)]',
    dot: 'bg-[var(--app-route)]',
    chip: 'border-[var(--app-route)] text-[var(--app-route-text)]',
  },
  stack: {
    card: 'border-[var(--app-stack)] bg-[var(--app-stack-soft)]',
    dot: 'bg-[var(--app-stack)]',
    chip: 'border-[var(--app-stack)] text-[var(--app-stack-text)]',
  },
}

const actionIconMap: Record<ProviderCardAction['key'], string> = {
  switch: 'play',
  exitAndUse: 'shieldCheck',
  routeHere: 'route',
  add: 'plus',
  remove: 'minus',
  enable: 'power',
  openTerminal: 'squareTerminal',
}

const actionIconName = computed(() => (props.actionButton ? actionIconMap[props.actionButton.key] : 'play'))

const resolvedHref = computed(() => {
  if (!props.provider.href) return ''
  if (typeof props.provider.href === 'string') return props.provider.href
  return props.provider.href[lang.value]
})

const tierKeyMap: Record<string, 'fiveHour' | 'weekly' | 'fable'> = {
  '5h': 'fiveHour',
  '7d': 'weekly',
  fable: 'fable',
}

interface FormattedTier {
  key: string
  before: string
  value: number
  after: string
  tone: 'normal' | 'warning' | 'danger'
}

const quotaRows = computed<FormattedTier[][]>(() => {
  const tiers = props.provider.quota?.tiers ?? []
  if (!tiers.length) return []
  const qMsg = msg.value.window.quota
  const isCompactRow = tiers.length > 2
  const mapped: FormattedTier[] = tiers.map((t, idx) => {
    const left = Math.max(0, Math.round(100 - t.utilization))
    const mappedKey = tierKeyMap[t.label]
    const label = mappedKey ? (isCompactRow && idx > 0 ? qMsg.shortTiers[mappedKey] : qMsg.tiers[mappedKey]) : t.label
    const tpl = idx > 0 && isCompactRow ? qMsg.tierShort : qMsg.tierLeft
    const normalizedTpl = /[A-Za-z0-9]$/.test(label) ? tpl.replace(/\{label\}(?=[^\s{])/, '{label} ') : tpl
    const [before = '', rawAfter = ''] = fmt(normalizedTpl, { label }).split('{value}')
    const after = rawAfter.startsWith('%') ? rawAfter.slice(1) : rawAfter
    return {
      key: t.label,
      before,
      value: left,
      after,
      tone: quotaTone(left),
    }
  })
  return mapped.length > 2 ? [[mapped[0]], mapped.slice(1)] : mapped.map((t) => [t])
})

function quotaColorClass(tone: 'normal' | 'warning' | 'danger'): string {
  if (tone === 'danger') return 'text-red-500 dark:text-red-400'
  if (tone === 'warning') return 'text-orange-500 dark:text-orange-400'
  return 'text-green-600 dark:text-green-400'
}

function handleAction() {
  if (!props.actionButton || props.actionButton.disabledReason) return
  props.actionButton.onClick()
}

const moreMenuItems = computed<CcsMenuItem[]>(() => [
  {
    label: msg.value.window.edit,
    icon: 'pencil',
    onSelect: () => emit('edit', props.provider),
  },
  {
    label: msg.value.window.action.openTerminal,
    icon: 'squareTerminal',
    onSelect: () => workbench.toast({ text: `${props.provider.name} · Terminal` }),
  },
])
</script>
