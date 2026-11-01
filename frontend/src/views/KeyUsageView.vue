<template>
  <div class="flex min-h-screen flex-col bg-default text-default">
    <PublicHeader :site-name="siteName" :logo="siteLogo" brand-to="/home">
      <template #actions>
        <LocaleSwitcher />
        <UButton
          v-if="docUrl"
          :href="docUrl"
          external
          target="_blank"
          icon="i-lucide-book-open"
          color="neutral"
          variant="ghost"
          square
          :aria-label="t('home.viewDocs')"
          :title="t('home.viewDocs')"
        />
        <ThemeToggleButton />
      </template>
    </PublicHeader>

    <main class="flex-1">
      <UPageHero
        :title="t('keyUsage.title')"
        :description="t('keyUsage.subtitle')"
        :ui="{
          container: 'max-w-5xl py-14 sm:py-16 lg:py-20 gap-10 sm:gap-y-10',
          title: 'text-3xl sm:text-4xl',
          description: 'mx-auto mt-3 max-w-md text-base',
          body: 'mt-8'
        }"
      >
        <template #body>
          <div class="mx-auto w-full max-w-xl text-left">
            <div class="flex gap-2">
              <UInput
                v-model="apiKey"
                :type="keyVisible ? 'text' : 'password'"
                :placeholder="t('keyUsage.placeholder')"
                icon="i-lucide-lock"
                size="xl"
                class="min-w-0 flex-1"
                autocomplete="off"
                @keydown.enter="queryKey"
              >
                <template #trailing>
                  <UButton
                    :icon="keyVisible ? 'i-lucide-eye' : 'i-lucide-eye-off'"
                    color="neutral"
                    variant="link"
                    size="sm"
                    :aria-label="keyVisible ? t('keyUsage.hideKey') : t('keyUsage.showKey')"
                    :title="keyVisible ? t('keyUsage.hideKey') : t('keyUsage.showKey')"
                    @click="keyVisible = !keyVisible"
                  />
                </template>
              </UInput>
              <UButton
                :loading="isQuerying"
                :disabled="isQuerying"
                icon="i-lucide-search"
                :label="isQuerying ? t('keyUsage.querying') : t('keyUsage.query')"
                size="xl"
                class="shrink-0"
                data-testid="key-usage-query"
                @click="queryKey"
              />
            </div>
            <p class="mt-3 text-center text-xs text-dimmed">{{ t('keyUsage.privacyNote') }}</p>

            <!-- Date Range Picker -->
            <div v-if="showDatePicker" class="mt-5 flex flex-wrap items-center justify-center gap-2">
              <span class="text-xs text-muted">{{ t('keyUsage.dateRange') }}</span>
              <UTabs
                :model-value="currentRange"
                :items="dateRangeItems"
                :content="false"
                size="xs"
                class="w-auto"
                :ui="{ list: 'w-auto' }"
                @update:model-value="setDateRange($event as DateRangeKey)"
              />
              <div v-if="currentRange === 'custom'" class="flex items-center gap-2">
                <UInput v-model="customStartDate" type="date" size="sm" :aria-label="t('dates.startDate')" />
                <span class="text-xs text-dimmed">-</span>
                <UInput v-model="customEndDate" type="date" size="sm" :aria-label="t('dates.endDate')" />
                <UButton :label="t('keyUsage.apply')" size="sm" @click="queryKey" />
              </div>
            </div>
          </div>
        </template>
      </UPageHero>

      <!-- Results -->
      <UContainer v-if="showResults" class="max-w-5xl pb-20">
        <!-- Loading -->
        <div v-if="showLoading" class="space-y-6">
          <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
            <USkeleton v-for="n in 3" :key="n" class="h-36 rounded-lg" />
          </div>
          <USkeleton class="h-56 rounded-lg" />
        </div>

        <div v-else-if="resultData" class="space-y-10">
          <!-- Status -->
          <div v-if="statusInfo" class="fade-up flex justify-center">
            <UBadge
              :color="statusInfo.isActive ? 'success' : 'error'"
              variant="subtle"
              size="lg"
              class="gap-2 rounded-full px-3"
            >
              <span class="size-2 rounded-full" :class="statusInfo.isActive ? 'bg-success' : 'bg-error'"></span>
              <span class="font-medium">{{ statusInfo.label }}</span>
              <span class="opacity-60">·</span>
              <span>{{ statusInfo.statusText }}</span>
            </UBadge>
          </div>

          <!-- Quota cards -->
          <UPageGrid v-if="ringItems.length > 0" :class="ringGridClass">
            <UPageCard
              v-for="(ring, i) in ringItems"
              :key="i"
              :icon="RING_ICONS[ring.iconType]"
              :title="ring.title"
              variant="subtle"
              class="fade-up"
              :class="`fade-up-delay-${Math.min(i + 1, 4)}`"
              :ui="{
                container: 'gap-y-3',
                leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
                title: 'font-normal text-muted text-xs uppercase'
              }"
            >
              <template v-if="ring.isBalance">
                <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ ring.amount }}</span>
              </template>
              <template v-else>
                <div class="flex items-baseline justify-between gap-3">
                  <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ displayPcts[i] ?? 0 }}%</span>
                  <span class="text-xs text-muted">{{ t('keyUsage.used') }}</span>
                </div>
                <UProgress :model-value="displayPcts[i] ?? 0" :max="100" size="sm" class="mt-2" />
                <div class="mt-2 flex items-center justify-between gap-3 text-xs tabular-nums">
                  <span class="font-medium text-toned">{{ ring.amount }}</span>
                  <span
                    v-if="ring.resetAt && formatResetTime(ring.resetAt)"
                    class="inline-flex items-center gap-1 text-dimmed"
                  >
                    <UIcon name="i-lucide-rotate-cw" class="size-3" />
                    {{ formatResetTime(ring.resetAt) }}
                  </span>
                </div>
              </template>
            </UPageCard>
          </UPageGrid>

          <!-- Detail -->
          <section v-if="detailRows.length > 0" class="fade-up fade-up-delay-3">
            <h2 class="mb-3 text-base font-semibold text-highlighted">{{ t('keyUsage.detailInfo') }}</h2>
            <UPageCard variant="subtle" :ui="{ container: 'p-0 sm:p-0 gap-y-0' }">
              <div class="divide-y divide-default">
                <div
                  v-for="(row, i) in detailRows"
                  :key="i"
                  class="flex items-center justify-between gap-4 px-4 py-3 sm:px-6"
                >
                  <div class="flex min-w-0 items-center gap-3">
                    <span class="flex size-8 shrink-0 items-center justify-center rounded-md bg-elevated">
                      <UIcon :name="row.icon" class="size-4 text-muted" />
                    </span>
                    <span class="text-sm text-default">{{ row.label }}</span>
                  </div>
                  <span class="text-right text-sm font-semibold tabular-nums" :class="row.valueClass || 'text-highlighted'">
                    {{ row.value }}
                  </span>
                </div>
              </div>
            </UPageCard>
          </section>

          <!-- Token stats -->
          <section v-if="usageStatCells.length > 0" class="fade-up fade-up-delay-3">
            <h2 class="mb-3 text-base font-semibold text-highlighted">{{ t('keyUsage.tokenStats') }}</h2>
            <div class="overflow-hidden rounded-lg border border-default">
              <div class="grid grid-cols-2 gap-px bg-(--ui-border) md:grid-cols-4">
                <div v-for="(cell, i) in usageStatCells" :key="i" class="bg-default px-4 py-3 sm:px-5">
                  <div class="text-xs text-muted">{{ cell.label }}</div>
                  <div class="mt-1 text-sm font-semibold tabular-nums text-highlighted">{{ cell.value }}</div>
                </div>
              </div>
            </div>
          </section>

          <!-- Daily usage -->
          <section v-if="showDailyUsage" class="fade-up fade-up-delay-4">
            <div class="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <h2 class="text-base font-semibold text-highlighted">{{ t('keyUsage.dailyDetail') }}</h2>
              <UTabs
                :model-value="dailyUsageDays"
                :items="dailyUsageOptions"
                :content="false"
                size="xs"
                class="w-auto"
                :ui="{ list: 'w-auto' }"
                @update:model-value="setDailyUsageDays(Number($event) as 7 | 30 | 90)"
              />
            </div>
            <UTable
              :data="dailyUsageRows"
              :columns="dailyColumns"
              :empty="t('keyUsage.noDailyUsage')"
              class="rounded-lg border border-default"
              :ui="{ thead: 'bg-elevated/50', th: 'py-3 text-xs font-medium text-muted', td: 'py-3' }"
            />
          </section>

          <!-- Model stats -->
          <section v-if="modelStats.length > 0" class="fade-up fade-up-delay-4">
            <h2 class="mb-3 text-base font-semibold text-highlighted">{{ t('keyUsage.modelStats') }}</h2>
            <UTable
              :data="modelStats"
              :columns="modelColumns"
              class="rounded-lg border border-default"
              :ui="{ thead: 'bg-elevated/50', th: 'py-3 text-xs font-medium text-muted', td: 'py-3' }"
            />
          </section>
        </div>
      </UContainer>
    </main>

    <!-- Footer -->
    <footer class="border-t border-default">
      <UContainer class="flex flex-col items-center justify-between gap-3 py-6 sm:flex-row">
        <p class="text-sm text-muted">&copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}</p>
        <div class="flex items-center gap-1">
          <UButton
            v-if="docUrl"
            :href="docUrl"
            external
            target="_blank"
            :label="t('home.docs')"
            color="neutral"
            variant="link"
            size="sm"
          />
          <UButton :href="githubUrl" external target="_blank" label="GitHub" color="neutral" variant="link" size="sm" />
        </div>
      </UContainer>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import PublicHeader from '@/views/public/components/PublicHeader.vue'
import ThemeToggleButton from '@/views/public/components/ThemeToggleButton.vue'
import { buildGatewayUrl } from '@/api/client'
import { formatDateLocalInput } from '@/utils/format'
import { sanitizeUrl } from '@/utils/url'
import { resolveDocsUrl } from '@/utils/docsUrl'

const { t, locale } = useI18n()
const appStore = useAppStore()

// ==================== Site Settings (same as HomeView) ====================

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const docUrl = computed(() => resolveDocsUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl))
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// Light/dark follows the shared site theme (main.ts + ThemeToggleButton), including "system".

const currentYear = computed(() => new Date().getFullYear())

// ==================== Key Query State ====================

const apiKey = ref('')
const keyVisible = ref(false)
const isQuerying = ref(false)
const showResults = ref(false)
const showLoading = ref(false)
const showDatePicker = ref(false)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const resultData = ref<any>(null)
const now = ref(new Date())
let resetTimer: ReturnType<typeof setInterval> | null = null

// ==================== Date Range State ====================

type DateRangeKey = 'today' | '7d' | '30d' | 'custom'
const currentRange = ref<DateRangeKey>('today')
const customStartDate = ref('')
const customEndDate = ref('')
const dailyUsageDays = ref<7 | 30 | 90>(30)

const dateRangeItems = computed(() => [
  { value: 'today', label: t('keyUsage.dateRangeToday') },
  { value: '7d', label: t('keyUsage.dateRange7d') },
  { value: '30d', label: t('keyUsage.dateRange30d') },
  { value: 'custom', label: t('keyUsage.dateRangeCustom') },
])

const dailyUsageOptions = computed(() => [
  { value: 7 as const, label: t('keyUsage.dateRange7d') },
  { value: 30 as const, label: t('keyUsage.dateRange30d') },
  { value: 90 as const, label: t('keyUsage.dateRange90d') },
])

function setDateRange(key: DateRangeKey) {
  currentRange.value = key
  if (key !== 'custom') {
    queryKey()
  }
}

function getDateParams(): string {
  const now = new Date()
  const params = new URLSearchParams()

  if (currentRange.value === 'custom') {
    if (customStartDate.value && customEndDate.value) {
      params.set('start_date', customStartDate.value)
      params.set('end_date', customEndDate.value)
    }
  } else {
    const end = formatDateLocalInput(now)
    let start: string
    switch (currentRange.value) {
      case 'today': start = end; break
      case '7d': start = formatDateLocalInput(new Date(now.getTime() - 7 * 86400000)); break
      case '30d': start = formatDateLocalInput(new Date(now.getTime() - 30 * 86400000)); break
      default: start = formatDateLocalInput(new Date(now.getTime() - 30 * 86400000))
    }
    params.set('start_date', start)
    params.set('end_date', end)
  }
  params.set('days', String(dailyUsageDays.value))
  params.set('timezone', getBrowserTimezone())
  return params.toString()
}

function setDailyUsageDays(days: 7 | 30 | 90) {
  if (dailyUsageDays.value === days) return
  dailyUsageDays.value = days
  if (resultData.value && apiKey.value.trim()) {
    queryKey()
  }
}

// ==================== Quota Card Animation ====================

const RING_ICONS: Record<RingItem['iconType'], string> = {
  clock: 'i-lucide-clock',
  calendar: 'i-lucide-calendar',
  dollar: 'i-lucide-dollar-sign',
}

// Percentages count up from 0 after a query; the progress bars follow the same numbers.
const displayPcts = ref<number[]>([])

interface RingItem {
  title: string
  pct: number
  amount: string
  isBalance?: boolean
  iconType: 'clock' | 'calendar' | 'dollar'
  resetAt?: string | null
}

function triggerRingAnimation(items: RingItem[]) {
  displayPcts.value = items.map(() => 0)

  nextTick(() => {
    requestAnimationFrame(() => {
      setTimeout(() => {
        // Animate percentage numbers
        const duration = 1000
        const startTime = performance.now()
        const targets = items.map(item => item.isBalance ? 0 : item.pct)

        function tick() {
          const elapsed = performance.now() - startTime
          const p = Math.min(elapsed / duration, 1)
          const ease = 1 - Math.pow(1 - p, 3)
          displayPcts.value = targets.map(target => Math.round(ease * target))
          if (p < 1) requestAnimationFrame(tick)
        }
        requestAnimationFrame(tick)
      }, 50)
    })
  })
}

// ==================== Computed Data ====================

const statusInfo = computed(() => {
  const data = resultData.value
  if (!data) return null

  if (data.mode === 'quota_limited') {
    const isValid = data.isValid !== false
    const statusMap: Record<string, string> = {
      active: 'Active',
      quota_exhausted: 'Quota Exhausted',
      expired: 'Expired',
    }
    return {
      label: t('keyUsage.quotaMode'),
      statusText: statusMap[data.status] || data.status || 'Unknown',
      isActive: isValid && data.status === 'active',
    }
  }

  return {
    label: data.planName || t('keyUsage.walletBalance'),
    statusText: 'Active',
    isActive: true,
  }
})

const ringItems = computed<RingItem[]>(() => {
  const data = resultData.value
  if (!data) return []

  const items: RingItem[] = []

  if (data.mode === 'quota_limited') {
    if (data.quota) {
      const pct = data.quota.limit > 0 ? Math.min(Math.round((data.quota.used / data.quota.limit) * 100), 100) : 0
      items.push({ title: t('keyUsage.totalQuota'), pct, amount: `${usd(data.quota.used)} / ${usd(data.quota.limit)}`, iconType: 'dollar' })
    }
    if (data.rate_limits) {
      const windowLabels: Record<string, string> = { '5h': t('keyUsage.limit5h'), '1d': t('keyUsage.limitDaily'), '7d': t('keyUsage.limit7d') }
      const windowIcons: Record<string, 'clock' | 'calendar'> = { '5h': 'clock', '1d': 'calendar', '7d': 'calendar' }
      for (const rl of data.rate_limits) {
        const pct = rl.limit > 0 ? Math.min(Math.round((rl.used / rl.limit) * 100), 100) : 0
        items.push({
          title: windowLabels[rl.window] || rl.window,
          pct,
          amount: `${usd(rl.used)} / ${usd(rl.limit)}`,
          iconType: windowIcons[rl.window] || 'clock',
          resetAt: rl.reset_at,
        })
      }
    }
  } else {
    if (data.subscription) {
      const sub = data.subscription
      const limits = [
        { label: t('keyUsage.limitDaily'), usage: sub.daily_usage_usd, limit: sub.daily_limit_usd },
        { label: t('keyUsage.limitWeekly'), usage: sub.weekly_usage_usd, limit: sub.weekly_limit_usd },
        { label: t('keyUsage.limitMonthly'), usage: sub.monthly_usage_usd, limit: sub.monthly_limit_usd },
      ]
      for (const l of limits) {
        if (l.limit != null && l.limit > 0) {
          const pct = Math.min(Math.round((l.usage / l.limit) * 100), 100)
          items.push({ title: l.label, pct, amount: `${usd(l.usage)} / ${usd(l.limit)}`, iconType: 'calendar' })
        }
      }
    }
    if (!data.subscription && data.balance != null) {
      items.push({ title: t('keyUsage.walletBalance'), pct: 0, amount: usd(data.balance), isBalance: true, iconType: 'dollar' })
    }
  }

  return items
})

// One card stays narrow and centred; more cards share the row (four fit on wide screens).
const ringGridClass = computed(() => {
  const len = ringItems.value.length
  if (len === 1) return 'mx-auto max-w-md grid-cols-1 sm:grid-cols-1 lg:grid-cols-1 gap-4'
  if (len === 2) return 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-2 gap-4'
  if (len === 3) return 'grid-cols-1 sm:grid-cols-3 lg:grid-cols-3 gap-4'
  return 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4'
})

interface DetailRow {
  icon: string
  label: string
  value: string
  valueClass: string
}

function getUsageColor(pct: number): string {
  if (pct > 90) return 'text-error'
  if (pct > 70) return 'text-warning'
  return 'text-success'
}

const ICON_SHIELD = 'i-lucide-shield'
const ICON_CALENDAR = 'i-lucide-calendar'
const ICON_DOLLAR = 'i-lucide-dollar-sign'
const ICON_CHECK = 'i-lucide-check'

const detailRows = computed<DetailRow[]>(() => {
  const data = resultData.value
  if (!data) return []

  const rows: DetailRow[] = []

  if (data.mode === 'quota_limited') {
    if (data.quota) {
      const remainColor = data.quota.remaining <= 0 ? 'text-error'
        : data.quota.remaining < data.quota.limit * 0.1 ? 'text-warning'
        : 'text-success'
      rows.push({ icon: ICON_SHIELD, label: t('keyUsage.remainingQuota'), value: usd(data.quota.remaining), valueClass: remainColor })
    }
    if (data.expires_at) {
      const daysLeft = data.days_until_expiry
      let expiryStr = formatDate(data.expires_at)
      if (daysLeft != null) {
        expiryStr += daysLeft > 0 ? ` ${t('keyUsage.daysLeft', { days: daysLeft })}` : daysLeft === 0 ? ` ${t('keyUsage.todayExpires')}` : ''
      }
      rows.push({ icon: ICON_CALENDAR, label: t('keyUsage.expiresAt'), value: expiryStr, valueClass: '' })
    }
    if (data.rate_limits) {
      const windowMap: Record<string, string> = { '5h': '5H', '1d': locale.value === 'zh' ? '日' : 'D', '7d': '7D' }
      for (const rl of data.rate_limits) {
        const pct = rl.limit > 0 ? (rl.used / rl.limit) * 100 : 0
        let valueStr = `${usd(rl.used)} / ${usd(rl.limit)}`
        const resetStr = formatResetTime(rl.reset_at)
        if (resetStr) {
          valueStr += ` (⟳ ${resetStr})`
        }
        rows.push({
          icon: ICON_DOLLAR,
          label: `${t('keyUsage.usedQuota')} (${windowMap[rl.window] || rl.window})`,
          value: valueStr,
          valueClass: getUsageColor(pct),
        })
      }
    }
  } else {
    rows.push({ icon: ICON_CHECK, label: t('keyUsage.subscriptionType'), value: data.planName || t('keyUsage.walletBalance'), valueClass: '' })

    if (data.subscription) {
      const sub = data.subscription
      if (sub.daily_limit_usd > 0) {
        const pct = (sub.daily_usage_usd / sub.daily_limit_usd) * 100
        rows.push({
          icon: ICON_DOLLAR,
          label: `${t('keyUsage.usedQuota')} (${locale.value === 'zh' ? '日' : 'D'})`, value: `${usd(sub.daily_usage_usd)} / ${usd(sub.daily_limit_usd)}`, valueClass: getUsageColor(pct),
        })
      }
      if (sub.weekly_limit_usd > 0) {
        const pct = (sub.weekly_usage_usd / sub.weekly_limit_usd) * 100
        rows.push({
          icon: ICON_DOLLAR,
          label: `${t('keyUsage.usedQuota')} (${locale.value === 'zh' ? '周' : 'W'})`, value: `${usd(sub.weekly_usage_usd)} / ${usd(sub.weekly_limit_usd)}`, valueClass: getUsageColor(pct),
        })
      }
      if (sub.monthly_limit_usd > 0) {
        const pct = (sub.monthly_usage_usd / sub.monthly_limit_usd) * 100
        rows.push({
          icon: ICON_DOLLAR,
          label: `${t('keyUsage.usedQuota')} (${locale.value === 'zh' ? '月' : 'M'})`, value: `${usd(sub.monthly_usage_usd)} / ${usd(sub.monthly_limit_usd)}`, valueClass: getUsageColor(pct),
        })
      }
      if (sub.expires_at) {
        rows.push({ icon: ICON_CALENDAR, label: t('keyUsage.subscriptionExpires'), value: formatDate(sub.expires_at), valueClass: '' })
      }
    }

    const remainColor = data.remaining != null
      ? (data.remaining <= 0 ? 'text-error' : data.remaining < 10 ? 'text-warning' : 'text-success')
      : ''
    rows.push({ icon: ICON_SHIELD, label: t('keyUsage.remainingQuota'), value: data.remaining != null ? usd(data.remaining) : '-', valueClass: remainColor })
  }

  return rows
})

interface StatCell {
  label: string
  value: string
}

const usageStatCells = computed<StatCell[]>(() => {
  const usage = resultData.value?.usage
  if (!usage) return []

  const today = usage.today || {}
  const total = usage.total || {}

  return [
    { label: t('keyUsage.todayRequests'), value: fmtNum(today.requests) },
    { label: t('keyUsage.todayInputTokens'), value: fmtNum(today.input_tokens) },
    { label: t('keyUsage.todayOutputTokens'), value: fmtNum(today.output_tokens) },
    { label: t('keyUsage.todayTokens'), value: fmtNum(today.total_tokens) },
    { label: t('keyUsage.todayCacheCreation'), value: fmtNum(today.cache_creation_tokens) },
    { label: t('keyUsage.todayCacheRead'), value: fmtNum(today.cache_read_tokens) },
    { label: t('keyUsage.todayCost'), value: usd(today.actual_cost) },
    { label: t('keyUsage.rpmTpm'), value: `${usage.rpm || 0} / ${usage.tpm || 0}` },
    { label: t('keyUsage.totalRequests'), value: fmtNum(total.requests) },
    { label: t('keyUsage.totalInputTokens'), value: fmtNum(total.input_tokens) },
    { label: t('keyUsage.totalOutputTokens'), value: fmtNum(total.output_tokens) },
    { label: t('keyUsage.totalTokensLabel'), value: fmtNum(total.total_tokens) },
    { label: t('keyUsage.totalCacheCreation'), value: fmtNum(total.cache_creation_tokens) },
    { label: t('keyUsage.totalCacheRead'), value: fmtNum(total.cache_read_tokens) },
    { label: t('keyUsage.totalCost'), value: usd(total.actual_cost) },
    { label: t('keyUsage.avgDuration'), value: usage.average_duration_ms ? `${Math.round(usage.average_duration_ms)} ms` : '-' },
  ]
})

interface ModelStatRow {
  model?: string
  requests?: number
  input_tokens?: number
  output_tokens?: number
  cache_creation_tokens?: number
  cache_read_tokens?: number
  total_tokens?: number
  cost?: number
  actual_cost?: number | null
}

const modelStats = computed<ModelStatRow[]>(() => resultData.value?.model_stats || [])

interface DailyUsageRow {
  date: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cost: number
  actual_cost?: number
}

const dailyUsageRows = computed<DailyUsageRow[]>(() => {
  const rows = resultData.value?.daily_usage
  return Array.isArray(rows) ? rows : []
})

const showDailyUsage = computed(() => Boolean(resultData.value && Array.isArray(resultData.value.daily_usage)))

// Numbers right-aligned in tabular figures; the first column and the cost stand out.
function textColumn<T>(id: string, label: string, value: (row: T) => string): TableColumn<T> {
  return { id, header: label, cell: ({ row }) => value(row.original), meta: { class: { td: 'font-medium text-highlighted' } } }
}

function numberColumn<T>(id: string, label: string, value: (row: T) => string, strong = false): TableColumn<T> {
  return {
    id,
    header: label,
    cell: ({ row }) => value(row.original),
    meta: { class: { th: 'text-right', td: `text-right tabular-nums ${strong ? 'font-medium text-highlighted' : 'text-default'}` } },
  }
}

const dailyColumns = computed<TableColumn<DailyUsageRow>[]>(() => [
  textColumn<DailyUsageRow>('date', t('keyUsage.date'), (r) => r.date),
  numberColumn<DailyUsageRow>('requests', t('keyUsage.requests'), (r) => fmtNum(r.requests)),
  numberColumn<DailyUsageRow>('input', t('keyUsage.inputTokens'), (r) => fmtNum(r.input_tokens)),
  numberColumn<DailyUsageRow>('output', t('keyUsage.outputTokens'), (r) => fmtNum(r.output_tokens)),
  numberColumn<DailyUsageRow>('cacheRead', t('keyUsage.cacheReadTokens'), (r) => fmtNum(r.cache_read_tokens)),
  numberColumn<DailyUsageRow>('cacheWrite', t('keyUsage.cacheWriteTokens'), (r) => fmtNum(r.cache_write_tokens)),
  numberColumn<DailyUsageRow>('cost', t('keyUsage.cost'), (r) => usd(r.actual_cost != null ? r.actual_cost : r.cost), true),
])

const modelColumns = computed<TableColumn<ModelStatRow>[]>(() => [
  textColumn<ModelStatRow>('model', t('keyUsage.model'), (m) => m.model || '-'),
  numberColumn<ModelStatRow>('requests', t('keyUsage.requests'), (m) => fmtNum(m.requests)),
  numberColumn<ModelStatRow>('input', t('keyUsage.inputTokens'), (m) => fmtNum(m.input_tokens)),
  numberColumn<ModelStatRow>('output', t('keyUsage.outputTokens'), (m) => fmtNum(m.output_tokens)),
  numberColumn<ModelStatRow>('cacheCreation', t('keyUsage.cacheCreationTokens'), (m) => fmtNum(m.cache_creation_tokens)),
  numberColumn<ModelStatRow>('cacheRead', t('keyUsage.cacheReadTokens'), (m) => fmtNum(m.cache_read_tokens)),
  numberColumn<ModelStatRow>('total', t('keyUsage.totalTokens'), (m) => fmtNum(m.total_tokens)),
  numberColumn<ModelStatRow>('cost', t('keyUsage.cost'), (m) => usd(m.actual_cost != null ? m.actual_cost : m.cost), true),
])

// ==================== Utility Functions ====================

function usd(value: number | null | undefined): string {
  if (value == null || value < 0) return '-'
  return '$' + Number(value).toFixed(2)
}

function fmtNum(val: number | null | undefined): string {
  if (val == null) return '-'
  return val.toLocaleString()
}

function formatDate(iso: string | null | undefined): string {
  if (!iso) return '-'
  const d = new Date(iso)
  const loc = locale.value === 'zh' ? 'zh-CN' : 'en-US'
  return d.toLocaleDateString(loc, { year: 'numeric', month: 'long', day: 'numeric' })
}

function getBrowserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

// ==================== API Query ====================

async function fetchUsage(key: string) {
  const dateParams = getDateParams()
  const url = buildGatewayUrl('/v1/usage') + (dateParams ? '?' + dateParams : '')
  const res = await fetch(url, {
    headers: { 'Authorization': 'Bearer ' + key },
  })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    const msg = body?.error?.message || body?.message || `${t('keyUsage.queryFailed')} (${res.status})`
    throw new Error(msg)
  }
  return await res.json()
}

async function queryKey() {
  if (isQuerying.value) return
  const key = apiKey.value.trim()
  if (!key) {
    appStore.showInfo(t('keyUsage.enterApiKey'))
    return
  }

  isQuerying.value = true
  showResults.value = true
  showLoading.value = true
  resultData.value = null

  try {
    const data = await fetchUsage(key)
    resultData.value = data
    showLoading.value = false
    showDatePicker.value = true

    // Count the percentages up after the DOM update
    nextTick(() => {
      triggerRingAnimation(ringItems.value)
    })

    appStore.showSuccess(t('keyUsage.querySuccess'))
  } catch (err) {
    showResults.value = false
    showLoading.value = false
    appStore.showError((err as Error).message || t('keyUsage.queryFailedRetry'))
  } finally {
    isQuerying.value = false
  }
}

// ==================== Lifecycle ====================

function formatResetTime(resetAt: string | null | undefined): string {
  if (!resetAt) return ''
  const diff = new Date(resetAt).getTime() - now.value.getTime()
  if (diff <= 0) return t('keyUsage.resetNow')
  const days = Math.floor(diff / 86400000)
  const hours = Math.floor((diff % 86400000) / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

onMounted(() => {
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
  resetTimer = setInterval(() => { now.value = new Date() }, 60000)
})

onUnmounted(() => {
  if (resetTimer) clearInterval(resetTimer)
})
</script>

<style scoped>
/* Result sections fade up one after another. */
@keyframes fade-up-kv {
  from { opacity: 0; transform: translateY(16px); }
  to { opacity: 1; transform: translateY(0); }
}
.fade-up {
  animation: fade-up-kv 0.5s cubic-bezier(0.4, 0, 0.2, 1) forwards;
}
.fade-up-delay-1 { animation-delay: 0.1s; opacity: 0; }
.fade-up-delay-2 { animation-delay: 0.2s; opacity: 0; }
.fade-up-delay-3 { animation-delay: 0.3s; opacity: 0; }
.fade-up-delay-4 { animation-delay: 0.4s; opacity: 0; }

@media (prefers-reduced-motion: reduce) {
  .fade-up,
  .fade-up-delay-1,
  .fade-up-delay-2,
  .fade-up-delay-3,
  .fade-up-delay-4 {
    animation: none;
    opacity: 1;
  }
}
</style>
