<template>
  <!-- The cell footprint is fixed on purpose. Only a single icon trigger lives in the table cell;
       the plan summary and the execution log moved into the dialog below, so a snapshot that
       arrives late can never widen this column and the table never starts scrolling sideways.
       Two states, no text: tinted while a plan is active, quiet outline while there is none. -->
  <div class="inline-flex shrink-0 align-middle" data-test="timer-cell" @click.stop>
    <UButton
      data-test="timer-entry"
      :data-scheduled="hasSchedule ? 'true' : 'false'"
      :color="hasSchedule ? 'primary' : 'neutral'"
      :variant="hasSchedule ? 'soft' : 'outline'"
      icon="i-lucide-alarm-clock"
      size="xs"
      square
      :disabled="busy || loading"
      :aria-expanded="expanded"
      aria-haspopup="dialog"
      :aria-label="t('admin.accounts.timer.title')"
      :title="triggerTitle"
      @click="openDialog"
    />
    <BaseDialog :show="expanded" :title="t('admin.accounts.timer.title')" width="narrow"
      :close-on-escape="!busy" :close-on-click-outside="!busy" :show-close-button="!busy"
      :draft-notice="timerDraft.restored.value ? t('common.draftRestored') : ''"
      :draft-discard-label="t('common.draftDiscard')"
      @discard-draft="discardTimerDraft" @close="closeDialog">
      <div v-if="expanded" data-test="timer-dialog" class="max-w-full space-y-3 whitespace-normal">
        <!-- Two dense pages: the plan, and what the scheduler already did. -->
        <UFieldGroup class="flex w-full" role="tablist" :aria-label="t('admin.accounts.timer.title')">
          <UButton
            v-for="option in tabOptions"
            :key="option.value"
            role="tab"
            color="neutral"
            :variant="tab === option.value ? 'solid' : 'outline'"
            class="flex-1 justify-center transition-colors duration-150"
            :aria-selected="tab === option.value"
            :data-test="`timer-tab-${option.value}`"
            :label="option.label"
            @click="tab = option.value"
          />
        </UFieldGroup>

        <div v-show="tab === 'plan'" data-test="timer-plan" role="tabpanel" :aria-label="t('admin.accounts.timer.tabPlan')" class="space-y-3">
          <template v-if="!editing">
            <p v-if="planZone" class="text-xs text-dimmed">{{ t('admin.accounts.timer.timezone') }} · {{ planZone }}</p>
            <div data-test="timer-plan-summary" class="space-y-0.5">
              <div v-if="snapshot?.recurring?.enabled" data-test="timer-plan-recurring" class="flex min-h-10 items-center gap-2">
                <span class="min-w-0 flex-1 truncate text-[13px]" :title="recurringSummary">{{ recurringSummary }}</span>
                <UButton data-test="timer-edit-recurring" variant="link" size="xs" class="shrink-0 p-0" :disabled="busy || loading" :label="t('admin.accounts.timer.edit')" @click="begin('recurring')" />
                <UButton data-test="timer-cancel-recurring" color="error" variant="link" size="xs" class="shrink-0 p-0" :disabled="busy || loading" :label="t('admin.accounts.timer.cancel')" @click="cancelRecurring" />
              </div>
              <div v-for="task in pending" :key="task.action" :data-test="`timer-plan-${task.action}`" class="flex min-h-10 items-center gap-2">
                <span class="min-w-0 flex-1 truncate text-[13px]" :title="planTaskLine(task)">{{ planTaskLine(task) }}</span>
                <span class="shrink-0 text-xs tabular-nums text-muted">{{ countdown(task) }}</span>
                <UButton :data-test="`timer-edit-${task.action}`" variant="link" size="xs" class="shrink-0 p-0" :disabled="busy" :label="t('admin.accounts.timer.edit')" @click="begin(task.action)" />
                <UButton :data-test="`timer-cancel-${task.action}`" color="error" variant="link" size="xs" class="shrink-0 p-0" :disabled="busy" :label="t('admin.accounts.timer.cancel')" @click="cancel(task)" />
              </div>
              <p v-if="!hasSchedule" data-test="timer-plan-empty" class="py-2 text-[13px] text-muted">{{ t('admin.accounts.timer.noPlan') }}</p>
            </div>
            <template v-for="task in pending" :key="`${task.action}-reason`">
              <p v-if="task.reason" class="text-[13px] text-warning">{{ reasonText(task.reason) }}</p>
            </template>
            <div v-if="schedulable !== undefined" class="flex min-h-10 items-center gap-3 border-t border-default pt-1">
              <div class="min-w-0 flex-1">
                <p class="truncate text-[13px]">{{ t('admin.accounts.timer.schedulable') }}</p>
                <p class="truncate text-xs text-muted">{{ t('admin.accounts.timer.schedulableHint') }}</p>
              </div>
              <USwitch
                data-test="timer-schedulable"
                :model-value="schedulable"
                :disabled="schedulableBusy"
                size="sm"
                :aria-label="t('admin.accounts.timer.schedulable')"
                @update:model-value="emit('toggle-schedulable')"
              />
            </div>
            <div class="flex flex-wrap gap-2 border-t border-default pt-2">
              <UButton data-test="timer-plan-new" size="xs" color="neutral" variant="outline" :disabled="busy || loading" :label="t('admin.accounts.timer.newPlan')" @click="begin(lastOneShotAction)" />
              <UButton data-test="timer-plan-new-recurring" size="xs" color="neutral" variant="outline" :disabled="busy || loading" :label="t('admin.accounts.timer.recurring')" @click="begin('recurring')" />
            </div>
            <p v-if="error" class="text-[13px] text-error" role="alert">{{ error }}</p>
            <div v-if="loadFailed" class="flex min-h-10 items-center">
              <UButton data-test="timer-reload" variant="link" size="xs" class="p-0" :label="t('admin.accounts.timer.reload')" @click="load" />
            </div>
          </template>
          <div v-else data-test="timer-form" class="max-w-full space-y-4">
            <p class="text-[13px] font-medium text-highlighted">{{ formTitle }}</p>
            <fieldset :disabled="busy" class="min-w-0 space-y-4">
              <UFormField :label="t('admin.accounts.timer.mode')">
                <UFieldGroup class="flex w-full" role="tablist" :aria-label="t('admin.accounts.timer.mode')">
                  <UButton
                    v-for="option in modeOptions"
                    :key="option.value"
                    role="tab"
                    color="neutral"
                    :variant="form.mode === option.value ? 'solid' : 'outline'"
                    class="flex-1 justify-center"
                    :aria-selected="form.mode === option.value"
                    :data-test="`timer-mode-${option.value}`"
                    :label="option.label"
                    @click="setMode(option.value)"
                  />
                </UFieldGroup>
              </UFormField>
              <UFormField v-if="form.mode !== 'recurring'" :label="t('admin.accounts.timer.action')">
                <USelect :model-value="action" :items="actionItems" data-test="timer-action" class="w-full" @update:model-value="selectAction" />
              </UFormField>
              <div v-if="form.mode === 'relative'" class="space-y-2">
                <div class="grid grid-cols-2 gap-2">
                  <UFormField :label="t('admin.accounts.timer.hours')">
                    <UInput v-model.number="form.hours" type="number" min="0" step="1" :max="Math.floor(maxMinutes / 60)" class="w-full" />
                  </UFormField>
                  <UFormField :label="t('admin.accounts.timer.minutes')">
                    <UInput v-model.number="form.minutes" type="number" min="0" step="1" :max="maxMinutes" class="w-full" />
                  </UFormField>
                </div>
                <UButton variant="link" size="xs" class="p-0" :label="t('admin.accounts.timer.preset')" @click="form.hours = 8; form.minutes = 0" />
                <p class="text-xs text-muted">{{ t('admin.accounts.timer.relativeHint') }}</p>
              </div>
              <div v-else-if="form.mode === 'recurring'" class="space-y-4">
                <UFormField :label="t('admin.accounts.timer.activeDays')">
                  <Select :model-value="weekdaysOnly" :options="dayScopeOptions" :aria-label="t('admin.accounts.timer.activeDays')" data-test="timer-day-mode" class="w-full" @update:model-value="updateWeekdaysOnly" />
                </UFormField>
                <div class="space-y-3">
                  <div v-for="(period, index) in recurringPeriods" :key="index" class="grid min-w-0 grid-cols-[1fr_1fr_auto] items-end gap-2">
                    <UFormField :label="t('admin.accounts.timer.start')" class="min-w-0">
                      <UInput v-model="period.start" data-test="timer-period-start" type="time" step="60" class="w-full" />
                    </UFormField>
                    <UFormField :label="t('admin.accounts.timer.end')" class="min-w-0">
                      <UInput v-model="period.end" data-test="timer-period-end" type="time" step="60" class="w-full" />
                    </UFormField>
                    <UButton
                      v-if="recurringPeriods.length > 1"
                      data-test="timer-period-remove"
                      color="neutral"
                      variant="ghost"
                      icon="i-lucide-x"
                      square
                      :aria-label="t('admin.accounts.timer.removePeriod')"
                      :title="t('admin.accounts.timer.removePeriod')"
                      @click="removePeriod(index)"
                    />
                  </div>
                </div>
                <UButton data-test="timer-add-period" variant="link" size="xs" icon="i-lucide-plus" class="p-0" :disabled="recurringPeriods.length >= 24" :label="t('admin.accounts.timer.addPeriod')" @click="addPeriod" />
                <p class="text-xs text-muted">{{ t('admin.accounts.timer.recurringHint') }}</p>
                <p v-if="pending.length" class="text-xs text-warning">{{ t('admin.accounts.timer.replacesOneShotHint') }}</p>
              </div>
              <div v-else class="space-y-3">
                <UFormField :label="t('admin.accounts.timer.date')">
                  <UInput v-model="form.date" type="date" class="w-full" />
                </UFormField>
                <UFormField :label="t('admin.accounts.timer.time')">
                  <UInput v-model="form.time" type="time" step="60" class="w-full" />
                </UFormField>
                <p class="text-xs text-muted">{{ t('admin.accounts.timer.absoluteHint') }}</p>
              </div>
              <UFormField :label="t('admin.accounts.timer.timezone')">
                <Select :model-value="form.timezone" :options="timezoneOptions" :aria-label="t('admin.accounts.timer.timezone')" data-test="timer-zone" searchable creatable class="w-full" @update:model-value="updateTimezone" />
                <div v-if="form.mode !== 'recurring'" class="mt-1.5 flex flex-wrap gap-3">
                  <UButton variant="link" size="xs" class="p-0" :label="t('admin.accounts.timer.beijing')" @click="form.timezone = 'Asia/Shanghai'" />
                  <UButton variant="link" size="xs" class="p-0" label="UTC" @click="form.timezone = 'UTC'" />
                </div>
              </UFormField>
              <UButton data-test="timer-preview" color="neutral" variant="outline" block :disabled="busy" :label="t(form.mode === 'recurring' ? 'admin.accounts.timer.previewNext' : 'admin.accounts.timer.preview')" @click="previewTime" />
              <div v-if="preview" class="space-y-3 text-sm" aria-live="polite">
                <p data-test="timer-preview-value" class="text-highlighted">{{ preview.local_date }} {{ preview.local_time }} · {{ preview.timezone }} · UTC{{ preview.offset }}<span v-if="preview.action"> · {{ t(`admin.accounts.timer.${preview.action}`) }}</span></p>
                <UCheckbox v-if="form.mode === 'absolute'" v-model="confirmed" data-test="timer-confirm" :label="t('admin.accounts.timer.confirm')" />
                <UButton data-test="timer-save" block :disabled="form.mode === 'absolute' && !confirmed" :label="form.mode === 'recurring' ? t('admin.accounts.timer.saveRecurring') : t('admin.accounts.timer.save')" @click="save" />
              </div>
            </fieldset>
            <p v-if="error" class="text-xs text-error" role="alert">{{ error }}</p>
            <p v-if="form.mode !== 'recurring'" class="text-xs text-muted">{{ t('admin.accounts.timer.policy', { seconds: snapshot?.poll_seconds ?? 30, grace: (snapshot?.open_grace_seconds ?? 900) / 60, days: maxMinutes / 1440 }) }}</p>
            <div class="flex justify-end">
              <UButton data-test="timer-form-cancel" color="neutral" variant="subtle" :disabled="busy" :label="t('common.cancel')" @click="closeForm" />
            </div>
          </div>
        </div>

        <div v-show="tab === 'history'" data-test="timer-history" role="tabpanel" :aria-label="t('admin.accounts.timer.tabHistory')" class="space-y-2">
          <p v-if="historyZone" class="text-xs text-dimmed">{{ historyZone }}</p>
          <div data-test="timer-history-rows" :class="historyExpanded ? 'max-h-80 overflow-y-auto pe-1' : ''">
            <!-- One dense line per record; the shared time zone is named once above the list. -->
            <div
              v-for="(item, index) in visibleHistory"
              :key="`${item.action}-${item.execute_at}-${index}`"
              data-test="timer-history-row"
              :class="['min-h-10 border-b border-default/60 py-1 last:border-0', index >= HISTORY_PREVIEW && historyExpanded ? 'timer-history-row-in' : '']"
            >
              <div class="flex min-w-0 items-baseline gap-2">
                <span class="shrink-0 text-xs tabular-nums text-muted">{{ historyWhen(item) }}</span>
                <span class="min-w-0 flex-1 truncate text-[13px]">{{ t(`admin.accounts.timer.${item.action}`) }}</span>
                <span class="shrink-0 text-xs" :class="resultClass(item.result)">{{ t(`admin.accounts.timer.results.${item.result}`) }}</span>
              </div>
              <p v-if="historyNote(item)" class="text-[13px] wrap-break-word" :class="resultClass(item.result) || 'text-dimmed'">{{ historyNote(item) }}</p>
            </div>
            <p v-if="!snapshot?.history.length" data-test="timer-history-empty" class="py-2 text-[13px] text-muted">{{ t('admin.accounts.timer.noHistory') }}</p>
          </div>
          <UButton
            v-if="(snapshot?.history.length ?? 0) > HISTORY_PREVIEW"
            data-test="timer-history-more"
            variant="link"
            size="xs"
            class="p-0"
            :label="historyExpanded ? t('admin.accounts.timer.historyLess') : t('admin.accounts.timer.historyMore', { count: snapshot?.history.length ?? 0 })"
            @click="historyExpanded = !historyExpanded"
          />
        </div>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { useDialogDraft } from '@/composables/useDialogDraft'
import timerZh from './schedulableTimer.zh'
import timerEn from './schedulableTimer.en'
import { schedulableTimersAPI, type SchedulableTimer, type TimerAction, type TimerAudit, type TimerInput, type TimerPreview, type TimerSnapshot } from '@/api/admin/schedulableTimers'
import { COMMON_TIMEZONES, formatTimezoneOffset } from '@/components/admin/channel/types'

const props = defineProps<{
  accountId: number
  refreshKey?: number
  // The account-level scheduling switch, shown on the plan page when the caller owns one.
  schedulable?: boolean
  schedulableBusy?: boolean
}>()
const emit = defineEmits<{
  executed: []
  'schedule-state': [hasSchedule: boolean]
  'toggle-schedulable': []
}>()
const { t } = useI18n({ useScope: 'local', messages: { zh: { admin: { accounts: { timer: timerZh } } }, en: { admin: { accounts: { timer: timerEn } } } } })
const app = useAppStore()
const snapshot = ref<TimerSnapshot | null>(null)
const pending = computed(() => snapshot.value?.tasks.filter(task => task.status === 'pending') ?? [])
const maxMinutes = computed(() => snapshot.value?.max_delay_minutes ?? 366 * 1440)
const expanded = ref(false)
// The dialog has two pages: the plan itself and what the scheduler already did.
const tab = ref<'plan' | 'history'>('plan')
// The plan page shows a summary until the operator edits or creates a schedule.
const editing = ref(false)
const action = ref<TimerAction>('open')
const lastOneShotAction = ref<'open' | 'close'>('open')
const editVersion = ref(0)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const loadFailed = ref(false)
const preview = ref<TimerPreview | null>(null)
const confirmed = ref(false)
const form = reactive<TimerInput>({ mode: 'relative', hours: 8, minutes: 0, date: '', time: '08:00', timezone: 'Asia/Shanghai', weekdays_only: false, periods: [{ start: '08:00', end: '18:00' }] })
const recurringPeriods = ref<Array<{ start: string; end: string }>>([{ start: '08:00', end: '18:00' }])
const weekdaysOnly = ref(false)
const timezoneOptions = COMMON_TIMEZONES.map(value => {
  const offset = formatTimezoneOffset(value)
  return { value, label: offset ? `${value} (${offset})` : value }
})
const dayScopeOptions = computed(() => [
  { value: false, label: t('admin.accounts.timer.everyDay') },
  { value: true, label: t('admin.accounts.timer.weekdaysOnly') },
])
const actionItems = computed(() => [
  { value: 'open', label: t('admin.accounts.timer.open') },
  { value: 'close', label: t('admin.accounts.timer.close') },
])
const tabOptions = computed<Array<{ value: 'plan' | 'history'; label: string }>>(() => [
  { value: 'plan', label: t('admin.accounts.timer.tabPlan') },
  { value: 'history', label: t('admin.accounts.timer.tabHistory') },
])
// The clock trigger is highlighted while a one-shot or recurring timer is active.
const hasSchedule = computed(() => pending.value.length > 0 || !!snapshot.value?.recurring?.enabled)
// History is one compact row per record, capped until the operator asks for all of it.
const HISTORY_PREVIEW = 6
const historyExpanded = ref(false)
const visibleHistory = computed(() => {
  const all = snapshot.value?.history ?? []
  return historyExpanded.value ? all : all.slice(0, HISTORY_PREVIEW)
})
// When every record shares one zone it is named once above the list instead of on each row.
const historyZone = computed(() => {
  const zones = new Set((snapshot.value?.history ?? []).map(item => item.timezone))
  if (zones.size !== 1) return ''
  const [zone] = zones
  const offset = formatTimezoneOffset(zone)
  return offset ? `${zone} (${offset})` : zone
})
// What the trigger tooltip says: the plan in one line, since the icon itself stays wordless.
const triggerTitle = computed(() => {
  const schedule = snapshot.value?.recurring
  if (schedule?.enabled) return `${t('admin.accounts.timer.title')} · ${recurringSummary.value}`
  const next = pending.value[0]
  if (next) return `${t('admin.accounts.timer.title')} · ${t(`admin.accounts.timer.${next.action}`)} · ${taskLocalTime(next)}`
  return t('admin.accounts.timer.title')
})
const formTitle = computed(() => action.value === 'recurring'
  ? t('admin.accounts.timer.recurringPage')
  : t(`admin.accounts.timer.${action.value}`))
const modeOptions = computed<Array<{ value: TimerInput['mode']; label: string }>>(() => [
  { value: 'relative', label: t('admin.accounts.timer.relative') },
  { value: 'absolute', label: t('admin.accounts.timer.absolute') },
  { value: 'recurring', label: t('admin.accounts.timer.recurring') },
])
const planZone = computed(() => {
  const schedule = snapshot.value?.recurring
  if (schedule?.enabled) {
    const offset = formatTimezoneOffset(schedule.timezone)
    return offset ? `${schedule.timezone} (${offset})` : schedule.timezone
  }
  const task = pending.value[0]
  if (!task) return ''
  const offset = formatTimezoneOffset(task.timezone)
  return offset ? `${task.timezone} (${offset})` : task.timezone
})
let epoch = 0
let received = 0
const now = ref(0)
let interval: ReturnType<typeof setInterval> | undefined
let refreshInterval: ReturnType<typeof setInterval> | undefined
let disposed = false
let loadSequence = 0
let previewSequence = 0

watch([form, recurringPeriods, weekdaysOnly], () => { previewSequence++; preview.value = null; confirmed.value = false; error.value = '' }, { deep: true, flush: 'sync' })
watch(() => props.refreshKey, () => { closeDialog(); void load() })
watch(() => props.accountId, () => {
  loadSequence++
  previewSequence++
  busy.value = false
  snapshot.value = null
  preview.value = null
  confirmed.value = false
  expanded.value = false
  editing.value = false
  tab.value = 'plan'
  historyExpanded.value = false
  error.value = ''
  void load()
})
function reasonText(reason: string) { return t(`admin.accounts.timer.errors.${reason}`) }
function showError(err: unknown) {
  const code = (err as { response?: { data?: { reason?: string } } })?.response?.data?.reason?.replace(/^TIMER_/, '')
  const known = ['timezone', 'datetime', 'dst', 'duration', 'period', 'overlap', 'recurring', 'mode', 'past', 'confirmation', 'version', 'conflict', 'request', 'account_deleted', 'account_inactive', 'account_expired', 'open_expired', 'execution_error', 'admin_cancelled', 'manual_override', 'quality_paused', 'manual_pause', 'other_pause', 'oauth_admission_paused', 'temporary_pause', 'overloaded', 'rate_limited', 'invalid_schedule', 'recurring_schedule_replaced', 'one_shot_schedule_override']
  error.value = reasonText(code && known.includes(code) ? code : 'internal')
  app.showError(error.value)
}
async function load() {
  const sequence = ++loadSequence
  loading.value = true
  try {
    const data = await schedulableTimersAPI.list(props.accountId)
    if (disposed || sequence !== loadSequence) return
    const previousRecurring = snapshot.value?.recurring
    const executed = data.tasks.some(task => task.status === 'executed' && snapshot.value?.tasks.some(previous => previous.action === task.action && previous.version === task.version && previous.status === 'pending'))
    const recurringApplied = !!previousRecurring && !!data.recurring?.enabled &&
      previousRecurring.last_applied_at !== data.recurring.last_applied_at && !!data.recurring.last_applied_at
    snapshot.value = data
    // The parent uses this lightweight signal to avoid showing the destructive
    // "立即切换" confirmation when this account has no timer configuration.
    emit('schedule-state', data.tasks.some(task => task.status === 'pending') || !!data.recurring?.enabled)
    if (executed || recurringApplied) emit('executed')
    epoch = Date.parse(data.server_now)
    received = performance.now()
    now.value = epoch
    if (loadFailed.value) error.value = ''
    loadFailed.value = false
  } catch (err) {
    if (disposed || sequence !== loadSequence) return
    loadFailed.value = true
    error.value = reasonText('internal')
  } finally { if (sequence === loadSequence) loading.value = false }
}
function localParts(timestamp: string, timezone: string) {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'longOffset' }).formatToParts(new Date(timestamp))
  const value = (key: string) => parts.find(part => part.type === key)?.value ?? ''
  return { date: `${value('year')}-${value('month')}-${value('day')}`, time: `${value('hour')}:${value('minute')}`, offset: value('timeZoneName').replace('GMT', 'UTC') }
}
function localTime(timestamp: string, timezone: string) {
  try { const local = localParts(timestamp, timezone); return `${local.date} ${local.time} · ${timezone} · ${local.offset}` }
  catch { return `${timestamp} · ${timezone}` }
}
// Rows that only record a change of plan are timed by when the change was made; the rest by the slot they concern.
const RECORD_ONLY_RESULTS = new Set(['scheduled', 'replaced', 'cancelled'])
// A run is only worth a note when it happened clearly later than its slot.
const LATE_NOTE_MINUTES = 2
function compactTime(timestamp: string, timezone: string) {
  try {
    const local = localParts(timestamp, timezone)
    const currentYear = localParts(new Date(now.value || Date.now()).toISOString(), timezone).date.slice(0, 4)
    return local.date.slice(0, 4) === currentYear ? `${local.date.slice(5)} ${local.time}` : `${local.date} ${local.time}`
  } catch { return timestamp }
}
function historyWhen(item: TimerAudit) {
  const when = compactTime(RECORD_ONLY_RESULTS.has(item.result) ? item.actual_at : item.execute_at, item.timezone)
  if (historyZone.value) return when
  return `${when} ${formatTimezoneOffset(item.timezone) || item.timezone}`
}
function historyNote(item: TimerAudit) {
  const parts: string[] = []
  if (item.reason) parts.push(reasonText(item.reason))
  if (RECORD_ONLY_RESULTS.has(item.result)) {
    parts.push(`${t('admin.accounts.timer.plannedAt')} ${compactTime(item.execute_at, item.timezone)}`)
  } else if (!item.reason) {
    const lateMinutes = Math.round((Date.parse(item.actual_at) - Date.parse(item.execute_at)) / 60000)
    if (lateMinutes >= LATE_NOTE_MINUTES) {
      parts.push(t('admin.accounts.timer.lateBy', { time: compactTime(item.actual_at, item.timezone), minutes: lateMinutes }))
    }
  }
  return parts.join(' · ')
}
function resultClass(result: string) {
  if (result === 'failed') return 'text-error'
  if (result === 'skipped' || result === 'pending') return 'text-warning'
  return ''
}
function taskLocalTime(task: SchedulableTimer) {
  return task.local_date && task.local_time && task.offset
    ? `${task.local_date} ${task.local_time} · ${task.timezone} · UTC${task.offset}`
    : localTime(task.execute_at, task.timezone)
}
// One line on the plan page: what will happen, when, in the configured zone.
function planTaskLine(task: SchedulableTimer) {
  return `${t(`admin.accounts.timer.${task.action}`)} · ${taskLocalTime(task)}`
}
const recurringSummary = computed(() => {
  const schedule = snapshot.value?.recurring
  if (!schedule) return ''
  const day = schedule.weekdays_only ? t('admin.accounts.timer.weekdaysOnly') : t('admin.accounts.timer.everyDay')
  const offset = formatTimezoneOffset(schedule.timezone)
  return `${day} · ${(schedule.periods ?? []).map(period => `${period.start}–${period.end}`).join(', ')} · ${schedule.timezone}${offset ? ` (${offset})` : ''}`
})
function countdown(task: SchedulableTimer) {
  const minutes = Math.ceil((Date.parse(task.execute_at) - now.value) / 60000)
  return minutes <= 0 ? t('admin.accounts.timer.awaiting') : t('admin.accounts.timer.remaining', { hours: Math.floor(minutes / 60), minutes: minutes % 60 })
}
// The dialog is the only surface now: the trigger opens it and nothing else is rendered in the cell.
async function openDialog() {
  if (expanded.value) return
  tab.value = 'plan'
  expanded.value = true
  editing.value = false
  historyExpanded.value = false
  await load()
  if (loadFailed.value) return
  // Nothing to summarize yet, so go straight to the form that creates the first plan.
  if (!hasSchedule.value) prepareEditor(lastOneShotAction.value)
}
function prepareEditor(value: TimerAction) {
  tab.value = 'plan'
  action.value = value
  if (value === 'open' || value === 'close') lastOneShotAction.value = value
  const task = snapshot.value?.tasks.find(item => item.action === value)
  editVersion.value = value === 'recurring' ? (snapshot.value?.recurring?.version ?? 0) : (task?.version ?? 0)
  if (value === 'recurring') {
    const schedule = snapshot.value?.recurring
    Object.assign(form, { mode: 'recurring', hours: 0, minutes: 0, date: '', time: '', timezone: schedule?.timezone ?? 'Asia/Shanghai' })
    recurringPeriods.value = schedule?.periods?.map(period => ({ ...period })) ?? [{ start: '08:00', end: '18:00' }]
    weekdaysOnly.value = schedule?.weekdays_only ?? false
    error.value = ''; preview.value = null; confirmed.value = false; editing.value = true; expanded.value = true
    return
  }
  const existing = task?.status === 'pending' ? task : undefined
  if (existing) {
    const local = existing.local_date && existing.local_time
      ? { date: existing.local_date, time: existing.local_time }
      : localParts(existing.execute_at, existing.timezone)
    Object.assign(form, { mode: 'absolute', hours: 0, minutes: 0, date: local.date, time: local.time, timezone: existing.timezone })
  } else Object.assign(form, { mode: 'relative', hours: 8, minutes: 0, date: '', time: '08:00', timezone: 'Asia/Shanghai', weekdays_only: false, periods: [{ start: '08:00', end: '18:00' }] })
  error.value = ''; preview.value = null; confirmed.value = false; editing.value = true; expanded.value = true
}
async function begin(value: TimerAction) {
  await load()
  if (loadFailed.value) return
  prepareEditor(value)
}
function input(): TimerInput {
  return { ...form, hours: form.mode === 'relative' ? form.hours : 0, minutes: form.mode === 'relative' ? form.minutes : 0, date: form.mode === 'absolute' ? form.date : '', time: form.mode === 'absolute' ? form.time : '', weekdays_only: form.mode === 'recurring' ? weekdaysOnly.value : false, periods: form.mode === 'recurring' ? recurringPeriods.value.map(period => ({ ...period })) : [] }
}
function addPeriod() { if (recurringPeriods.value.length < 24) recurringPeriods.value.push({ start: '08:00', end: '18:00' }) }
function removePeriod(index: number) { if (recurringPeriods.value.length > 1) recurringPeriods.value.splice(index, 1) }
function selectAction(value: unknown) {
  if (value !== 'open' && value !== 'close') return
  action.value = value
  onActionChanged()
}
function onActionChanged() {
  if (action.value !== 'open' && action.value !== 'close') return
  lastOneShotAction.value = action.value
  editVersion.value = snapshot.value?.tasks.find(item => item.action === action.value)?.version ?? 0
}
function setMode(mode: TimerInput['mode']) {
  if (form.mode === mode) return
  form.mode = mode
  modeChanged()
}
function modeChanged() {
  if (form.mode === 'recurring') {
    action.value = 'recurring'
    const schedule = snapshot.value?.recurring
    editVersion.value = schedule?.version ?? 0
    if (schedule) {
      recurringPeriods.value = schedule.periods.map(period => ({ ...period }))
      weekdaysOnly.value = schedule.weekdays_only
      form.timezone = schedule.timezone
    }
    return
  }
  if (action.value === 'recurring') {
    action.value = lastOneShotAction.value
    editVersion.value = snapshot.value?.tasks.find(task => task.action === action.value)?.version ?? 0
  }
}
function updateTimezone(value: string | number | boolean | null) { form.timezone = String(value ?? '') }
function updateWeekdaysOnly(value: string | number | boolean | null) { weekdaysOnly.value = value === true }
function validZone(zone: string) {
  if (!zone || zone === 'Local' || (zone !== 'UTC' && !zone.includes('/'))) return false
  try { new Intl.DateTimeFormat('en', { timeZone: zone }).format(); return true } catch { return false }
}
function validateRecurring() {
  if (!validZone(form.timezone)) { error.value = reasonText('timezone'); return false }
  if (recurringPeriods.value.length < 1 || recurringPeriods.value.length > 24) { error.value = reasonText('period'); return false }
  const segments: Array<{ day: number; start: number; end: number }> = []
  for (const period of recurringPeriods.value) {
    const parse = (value: string) => {
      if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(value)) return null
      const [hour, minute] = value.split(':').map(Number)
      return hour * 60 + minute
    }
    const start = parse(period.start), end = parse(period.end)
    if (start == null || end == null || start === end) { error.value = reasonText('period'); return false }
    for (let day = 0; day < 7; day++) {
      if (weekdaysOnly.value && (day === 0 || day === 6)) continue
      if (end > start) segments.push({ day, start, end })
      else {
        segments.push({ day, start, end: 1440 })
        segments.push({ day: (day + 1) % 7, start: 0, end })
      }
    }
  }
  for (let day = 0; day < 7; day++) {
    const daily = segments.filter(segment => segment.day === day).sort((a, b) => a.start - b.start)
    for (let index = 1; index < daily.length; index++) {
      if (daily[index]!.start < daily[index - 1]!.end) { error.value = reasonText('overlap'); return false }
    }
  }
  return true
}
// Leaving the form returns to the plan page; with no plan at all it closes the dialog too.
function closeForm() {
  editing.value = false
  preview.value = null
  confirmed.value = false
  error.value = ''
  if (!hasSchedule.value) expanded.value = false
}
function closeDialog() {
  expanded.value = false
  editing.value = false
  historyExpanded.value = false
}

// The timer dialog closes on an outside click, so a half-filled schedule survives it. The
// key carries the account id, and the signature is the schedule version we started from: a
// timer saved elsewhere in the meantime invalidates the draft instead of overwriting it.
//
// Only one-shot timers are drafted. A recurring schedule keeps its periods and weekday scope
// outside `form` (in `recurringPeriods` and `weekdaysOnly`), so a draft could bring back the
// timezone but not the periods, and the dialog would claim to have restored input it had lost.
const isOneShotDraft = () => action.value !== 'recurring' && form.mode !== 'recurring'
const timerDraft = useDialogDraft({
  key: () => `admin.accounts.timer:${props.accountId}:${action.value}`,
  fields: ['mode', 'hours', 'minutes', 'date', 'time', 'timezone'] as const,
  state: form,
  open: () => editing.value,
  enabled: isOneShotDraft,
  signature: () => `${action.value}:${editVersion.value}`
})

function discardTimerDraft() {
  timerDraft.discard()
  preview.value = null
  confirmed.value = false
  error.value = ''
  Object.assign(form, { mode: 'relative', hours: 8, minutes: 0, date: '', time: '08:00', timezone: 'Asia/Shanghai', weekdays_only: false, periods: [{ start: '08:00', end: '18:00' }] })
  weekdaysOnly.value = false
  recurringPeriods.value = [{ start: '08:00', end: '18:00' }]
}
async function previewTime() {
  const request = input()
  if (request.mode === 'recurring' && !validateRecurring()) return
  if (request.mode === 'relative' && (!Number.isInteger(request.hours) || !Number.isInteger(request.minutes) || request.hours < 0 || request.minutes < 0 || request.hours * 60 + request.minutes <= 0 || request.hours * 60 + request.minutes > maxMinutes.value)) { error.value = reasonText('duration'); return }
  const sequence = ++previewSequence
  busy.value = true; error.value = ''; preview.value = null; confirmed.value = false
  try {
    const result = await schedulableTimersAPI.preview(props.accountId, request)
    if (!disposed && sequence === previewSequence) preview.value = result
  }
  catch (err) { if (!disposed && sequence === previewSequence) showError(err) }
  finally { if (sequence === previewSequence) busy.value = false }
}
async function save() {
  if (!preview.value || (form.mode === 'absolute' && !confirmed.value)) return
  busy.value = true; error.value = ''
  try {
    const request = input()
    if (request.mode === 'absolute') request.date = preview.value.local_date
    await schedulableTimersAPI.save(props.accountId, { ...request, action: action.value, expected_version: editVersion.value, confirmed_at: request.mode === 'absolute' ? preview.value.execute_at : undefined })
    // Saved: the draft must not reappear next time the dialog is opened.
    timerDraft.markSubmitted()
    app.showSuccess(t('admin.accounts.timer.saved')); closeDialog(); await load()
  } catch (err) { showError(err) }
  finally { busy.value = false }
}
async function cancel(task: SchedulableTimer) {
  busy.value = true; error.value = ''
  try { await schedulableTimersAPI.cancel(props.accountId, task.action, task.version); app.showSuccess(t('admin.accounts.timer.cancelled')); editing.value = false; await load() }
  catch (err) { showError(err) }
  finally { busy.value = false }
}
async function cancelRecurring() {
  const schedule = snapshot.value?.recurring
  if (!schedule) return
  busy.value = true; error.value = ''
  try { await schedulableTimersAPI.cancel(props.accountId, 'recurring', schedule.version); app.showSuccess(t('admin.accounts.timer.cancelled')); editing.value = false; await load() }
  catch (err) { showError(err) }
  finally { busy.value = false }
}
onMounted(() => {
  void load()
  interval = setInterval(() => { now.value = epoch + performance.now() - received }, 1000)
  refreshInterval = setInterval(() => { if (!busy.value && document.visibilityState === 'visible') void load() }, 30000)
})
onUnmounted(() => { disposed = true; loadSequence++; previewSequence++; clearInterval(interval); clearInterval(refreshInterval) })
</script>

<style scoped>
/* The rows that appear when more history is asked for rise in over 150ms: enough to read as a
   change, small enough that a dense list never turns into a performance. */
.timer-history-row-in {
  animation: timer-history-row-in 150ms ease-out;
}
@keyframes timer-history-row-in {
  from {
    opacity: 0;
    transform: translateY(2px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .timer-history-row-in {
    animation: none;
  }
}
</style>
