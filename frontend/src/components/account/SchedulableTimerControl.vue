<template>
  <div class="mb-1 min-w-0 text-left text-xs" @click.stop>
    <div class="flex flex-wrap gap-x-2 gap-y-1">
      <button v-for="value in actions" :key="value" type="button" :data-test="`timer-${value}`"
        class="text-primary-600 hover:underline disabled:opacity-50 dark:text-primary-400"
        :disabled="busy || loading" :aria-expanded="expanded && action === value" @click="begin(value)">
        {{ t(`admin.accounts.timer.${value}`) }}
      </button>
    </div>
    <div v-if="snapshot?.recurring?.enabled" class="mt-2 max-w-80 whitespace-normal break-words text-gray-600 dark:text-dark-300" aria-live="polite">
      <span>{{ recurringSummary }}</span>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="text-primary-600 hover:underline" :disabled="busy || loading" @click="begin('recurring')">{{ t('admin.accounts.timer.edit') }}</button>
        <button type="button" data-test="timer-cancel-recurring" class="text-red-600 hover:underline" :disabled="busy || loading" @click="cancelRecurring">{{ t('admin.accounts.timer.cancel') }}</button>
      </div>
    </div>
    <div v-for="task in pending" :key="task.action" class="mt-1 max-w-80 whitespace-normal break-words text-gray-600 dark:text-dark-300" aria-live="polite">
      <span>{{ t(`admin.accounts.timer.${task.action}`) }} · {{ taskLocalTime(task) }} · {{ countdown(task) }}</span>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="text-primary-600 hover:underline" :disabled="busy" @click="begin(task.action)">{{ t('admin.accounts.timer.edit') }}</button>
        <button type="button" :data-test="`timer-cancel-${task.action}`" class="text-red-600 hover:underline" :disabled="busy" @click="cancel(task)">{{ t('admin.accounts.timer.cancel') }}</button>
      </div>
      <span v-if="task.reason" class="text-amber-600">{{ reasonText(task.reason) }}</span>
    </div>
    <p v-if="error && !expanded" class="mt-1 max-w-80 whitespace-normal text-red-600" role="alert">{{ error }}</p>
    <button v-if="loadFailed" type="button" class="text-primary-600 underline" @click="load">{{ t('admin.accounts.timer.reload') }}</button>
    <BaseDialog :show="expanded" :title="t(`admin.accounts.timer.${action}`)" width="narrow"
      :close-on-escape="!busy" :close-on-click-outside="!busy" :show-close-button="!busy" @close="expanded = false">
    <div v-if="expanded" data-test="timer-form" class="max-w-full space-y-4 whitespace-normal">
      <fieldset :disabled="busy" class="min-w-0 space-y-2">
        <label class="block">{{ t('admin.accounts.timer.mode') }}
          <select v-model="form.mode" data-test="timer-mode" class="input mt-1 w-full text-xs" @change="modeChanged">
            <option value="relative">{{ t('admin.accounts.timer.relative') }}</option>
            <option value="absolute">{{ t('admin.accounts.timer.absolute') }}</option>
            <option value="recurring">{{ t('admin.accounts.timer.recurring') }}</option>
          </select>
        </label>
        <div v-if="form.mode === 'relative'" class="space-y-1">
          <div class="grid grid-cols-2 gap-2">
            <label>{{ t('admin.accounts.timer.hours') }}<input v-model.number="form.hours" type="number" min="0" step="1" :max="Math.floor(maxMinutes / 60)" class="input mt-1 w-full text-xs" /></label>
            <label>{{ t('admin.accounts.timer.minutes') }}<input v-model.number="form.minutes" type="number" min="0" step="1" :max="maxMinutes" class="input mt-1 w-full text-xs" /></label>
          </div>
          <button type="button" class="text-primary-600 underline" @click="form.hours = 8; form.minutes = 0">{{ t('admin.accounts.timer.preset') }}</button>
          <p class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.timer.relativeHint') }}</p>
        </div>
        <div v-else-if="form.mode === 'recurring'" class="space-y-4">
          <div>
            <label class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.timer.activeDays') }}</label>
            <Select :model-value="weekdaysOnly" :options="dayScopeOptions" :aria-label="t('admin.accounts.timer.activeDays')" data-test="timer-day-mode" class="mt-1 w-full" @update:model-value="updateWeekdaysOnly" />
          </div>
          <div class="space-y-3">
            <div v-for="(period, index) in recurringPeriods" :key="index" class="grid min-w-0 grid-cols-2 items-end gap-2">
              <label class="min-w-0">{{ t('admin.accounts.timer.start') }}<input v-model="period.start" data-test="timer-period-start" type="time" step="60" class="input mt-1 w-full text-xs" /></label>
              <label class="min-w-0">{{ t('admin.accounts.timer.end') }}<input v-model="period.end" data-test="timer-period-end" type="time" step="60" class="input mt-1 w-full text-xs" /></label>
              <button v-if="recurringPeriods.length > 1" type="button" data-test="timer-period-remove" class="col-span-2 justify-self-end text-xs text-gray-500 hover:text-red-600 sm:col-span-1 sm:col-start-3" @click="removePeriod(index)">{{ t('admin.accounts.timer.removePeriod') }}</button>
            </div>
          </div>
          <button type="button" data-test="timer-add-period" class="text-primary-600 hover:underline" :disabled="recurringPeriods.length >= 24" @click="addPeriod">+ {{ t('admin.accounts.timer.addPeriod') }}</button>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.timer.recurringHint') }}</p>
          <p v-if="pending.length" class="text-xs text-amber-700 dark:text-amber-400">{{ t('admin.accounts.timer.replacesOneShotHint') }}</p>
        </div>
        <div v-else class="space-y-1">
          <label class="block">{{ t('admin.accounts.timer.date') }}<input v-model="form.date" type="date" class="input mt-1 w-full text-xs" /></label>
          <label class="block">{{ t('admin.accounts.timer.time') }}<input v-model="form.time" type="time" step="60" class="input mt-1 w-full text-xs" /></label>
          <p class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.timer.absoluteHint') }}</p>
        </div>
        <div>
          <label class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.timer.timezone') }}</label>
          <Select :model-value="form.timezone" :options="timezoneOptions" :aria-label="t('admin.accounts.timer.timezone')" data-test="timer-zone" searchable creatable class="mt-1 w-full" @update:model-value="updateTimezone" />
        </div>
        <div v-if="form.mode !== 'recurring'" class="flex flex-wrap gap-2">
          <button type="button" class="text-primary-600 underline" @click="form.timezone = 'Asia/Shanghai'">{{ t('admin.accounts.timer.beijing') }}</button>
          <button type="button" class="text-primary-600 underline" @click="form.timezone = 'UTC'">UTC</button>
        </div>
        <button type="button" data-test="timer-preview" class="btn btn-secondary w-full text-xs" :disabled="busy" @click="previewTime">{{ t(form.mode === 'recurring' ? 'admin.accounts.timer.previewNext' : 'admin.accounts.timer.preview') }}</button>
        <div v-if="preview" class="space-y-3 py-1 text-sm" aria-live="polite">
          <p data-test="timer-preview-value">{{ preview.local_date }} {{ preview.local_time }} · {{ preview.timezone }} · UTC{{ preview.offset }}<span v-if="preview.action"> · {{ t(`admin.accounts.timer.${preview.action}`) }}</span></p>
          <label v-if="form.mode === 'absolute'" class="flex items-start gap-2">
            <input v-model="confirmed" data-test="timer-confirm" type="checkbox" class="mt-0.5" />
            {{ t('admin.accounts.timer.confirm') }}
          </label>
          <button type="button" data-test="timer-save" class="btn btn-primary w-full text-xs" :disabled="form.mode === 'absolute' && !confirmed" @click="save">{{ form.mode === 'recurring' ? t('admin.accounts.timer.saveRecurring') : t('admin.accounts.timer.save') }}</button>
        </div>
      </fieldset>
      <p v-if="error" class="text-xs text-red-600" role="alert">{{ error }}</p>
      <p v-if="form.mode !== 'recurring'" class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.timer.policy', { seconds: snapshot?.poll_seconds ?? 30, grace: (snapshot?.open_grace_seconds ?? 900) / 60, days: maxMinutes / 1440 }) }}</p>
      <div class="flex justify-end">
        <button type="button" data-test="timer-form-cancel" class="text-sm text-gray-500 hover:text-gray-800 dark:hover:text-gray-200" :disabled="busy" @click="closeForm">{{ t('common.cancel') }}</button>
      </div>
    </div>
    </BaseDialog>
    <details v-if="snapshot?.history.length" class="mt-1 max-w-80 whitespace-normal text-gray-500 dark:text-dark-400">
      <summary class="cursor-pointer">{{ t('admin.accounts.timer.history') }} · {{ t(`admin.accounts.timer.results.${snapshot.history[0]?.result}`) }}<span v-if="snapshot.history[0]?.reason"> · {{ reasonText(snapshot.history[0].reason) }}</span></summary>
      <p v-for="(item, index) in snapshot.history" :key="index" class="mt-1 break-words">
        {{ t(`admin.accounts.timer.${item.action}`) }} · {{ localTime(item.execute_at, item.timezone) }}<br />
        {{ t('admin.accounts.timer.actual') }} {{ localTime(item.actual_at, item.timezone) }} · {{ t(`admin.accounts.timer.results.${item.result}`) }}
        <span v-if="item.reason"> · {{ reasonText(item.reason) }}</span>
      </p>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import timerZh from './schedulableTimer.zh'
import timerEn from './schedulableTimer.en'
import { schedulableTimersAPI, type SchedulableTimer, type TimerAction, type TimerInput, type TimerPreview, type TimerSnapshot } from '@/api/admin/schedulableTimers'
import { COMMON_TIMEZONES, formatTimezoneOffset } from '@/components/admin/channel/types'

const props = defineProps<{ accountId: number; refreshKey?: number }>()
const emit = defineEmits<{ executed: [] }>()
const { t } = useI18n({ useScope: 'local', messages: { zh: { admin: { accounts: { timer: timerZh } } }, en: { admin: { accounts: { timer: timerEn } } } } })
const app = useAppStore()
const actions: TimerAction[] = ['open', 'close', 'recurring']
const snapshot = ref<TimerSnapshot | null>(null)
const pending = computed(() => snapshot.value?.tasks.filter(task => task.status === 'pending') ?? [])
const maxMinutes = computed(() => snapshot.value?.max_delay_minutes ?? 366 * 1440)
const expanded = ref(false)
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
let epoch = 0
let received = 0
const now = ref(0)
let interval: ReturnType<typeof setInterval> | undefined
let refreshInterval: ReturnType<typeof setInterval> | undefined
let disposed = false
let loadSequence = 0
let previewSequence = 0

watch([form, recurringPeriods, weekdaysOnly], () => { previewSequence++; preview.value = null; confirmed.value = false; error.value = '' }, { deep: true, flush: 'sync' })
watch(() => props.refreshKey, () => { expanded.value = false; void load() })
watch(() => props.accountId, () => {
  loadSequence++
  previewSequence++
  busy.value = false
  snapshot.value = null
  preview.value = null
  confirmed.value = false
  expanded.value = false
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
function taskLocalTime(task: SchedulableTimer) {
  return task.local_date && task.local_time && task.offset
    ? `${task.local_date} ${task.local_time} · ${task.timezone} · UTC${task.offset}`
    : localTime(task.execute_at, task.timezone)
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
async function begin(value: TimerAction) {
  if (expanded.value && action.value === value) { expanded.value = false; return }
  await load()
  if (loadFailed.value) return
  action.value = value
  if (value === 'open' || value === 'close') lastOneShotAction.value = value
  const task = snapshot.value?.tasks.find(item => item.action === value)
  editVersion.value = value === 'recurring' ? (snapshot.value?.recurring?.version ?? 0) : (task?.version ?? 0)
  if (value === 'recurring') {
    const schedule = snapshot.value?.recurring
    Object.assign(form, { mode: 'recurring', hours: 0, minutes: 0, date: '', time: '', timezone: schedule?.timezone ?? 'Asia/Shanghai' })
    recurringPeriods.value = schedule?.periods?.map(period => ({ ...period })) ?? [{ start: '08:00', end: '18:00' }]
    weekdaysOnly.value = schedule?.weekdays_only ?? false
    error.value = ''; preview.value = null; confirmed.value = false; expanded.value = true
    return
  }
  const existing = task?.status === 'pending' ? task : undefined
  if (existing) {
    const local = existing.local_date && existing.local_time
      ? { date: existing.local_date, time: existing.local_time }
      : localParts(existing.execute_at, existing.timezone)
    Object.assign(form, { mode: 'absolute', hours: 0, minutes: 0, date: local.date, time: local.time, timezone: existing.timezone })
  } else Object.assign(form, { mode: 'relative', hours: 8, minutes: 0, date: '', time: '08:00', timezone: 'Asia/Shanghai', weekdays_only: false, periods: [{ start: '08:00', end: '18:00' }] })
  error.value = ''; preview.value = null; confirmed.value = false; expanded.value = true
}
function input(): TimerInput {
  return { ...form, hours: form.mode === 'relative' ? form.hours : 0, minutes: form.mode === 'relative' ? form.minutes : 0, date: form.mode === 'absolute' ? form.date : '', time: form.mode === 'absolute' ? form.time : '', weekdays_only: form.mode === 'recurring' ? weekdaysOnly.value : false, periods: form.mode === 'recurring' ? recurringPeriods.value.map(period => ({ ...period })) : [] }
}
function addPeriod() { if (recurringPeriods.value.length < 24) recurringPeriods.value.push({ start: '08:00', end: '18:00' }) }
function removePeriod(index: number) { if (recurringPeriods.value.length > 1) recurringPeriods.value.splice(index, 1) }
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
function closeForm() { expanded.value = false; preview.value = null; confirmed.value = false; error.value = '' }
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
    app.showSuccess(t('admin.accounts.timer.saved')); expanded.value = false; await load()
  } catch (err) { showError(err) }
  finally { busy.value = false }
}
async function cancel(task: SchedulableTimer) {
  busy.value = true; error.value = ''
  try { await schedulableTimersAPI.cancel(props.accountId, task.action, task.version); app.showSuccess(t('admin.accounts.timer.cancelled')); expanded.value = false; await load() }
  catch (err) { showError(err) }
  finally { busy.value = false }
}
async function cancelRecurring() {
  const schedule = snapshot.value?.recurring
  if (!schedule) return
  busy.value = true; error.value = ''
  try { await schedulableTimersAPI.cancel(props.accountId, 'recurring', schedule.version); app.showSuccess(t('admin.accounts.timer.cancelled')); expanded.value = false; await load() }
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
