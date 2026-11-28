<template>
  <div class="flex h-full min-h-0 flex-col">
    <!-- Reader Header -->
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <button
          type="button"
          :aria-label="reader.back"
          class="-ms-2 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, reader.back)"
          @mouseleave="tooltip.hide()"
          @click="emit('back')"
        >
          <CcSwitchLucideIcon name="arrowLeft" class-name="h-[15px] w-[15px]" :stroke-width="1.6" />
        </button>
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center">
          <CcSwitchAppIcon
            :app="app"
            :size="16"
            badge-class="border-[var(--ccs-bg)] bg-[var(--ccs-bg)]"
          />
        </span>
        <h3 class="truncate text-base font-semibold text-[var(--ccs-fg)]">{{ session.title[lang] }}</h3>
      </div>

      <span class="flex-1" />

      <div class="flex shrink-0 items-center justify-end gap-2">
        <div class="hidden items-center sm:flex">
          <button
            type="button"
            :disabled="!hasPrev"
            :aria-label="reader.prev"
            class="inline-flex h-8 w-8 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)] disabled:opacity-40 disabled:hover:bg-transparent"
            @mouseenter="tooltip.show($event, reader.prev)"
            @mouseleave="tooltip.hide()"
            @click="emit('prev')"
          >
            <CcSwitchLucideIcon name="chevronLeft" class-name="h-4 w-4" :stroke-width="1.6" />
          </button>
          <button
            type="button"
            :disabled="!hasNext"
            :aria-label="reader.next"
            class="inline-flex h-8 w-8 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)] disabled:opacity-40 disabled:hover:bg-transparent"
            @mouseenter="tooltip.show($event, reader.next)"
            @mouseleave="tooltip.hide()"
            @click="emit('next')"
          >
            <CcSwitchLucideIcon name="chevronRight" class-name="h-4 w-4" :stroke-width="1.6" />
          </button>
        </div>

        <div v-if="canResume" ref="resumeAnchorRef" class="flex">
          <button
            type="button"
            class="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-s-md bg-[var(--app-action)] pe-3.5 ps-3 text-[13px] font-semibold text-white transition-colors hover:bg-[var(--app-action-hover)]"
            @click="workbench.toast({ text: fmt(sessionsMsg.launchedIn, { terminal: 'Terminal' }) })"
          >
            <CcSwitchLucideIcon name="play" class-name="h-3.5 w-3.5" :stroke-width="2" />
            <span class="hidden sm:inline">{{ fmt(sessionsMsg.resumeIn, { terminal: 'Terminal' }) }}</span>
          </button>
          <button
            type="button"
            :aria-label="reader.moreResume"
            class="flex h-8 w-8 items-center justify-center rounded-e-md border-s border-white/20 bg-[var(--app-action)] text-white hover:bg-[var(--app-action-hover)]"
            @click="resumeMenuOpen = !resumeMenuOpen"
          >
            <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5" />
          </button>
          <CcSwitchPopover
            :anchor="resumeAnchorRef"
            :open="resumeMenuOpen"
            align="end"
            panel-class="w-[290px]"
            @close="resumeMenuOpen = false"
          >
            <div class="flex flex-col">
              <button
                type="button"
                class="flex min-h-8 w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-start text-[13px] hover:bg-[var(--app-subtle)]"
                @click="workbench.toast({ text: fmt(sessionsMsg.launchedIn, { terminal: 'Terminal' }) }); resumeMenuOpen = false"
              >
                <CcSwitchLucideIcon name="play" class-name="h-3.5 w-3.5" />
                <span>{{ fmt(sessionsMsg.resumeIn, { terminal: 'Terminal' }) }}</span>
              </button>
              <button
                type="button"
                class="flex min-h-8 w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-start text-[13px] hover:bg-[var(--app-subtle)]"
                @click="workbench.toast({ text: sessionsMsg.resumeCommandCopied }); resumeMenuOpen = false"
              >
                <CcSwitchLucideIcon name="copy" class-name="h-3.5 w-3.5" />
                <span>{{ sessionsMsg.copyResumeCommand }}</span>
              </button>
              <button
                type="button"
                class="flex min-h-8 w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-start text-[13px] hover:bg-[var(--app-subtle)]"
                @click="workbench.toast({ text: sessionsMsg.resumeCommandCopied }); resumeMenuOpen = false"
              >
                <CcSwitchLucideIcon name="folder" class-name="h-3.5 w-3.5" />
                <span>{{ reader.copyCdResume }}</span>
              </button>
            </div>
          </CcSwitchPopover>
        </div>
        <button
          v-else
          type="button"
          disabled
          class="inline-flex h-8 items-center gap-1.5 rounded-md bg-[var(--app-action)] px-3.5 text-[13px] font-semibold text-white opacity-50"
        >
          <CcSwitchLucideIcon name="copy" class-name="h-3.5 w-3.5" />
          <span class="hidden sm:inline">{{ sessionsMsg.copyResumeCommand }}</span>
        </button>

        <CcSwitchMenuButton
          large
          :label="msg.window.more"
          width="w-[230px]"
          :items="[
            { label: reader.copyAsMarkdown },
            { label: reader.exportMarkdown },
            { label: sessionsMsg.copySessionId, onSelect: () => workbench.toast({ text: sessionsMsg.sessionIdCopied }) },
            { label: sessionsMsg.copySourcePath, onSelect: () => workbench.toast({ text: sessionsMsg.sourcePathCopied }) },
            { label: reader.reload },
          ]"
        />
      </div>
    </header>

    <!-- Subheader Meta & Filter -->
    <div class="flex shrink-0 flex-col border-b border-[var(--ccs-border)] py-1.5 pe-4 ps-4 sm:ps-6">
      <div class="flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1">
        <div class="flex min-w-0 flex-1 items-center gap-2 text-xs tabular-nums text-[var(--app-fg2)]">
          <span class="hidden min-w-0 items-center gap-1 rounded-md border border-[var(--ccs-border)] bg-[var(--app-subtle)] px-1.5 py-0.5 text-[var(--ccs-fg)] md:inline-flex">
            <span class="truncate font-mono">{{ session.project ?? '~' }}</span>
          </span>
          <span class="whitespace-nowrap">{{ firstTurn?.time }} → {{ lastTurn?.time }}</span>
          <span class="hidden text-[var(--app-fg3)] sm:inline">·</span>
          <span class="hidden whitespace-nowrap sm:inline">
            {{ fmt(reader.questionsCount, { count: session.turns.length }) }} · {{ fmt(reader.toolsCount, { count: allSteps.length }) }}
          </span>
          <span class="hidden truncate font-mono text-[var(--app-fg3)] lg:inline">
            {{ SESSION_MODEL_BY_APP[session.app] ?? '' }}
          </span>
        </div>

        <div class="flex items-center gap-0.5">
          <button
            type="button"
            :aria-label="reader.outlineTitle"
            class="hidden h-8 w-8 items-center justify-center rounded-md text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)] lg:inline-flex"
            :class="outlineOpen ? 'bg-[var(--app-selected)] text-[var(--ccs-fg)]' : ''"
            @mouseenter="tooltip.show($event, reader.outlineTitle)"
            @mouseleave="tooltip.hide()"
            @click="outlineOpen = !outlineOpen"
          >
            <CcSwitchLucideIcon name="panelRight" class-name="h-4 w-4" :stroke-width="1.6" />
          </button>

          <div role="radiogroup" class="mx-1 inline-flex h-7 shrink-0 rounded-lg bg-[var(--app-subtle)] p-[3px]">
            <button
              v-for="f in (['all', 'chat', 'changes'] as const)"
              :key="f"
              type="button"
              role="radio"
              :aria-checked="filter === f"
              class="rounded-[5px] px-2.5 text-xs font-medium transition-colors"
              :class="filter === f ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)] shadow-sm' : 'text-[var(--app-fg2)] hover:text-[var(--ccs-fg)]'"
              @click="filter = f"
            >
              {{ f === 'all' ? reader.filterAll : f === 'chat' ? reader.filterChat : reader.filterChanges }}
            </button>
          </div>
        </div>
      </div>

      <p v-if="!canResume" class="pb-0.5 text-xs text-[var(--app-fg2)]">{{ reader.noResumeHermes }}</p>
    </div>

    <!-- Conversation + Outline -->
    <div class="flex min-h-0 flex-1">
      <div class="min-w-0 flex-1 overflow-y-auto px-4 pb-6 pt-4 sm:px-6">
        <p v-if="filter === 'changes' && !hasAnyEdits" class="py-10 text-center text-[13px] text-[var(--app-fg2)]">
          {{ reader.noChanges }}
        </p>
        <div
          v-for="(turn, idx) in session.turns"
          v-else
          :key="turn.time"
          :ref="(el) => setTurnRef(idx, el)"
          class="mx-auto w-full max-w-[820px]"
        >
          <div v-if="idx > 0" class="py-4">
            <div class="h-px bg-[var(--ccs-border)]" />
          </div>

          <template v-if="!(filter === 'changes' && editSteps(turn).length === 0)">
            <!-- User Question Bubble -->
            <div v-if="filter !== 'changes'" class="flex justify-end pb-3 ps-10 sm:ps-16">
              <div class="flex max-w-[72%] flex-col items-end">
                <span class="h-5 text-xs text-[var(--app-fg3)]">{{ turn.time }}</span>
                <div
                  class="rounded-[16px] rounded-se-[4px] px-3.5 py-2 text-[13px] leading-5 text-[var(--ccs-fg)]"
                  :style="{ background: `color-mix(in srgb, ${agentAccent} 14%, var(--app-subtle))` }"
                >
                  {{ turn.question[lang] }}
                </div>
              </div>
            </div>

            <!-- Agent Response + Timeline -->
            <div class="relative pe-2 ps-10 sm:pe-10">
              <span class="absolute start-0 top-0 flex h-7 w-7 items-center justify-center rounded-full border border-[var(--ccs-border)] bg-[var(--app-surface)]">
                <CcSwitchAppIcon :app="app" :size="16" badge-class="hidden" />
              </span>
              <div class="flex h-7 items-center gap-1.5 px-1.5 text-xs">
                <span class="font-semibold text-[var(--ccs-fg)]">{{ app.label }}</span>
                <span class="text-[var(--app-fg3)]">{{ turn.time }}</span>
                <span v-if="SESSION_MODEL_BY_APP[session.app]" class="hidden truncate font-mono text-[var(--app-fg3)] sm:inline">
                  · {{ SESSION_MODEL_BY_APP[session.app] }}
                </span>
              </div>

              <template v-if="filter !== 'chat' && turn.steps.length > 0">
                <button
                  v-if="filter === 'all'"
                  type="button"
                  :aria-label="reader.expandTimeline"
                  class="flex h-8 max-w-full items-center gap-1 rounded-md px-1.5 text-xs text-[var(--app-fg2)] hover:bg-[var(--app-subtle)]"
                  @click="toggleTurnSteps(idx)"
                >
                  <CcSwitchLucideIcon
                    name="chevronRight"
                    class-name="h-3.5 w-3.5 shrink-0 transition-transform"
                    :class="expandedTurns.includes(idx) ? 'rotate-90' : ''"
                  />
                  <span class="truncate">
                    <span class="font-medium text-[var(--ccs-fg)]">{{ fmt(reader.timelineSummary, { steps: turn.steps.length }) }}</span>
                    <template v-if="runCount(turn) > 0"> · {{ fmt(reader.summaryCommands, { count: runCount(turn) }) }}</template>
                    <template v-if="editCount(turn) > 0"> · {{ fmt(reader.summaryFiles, { count: editCount(turn) }) }}</template>
                    <span v-if="failCount(turn) > 0" class="text-[var(--app-danger-text)]">
                      · {{ fmt(reader.summaryErrors, { count: failCount(turn) }) }}
                    </span>
                    · ⏱ {{ turn.duration }}
                  </span>
                </button>

                <ul v-if="filter === 'changes' || expandedTurns.includes(idx)" class="ms-[13px] border-s border-[var(--ccs-border)] ps-2">
                  <li
                    v-for="(step, sIdx) in visibleSteps(turn)"
                    :key="`${step.kind}-${step.target}-${sIdx}`"
                    class="flex h-8 items-center gap-2 rounded-md px-1.5 text-xs hover:bg-[var(--app-subtle)]"
                  >
                    <span
                      class="w-4 shrink-0 text-center"
                      :class="step.failed ? 'text-[var(--app-danger)]' : session.app === 'claude' ? 'text-[var(--app-success-text)]' : 'text-[var(--app-fg3)]'"
                    >
                      {{ session.app === 'claude' ? '⏺' : '•' }}
                    </span>
                    <span class="min-w-0 flex-1 truncate text-[var(--ccs-fg)]" :class="session.app === 'claude' ? 'font-mono' : ''">
                      <template v-if="session.app === 'claude'">
                        <span class="font-semibold">{{ stepLabel(step.kind, true) }}</span>({{ step.target }})
                      </template>
                      <template v-else>
                        <span class="font-medium">{{ stepLabel(step.kind, false) }}</span> {{ step.target }}
                      </template>
                    </span>
                    <span v-if="step.kind === 'edit'" class="shrink-0 tabular-nums">
                      <span class="text-[var(--app-success-text)]">+{{ step.added }}</span>
                      <span class="ms-1 text-[var(--app-danger-text)]">−{{ step.removed }}</span>
                    </span>
                    <span v-if="step.failed" class="shrink-0 tabular-nums text-[var(--app-danger-text)]">exit 1</span>
                  </li>
                </ul>
              </template>

              <p v-if="filter !== 'changes'" class="px-1.5 pb-1 pt-2 text-[13px] leading-6 text-[var(--ccs-fg)]">
                {{ turn.reply[lang] }}
              </p>
            </div>
          </template>
        </div>
      </div>

      <!-- Right Conversation Outline (240px) -->
      <aside v-if="outlineOpen" class="hidden w-[240px] shrink-0 flex-col border-s border-[var(--ccs-border)] lg:flex">
        <div class="flex h-9 shrink-0 items-center gap-1.5 px-4 text-xs font-semibold text-[var(--app-fg2)]">
          <span>{{ reader.outlineTitle }}</span>
          <span class="font-normal text-[var(--app-fg3)]">{{ session.turns.length }}</span>
        </div>
        <ol class="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
          <li v-for="(turn, idx) in session.turns" :key="turn.time">
            <button
              type="button"
              class="relative w-full rounded-[10px] px-1.5 py-1.5 text-start"
              :class="activeTurn === idx ? 'bg-[var(--app-subtle)]' : ''"
              @click="scrollToTurn(idx)"
            >
              <span
                v-if="activeTurn === idx"
                class="absolute inset-y-2 start-0 w-0.5 rounded-full"
                :style="{ background: agentAccent }"
              />
              <span class="block text-end text-[11px] text-[var(--app-fg3)]">{{ reader.you }} · {{ turn.time }}</span>
              <span
                class="ms-auto mt-0.5 line-clamp-2 block max-w-[200px] rounded-[10px] rounded-se-[3px] px-2 py-1 text-xs text-[var(--ccs-fg)]"
                :style="{ background: `color-mix(in srgb, ${agentAccent} 14%, var(--app-subtle))` }"
              >
                {{ turn.question[lang] }}
              </span>
              <span class="mt-1.5 flex items-start gap-1.5">
                <span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full border border-[var(--ccs-border)] bg-[var(--app-surface)]">
                  <CcSwitchAppIcon :app="app" :size="11" badge-class="hidden" />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block text-[11px] text-[var(--app-fg3)]">
                    {{ fmt(reader.outlineSteps, { count: turn.steps.length }) }}
                    <span
                      v-if="turn.steps.some((s) => s.failed)"
                      class="ms-1 inline-block h-1.5 w-1.5 rounded-full bg-[var(--app-danger)] align-middle"
                    />
                  </span>
                  <span class="line-clamp-2 text-xs text-[var(--app-fg2)]">{{ turn.reply[lang] }}</span>
                </span>
              </span>
            </button>
          </li>
        </ol>
      </aside>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, type ComponentPublicInstance } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip, useCcSwitchWorkbench } from './ccswitchContext'
import { CC_SWITCH_APP_MAP, SESSION_MODEL_BY_APP } from './ccswitchDemoData'
import CcSwitchAppIcon from './CcSwitchAppIcon.vue'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchMenuButton from './CcSwitchMenuButton.vue'
import CcSwitchPopover from './CcSwitchPopover.vue'
import type { CcsSessionItem, CcsSessionTurn } from './ccswitchTypes'

const props = defineProps<{
  session: CcsSessionItem
  hasPrev: boolean
  hasNext: boolean
}>()

const emit = defineEmits<{
  back: []
  prev: []
  next: []
}>()

const { lang, msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()
const workbench = useCcSwitchWorkbench()

const reader = computed(() => msg.value.window.pages.reader)
const sessionsMsg = computed(() => msg.value.window.pages.sessions)
const app = computed(() => CC_SWITCH_APP_MAP[props.session.app] ?? CC_SWITCH_APP_MAP.claude)
const canResume = computed(() => props.session.app !== 'hermes' && props.session.app !== 'openclaw')
const agentAccent = computed(() =>
  ['claude', 'codex', 'gemini', 'opencode', 'pi'].includes(props.session.app)
    ? `var(--app-agent-${props.session.app})`
    : 'var(--app-fg2)',
)

const filter = ref<'all' | 'chat' | 'changes'>('all')
const outlineOpen = ref(true)
const activeTurn = ref(0)
const expandedTurns = ref<number[]>([])
const resumeMenuOpen = ref(false)
const resumeAnchorRef = ref<HTMLElement | null>(null)
const turnRefs = ref<Array<HTMLElement | null>>([])

function setTurnRef(idx: number, el: Element | ComponentPublicInstance | null) {
  turnRefs.value[idx] = el instanceof HTMLElement ? el : null
}

const allSteps = computed(() => props.session.turns.flatMap((t) => t.steps))
const hasAnyEdits = computed(() => allSteps.value.some((s) => s.kind === 'edit'))
const firstTurn = computed(() => props.session.turns[0])
const lastTurn = computed(() => props.session.turns[props.session.turns.length - 1])

function editSteps(turn: CcsSessionTurn) {
  return turn.steps.filter((s) => s.kind === 'edit')
}
function visibleSteps(turn: CcsSessionTurn) {
  return filter.value === 'changes' ? editSteps(turn) : turn.steps
}
function runCount(turn: CcsSessionTurn) {
  return turn.steps.filter((s) => s.kind === 'run').length
}
function editCount(turn: CcsSessionTurn) {
  return new Set(turn.steps.filter((s) => s.kind === 'edit').map((s) => s.target)).size
}
function failCount(turn: CcsSessionTurn) {
  return turn.steps.filter((s) => s.failed).length
}
function toggleTurnSteps(idx: number) {
  expandedTurns.value = expandedTurns.value.includes(idx)
    ? expandedTurns.value.filter((i) => i !== idx)
    : [...expandedTurns.value, idx]
}
function stepLabel(kind: 'read' | 'edit' | 'run', claudeStyle: boolean) {
  if (claudeStyle) return kind === 'read' ? 'Read' : kind === 'run' ? 'Bash' : 'Update'
  return kind === 'read' ? 'Read' : kind === 'run' ? 'Ran' : 'Edited'
}
function scrollToTurn(idx: number) {
  activeTurn.value = idx
  turnRefs.value[idx]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
</script>
