<template>
  <div class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-usage-pane">
    <!-- Top Header -->
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="chartColumn" class-name="h-5 w-5" :stroke-width="1.5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ u.title }}</h3>
        <button
          type="button"
          :aria-label="u.title"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, u.help, u.title)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
      </div>

      <span class="flex-1" />

      <div class="flex h-8 shrink-0 items-stretch overflow-hidden rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)]">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 whitespace-nowrap pe-3 ps-2.5 text-[13px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
          @mouseenter="tooltip.show($event, u.syncTip)"
          @mouseleave="tooltip.hide()"
          @click="triggerSync"
        >
          <CcSwitchLucideIcon
            name="refreshCw"
            class-name="h-3.5 w-3.5 text-[var(--app-fg2)]"
            :class="syncing ? 'animate-spin' : ''"
          />
          <span class="hidden sm:inline">{{ syncing ? u.syncing : u.justSynced }}</span>
        </button>
        <button
          type="button"
          :aria-label="u.settings"
          class="inline-flex w-8 items-center justify-center border-s border-[var(--ccs-border)] text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, u.settings)"
          @mouseleave="tooltip.hide()"
          @click="workbench.openPage('settings')"
        >
          <CcSwitchLucideIcon name="settings2" class-name="h-3.5 w-3.5" />
        </button>
      </div>
    </header>

    <!-- Filter Bar -->
    <div class="flex min-h-12 shrink-0 flex-wrap items-center gap-2 px-4 py-2 sm:px-6">
      <!-- Time Range Popover Trigger ($r) -->
      <span ref="rangeAnchorRef" class="inline-flex">
        <button
          type="button"
          :aria-expanded="rangeOpen"
          :title="`${u.timeRange}: ${rangeLabel}`"
          class="inline-flex h-8 max-w-[220px] shrink-0 items-center gap-1 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-2 ps-3 text-[13px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
          :class="rangeOpen ? 'bg-[var(--app-subtle)]' : ''"
          @click="openRangePopover"
        >
          <CcSwitchLucideIcon
            v-if="range.preset === 'custom'"
            name="calendarDays"
            class-name="h-3.5 w-3.5 shrink-0 text-[var(--app-fg2)]"
          />
          <span class="min-w-0 truncate">{{ rangeLabel }}</span>
          <CcSwitchLucideIcon
            name="chevronDown"
            class-name="h-3.5 w-3.5 shrink-0 text-[var(--app-fg2)] transition-transform"
            :class="rangeOpen ? 'rotate-180' : ''"
          />
        </button>

        <CcSwitchPopover
          :anchor="rangeAnchorRef"
          :open="rangeOpen"
          align="start"
          panel-class="w-[560px] max-w-[calc(100vw-2rem)] p-3"
          @close="rangeOpen = false"
        >
          <div class="flex flex-wrap gap-1.5 border-b border-[var(--ccs-border)] pb-2">
            <button
              v-for="p in PRESET_KEYS"
              :key="p"
              type="button"
              :aria-pressed="range.preset === p"
              class="h-7 rounded-md border px-2.5 text-[13px] transition-colors"
              :class="
                range.preset === p
                  ? 'border-[var(--ccs-fg)] bg-[var(--app-selected)] font-semibold text-[var(--ccs-fg)]'
                  : 'border-[var(--app-border-strong)] bg-[var(--app-surface)] font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-subtle)]'
              "
              @click="selectPreset(p)"
            >
              {{ u.presets[p] }}
            </button>
          </div>

          <div class="mt-3 flex flex-col gap-3 sm:flex-row">
            <div class="flex flex-col gap-2 sm:w-[220px] sm:shrink-0">
              <p class="text-xs text-[var(--app-fg2)]">{{ u.customRangeHint }}</p>
              <button
                type="button"
                class="w-full rounded-[10px] border px-3 py-2 text-start transition-colors"
                :class="
                  pickingField === 'start'
                    ? 'border-[var(--app-action)] bg-[var(--app-surface)] ring-1 ring-[var(--app-action)]'
                    : 'border-[var(--ccs-border)]'
                "
                @click="pickingField = 'start'"
              >
                <span class="mb-1 block text-[11px] font-medium uppercase tracking-wider text-[var(--app-fg2)]">
                  {{ u.startTime }}
                </span>
                <span class="flex items-center gap-3 text-[13px] tabular-nums text-[var(--ccs-fg)]">
                  <span class="flex-1">{{ formatYmd(draftStart) }}</span>
                  <span>{{ formatHm(draftStart) }}</span>
                </span>
              </button>

              <button
                type="button"
                :disabled="draftLiveEnd"
                class="w-full rounded-[10px] border px-3 py-2 text-start transition-colors"
                :class="
                  draftLiveEnd
                    ? 'cursor-not-allowed border-[var(--ccs-border)] bg-[var(--app-subtle)] opacity-50'
                    : pickingField === 'end'
                      ? 'border-[var(--app-action)] bg-[var(--app-surface)] ring-1 ring-[var(--app-action)]'
                      : 'border-[var(--ccs-border)]'
                "
                @click="pickingField = 'end'"
              >
                <span class="mb-1 block text-[11px] font-medium uppercase tracking-wider text-[var(--app-fg2)]">
                  {{ u.endTime }}
                </span>
                <span class="flex items-center gap-3 text-[13px] tabular-nums text-[var(--ccs-fg)]">
                  <span class="flex-1">{{ formatYmd(draftLiveEnd ? USAGE_REFERENCE_NOW : draftEnd) }}</span>
                  <span>{{ formatHm(draftLiveEnd ? USAGE_REFERENCE_NOW : draftEnd) }}</span>
                </span>
              </button>

              <label class="flex cursor-pointer select-none items-center gap-2 text-xs text-[var(--app-fg2)]">
                <input v-model="draftLiveEnd" type="checkbox" class="accent-[var(--app-action)]" />
                <span>{{ u.liveEndTime }}</span>
              </label>

              <div class="flex gap-2 pt-1">
                <button
                  type="button"
                  class="h-7 flex-1 rounded-md text-[13px] font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-subtle)]"
                  @click="rangeOpen = false"
                >
                  {{ u.cancel }}
                </button>
                <button
                  type="button"
                  class="h-7 flex-1 rounded-md bg-[var(--app-action)] text-[13px] font-semibold text-white hover:bg-[var(--app-action-hover)]"
                  @click="applyCustomRange"
                >
                  {{ u.confirm }}
                </button>
              </div>
            </div>

            <!-- Calendar Grid -->
            <div class="min-w-0 flex-1 rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-subtle)] p-2.5">
              <div class="mb-1.5 flex items-center justify-between">
                <button
                  type="button"
                  :aria-label="u.prevMonth"
                  class="flex h-7 w-7 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-surface)]"
                  @click="shiftCalendarMonth(-1)"
                >
                  <CcSwitchLucideIcon name="chevronLeft" class-name="h-3.5 w-3.5" />
                </button>
                <button
                  type="button"
                  class="text-[13px] font-medium text-[var(--ccs-fg)] hover:text-[var(--app-fg2)]"
                  @click="resetCalendarMonth"
                >
                  {{ calendarMonthLabel }}
                </button>
                <button
                  type="button"
                  :aria-label="u.nextMonth"
                  class="flex h-7 w-7 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-surface)]"
                  @click="shiftCalendarMonth(1)"
                >
                  <CcSwitchLucideIcon name="chevronRight" class-name="h-3.5 w-3.5" />
                </button>
              </div>
              <div class="mb-0.5 grid grid-cols-7 text-center text-[11px] text-[var(--app-fg2)]">
                <span v-for="(wd, idx) in calendarWeekdays" :key="idx" class="py-0.5">{{ wd }}</span>
              </div>
              <div class="grid grid-cols-7 gap-px">
                <button
                  v-for="day in calendarDays"
                  :key="day.toISOString()"
                  type="button"
                  class="h-7 rounded text-xs tabular-nums transition-colors"
                  :class="calendarDayClass(day)"
                  @click="pickCalendarDay(day)"
                >
                  {{ day.getDate() }}
                </button>
              </div>
            </div>
          </div>
        </CcSwitchPopover>
      </span>

      <span aria-hidden="true" class="mx-1 h-4 w-px bg-[var(--ccs-border)]" />

      <!-- App Icon Pill Strip -->
      <div class="flex h-8 min-w-0 max-w-full shrink-0 items-center gap-0.5 overflow-x-auto rounded-lg border border-[var(--app-border-strong)] bg-[var(--app-surface)] p-[2px]">
        <button
          type="button"
          class="inline-flex h-[26px] shrink-0 items-center rounded-[5px] px-2 text-[13px] transition-colors"
          :class="selectedApp === 'all' ? 'bg-[var(--app-subtle)] font-semibold text-[var(--ccs-fg)]' : 'font-medium text-[var(--app-fg2)] hover:text-[var(--ccs-fg)]'"
          @click="selectedApp = 'all'"
        >
          {{ u.all }}
        </button>
        <button
          v-for="app in usageStripApps"
          :key="app.id"
          type="button"
          :aria-label="app.label"
          class="flex h-[26px] w-[30px] shrink-0 items-center justify-center rounded-[5px] transition-colors"
          :class="selectedApp === app.id ? 'bg-[var(--app-subtle)]' : 'opacity-75 hover:opacity-100'"
          @mouseenter="tooltip.show($event, app.label)"
          @mouseleave="tooltip.hide()"
          @click="selectedApp = app.id"
        >
          <CcSwitchAppIcon
            :app="app"
            :size="16"
            :badge-class="selectedApp === app.id ? 'border-[var(--app-subtle)] bg-[var(--app-subtle)]' : 'border-[var(--app-surface)] bg-[var(--app-surface)]'"
          />
        </button>
      </div>

      <button
        type="button"
        class="inline-flex h-8 min-w-0 max-w-[260px] shrink items-center gap-1.5 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-2 ps-2.5 text-[13px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
        @click="activeTab = activeTab === 'providers' ? 'models' : 'providers'"
      >
        <CcSwitchLucideIcon name="listTree" class-name="h-4 w-4 shrink-0 text-[var(--app-fg3)]" />
        <span class="min-w-0 truncate">{{ u.providerFilter }} / {{ u.modelFilter }}</span>
        <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5 shrink-0 text-[var(--app-fg2)]" />
      </button>
    </div>

    <!-- Scrollable Usage Body -->
    <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-6 pt-1 sm:px-6">
      <!-- Card 1: 4 Clickable KPIs + Heatmap / Bar Chart -->
      <section class="shrink-0 rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
        <div class="relative z-10 border-b border-[var(--ccs-border)]">
          <div class="grid grid-cols-2 md:grid-cols-4">
            <div
              v-for="kpi in kpiCards"
              :key="kpi.key"
              class="group relative min-w-0 px-4 pb-3.5 pt-2.5"
            >
              <button
                type="button"
                data-ccs-overlay
                :aria-pressed="metric === kpi.key"
                :aria-label="`${kpi.label} ${kpi.value}`"
                class="absolute inset-0 transition-[background-color,box-shadow]"
                :class="
                  metric === kpi.key
                    ? 'shadow-[inset_0_-2px_0_var(--ccs-fg)]'
                    : 'group-hover:bg-[var(--app-subtle)]'
                "
                @click="metric = kpi.key"
              />
              <div class="pointer-events-none relative flex min-w-0 flex-col gap-0.5">
                <div class="flex h-[18px] min-w-0 items-center gap-0.5">
                  <span
                    class="truncate text-xs"
                    :class="metric === kpi.key ? 'text-[var(--ccs-fg)]' : 'text-[var(--app-fg2)]'"
                  >
                    {{ kpi.label }}
                  </span>
                  <button
                    v-if="kpi.help"
                    type="button"
                    class="pointer-events-auto inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
                    :aria-label="kpi.help.title"
                    @mouseenter="tooltip.show($event, kpi.help.body, kpi.help.title)"
                    @mouseleave="tooltip.hide()"
                  >
                    <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
                  </button>
                </div>
                <span
                  class="max-w-full self-start truncate text-[22px] font-semibold leading-7 tabular-nums"
                  :class="metric === kpi.key ? 'text-[var(--ccs-fg)]' : 'text-[var(--app-fg2)]'"
                >
                  {{ kpi.value }}
                </span>
              </div>
            </div>
          </div>

          <!-- Expandable 4 Secondary Token Metrics -->
          <div
            class="grid transition-[grid-template-rows] duration-200 ease-out"
            :class="moreMetricsOpen ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]'"
          >
            <div class="min-h-0 overflow-hidden">
              <div class="grid grid-cols-2 border-t border-[var(--ccs-border)] md:grid-cols-4">
                <div
                  v-for="sub in secondaryBreakdown"
                  :key="sub.label"
                  class="flex min-w-0 flex-col gap-0.5 px-4 py-2.5"
                >
                  <span class="whitespace-nowrap text-xs text-[var(--app-fg3)]">{{ sub.label }}</span>
                  <span class="self-start whitespace-nowrap text-[15px] tabular-nums text-[var(--app-fg2)]">
                    {{ formatCompactTokens(sub.value, lang) }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- Center Expand/Collapse Chevron -->
          <button
            type="button"
            :aria-label="u.moreMetrics"
            :aria-expanded="moreMetricsOpen"
            class="absolute bottom-0 left-1/2 inline-flex h-6 w-6 -translate-x-1/2 translate-y-1/2 items-center justify-center rounded-full border border-[var(--ccs-border)] bg-[var(--app-surface)] text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
            @mouseenter="tooltip.show($event, u.moreMetrics)"
            @mouseleave="tooltip.hide()"
            @click="moreMetricsOpen = !moreMetricsOpen"
          >
            <CcSwitchLucideIcon
              name="chevronDown"
              class-name="h-3.5 w-3.5 transition-transform duration-200"
              :class="moreMetricsOpen ? 'rotate-180' : ''"
            />
          </button>
        </div>

        <!-- 53-Week Heatmap (preset === 'all') -->
        <div v-if="range.preset === 'all'" class="px-4 pb-3 pt-5">
          <div class="text-[13px] font-semibold text-[var(--ccs-fg)]">{{ u.heatmapTitle }}</div>
          <div class="text-xs text-[var(--app-fg3)]">{{ u.heatmapSubtitle }}</div>
          <div class="mt-3 overflow-x-auto">
            <div class="min-w-[620px]">
              <div class="relative ml-7 h-4 text-[10px] text-[var(--ccs-muted-fg)]">
                <span
                  v-for="m in monthMarkers"
                  :key="m.week"
                  class="absolute"
                  :style="{ left: `${(m.week / 53) * 100}%` }"
                >
                  {{ m.text }}
                </span>
              </div>
              <div class="flex gap-1">
                <div class="grid w-6 shrink-0 grid-rows-7 gap-[3px] text-[10px] leading-[9px] text-[var(--ccs-muted-fg)]">
                  <span v-for="(wd, idx) in u.weekdays" :key="idx">{{ wd }}</span>
                </div>
                <div class="grid flex-1 grid-flow-col grid-rows-7 gap-[3px]">
                  <span
                    v-for="(level, idx) in heatmapCells"
                    :key="idx"
                    class="aspect-square rounded-[2px]"
                    :class="heatmapLevelClass(level)"
                  />
                </div>
              </div>
              <div class="mt-2 flex items-center justify-end gap-1 text-[10px] text-[var(--ccs-muted-fg)]">
                <span>{{ u.less }}</span>
                <span
                  v-for="level in [0, 1, 2, 3, 4]"
                  :key="level"
                  class="h-2.5 w-2.5 rounded-[2px]"
                  :class="heatmapLevelClass(level)"
                />
                <span>{{ u.more }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Hourly / Daily Bar Chart (Si) -->
        <div v-else class="px-4 pb-2.5 pt-5">
          <div class="min-h-7 whitespace-nowrap text-xs font-semibold leading-7 text-[var(--app-fg2)]">
            {{ fmt(u.trendTitle, { metric: u.metrics[metric], granularity: isHourly ? u.hourly : u.daily }) }}
          </div>
          <div class="mt-1 flex h-[132px] gap-1.5 pt-1.5">
            <div class="relative w-10 shrink-0 text-end text-[10px] tabular-nums text-[var(--app-fg3)]">
              <span
                v-for="tick in [1, 0.5, 0]"
                :key="tick"
                class="absolute end-0 -translate-y-1/2"
                :style="{ top: `${(1 - tick) * 100}%` }"
              >
                {{ formatAxisTick(chartMax * tick) }}
              </span>
            </div>
            <div class="relative min-w-0 flex-1">
              <span
                v-for="pct in [0, 50, 100]"
                :key="pct"
                class="absolute inset-x-0 h-px bg-[var(--app-chart-grid)]"
                :style="{ top: `${pct}%` }"
              />
              <div class="absolute inset-0 flex items-end gap-[3px]">
                <div
                  v-for="(b, idx) in buckets"
                  :key="b.key"
                  class="flex h-full min-w-0 flex-1 items-end justify-center rounded-t-[3px] hover:bg-[var(--app-subtle)]"
                  @mouseenter="tooltip.show($event, `${u.metrics[metric]}  ${formatBarValue(bucketValues[idx])}`, b.heading)"
                  @mouseleave="tooltip.hide()"
                >
                  <span
                    class="w-full max-w-[36px] rounded-t-[3px] bg-[var(--app-chart)] transition-[height] duration-300"
                    :style="{ height: `${(bucketValues[idx] / chartMax) * 100}%` }"
                  />
                </div>
              </div>
            </div>
          </div>
          <div class="ms-[46px] mt-1.5 flex gap-[3px] text-[10px] tabular-nums text-[var(--app-fg3)]">
            <span
              v-for="(b, idx) in buckets"
              :key="b.key"
              class="min-w-0 flex-1 overflow-visible whitespace-nowrap text-center"
            >
              {{ idx % labelStride === 0 ? b.label : '' }}
            </span>
          </div>
        </div>
      </section>

      <!-- Card 2: Tabbed Table (Request Logs / Providers / Models) -->
      <section class="flex shrink-0 flex-col overflow-hidden rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
        <div class="flex h-11 items-stretch gap-5 border-b border-[var(--ccs-border)] pe-2 ps-4">
          <button
            v-for="tabKey in (['logs', 'providers', 'models'] as const)"
            :key="tabKey"
            type="button"
            class="-mb-px inline-flex items-center gap-1.5 border-b-2 text-[13px] font-medium transition-colors"
            :class="
              activeTab === tabKey
                ? 'border-[var(--ccs-fg)] text-[var(--ccs-fg)]'
                : 'border-transparent text-[var(--app-fg2)] hover:text-[var(--ccs-fg)]'
            "
            @click="activeTab = tabKey"
          >
            <span>{{ u.tabs[tabKey] }}</span>
            <span class="rounded-[4px] bg-[var(--app-subtle)] px-1.5 text-xs font-medium tabular-nums text-[var(--app-fg3)]">
              {{ new Intl.NumberFormat(intlLoc).format(tabCounts[tabKey]) }}
            </span>
          </button>

          <span class="flex-1" />

          <div v-if="activeTab === 'logs'" class="flex items-center gap-2">
            <button
              type="button"
              class="inline-flex h-7 items-center gap-1.5 rounded-md pe-1.5 ps-2 text-xs text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
            >
              <CcSwitchLucideIcon name="listFilter" class-name="h-3.5 w-3.5" />
              <span>{{ u.statusCode }}</span>
              <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5" />
            </button>
            <span aria-hidden="true" class="hidden h-4 w-px bg-[var(--ccs-border)] sm:block" />
            <button
              type="button"
              :aria-label="u.logColumns"
              class="hidden h-7 w-7 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)] sm:inline-flex"
              @mouseenter="tooltip.show($event, u.logColumns)"
              @mouseleave="tooltip.hide()"
            >
              <CcSwitchLucideIcon name="columns3" class-name="h-[15px] w-[15px]" :stroke-width="1.6" />
            </button>
          </div>
        </div>

        <div class="w-full overflow-x-auto">
          <!-- Request Logs Table -->
          <table v-if="activeTab === 'logs'" class="w-full min-w-[760px] border-separate border-spacing-0 text-[13px] tabular-nums">
            <thead>
              <tr class="h-9 bg-[var(--app-subtle)] text-xs text-[var(--app-fg2)] [&>th]:border-b [&>th]:border-[var(--ccs-border)]">
                <th class="whitespace-nowrap px-2 text-start font-medium first:ps-4">{{ u.columns.time }}</th>
                <th class="whitespace-nowrap px-2 text-start font-medium">{{ u.columns.app }}</th>
                <th class="whitespace-nowrap px-2 text-start font-medium">{{ u.columns.provider }}</th>
                <th class="whitespace-nowrap px-2 text-start font-medium">{{ u.columns.model }}</th>
                <th class="whitespace-nowrap px-2 ps-6 text-start font-medium">{{ u.columns.cost }}</th>
                <th class="whitespace-nowrap px-2 ps-6 text-start font-medium">{{ u.columns.freshInput }}</th>
                <th class="whitespace-nowrap px-2 text-start font-medium">{{ u.columns.output }}</th>
                <th class="whitespace-nowrap px-2 text-start font-medium">{{ u.columns.cacheRead }}</th>
                <th class="whitespace-nowrap px-2 ps-6 text-start font-medium">
                  <span class="inline-flex items-center gap-0.5">
                    <span>{{ u.columns.speed }}</span>
                    <button
                      type="button"
                      :aria-label="u.columns.speedHelpTitle"
                      class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
                      @mouseenter="tooltip.show($event, u.columns.speedHelp, u.columns.speedHelpTitle)"
                      @mouseleave="tooltip.hide()"
                    >
                      <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
                    </button>
                  </span>
                </th>
                <th class="whitespace-nowrap px-2 pe-4 text-start font-medium">{{ u.columns.firstToken }}</th>
              </tr>
            </thead>
            <tbody class="text-[var(--ccs-fg)]">
              <tr
                v-for="row in filteredLogs"
                :key="row.time"
                class="h-10 [&:not(:last-child)>td]:border-b [&>td]:border-[var(--ccs-border)]"
              >
                <td class="whitespace-nowrap px-2 first:ps-4">{{ row.time }}</td>
                <td class="whitespace-nowrap px-2">
                  <span class="inline-flex items-center gap-1.5">
                    <CcSwitchAppIcon
                      :app="appSpec(row.app)"
                      :size="14"
                      badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]"
                    />
                    <span>{{ appSpec(row.app).label }}</span>
                  </span>
                </td>
                <td class="whitespace-nowrap px-2">{{ row.provider }}</td>
                <td class="whitespace-nowrap px-2 font-mono text-xs">{{ row.model }}</td>
                <td class="whitespace-nowrap px-2 ps-6">{{ row.cost }}</td>
                <td class="whitespace-nowrap px-2 ps-6">{{ row.input.toLocaleString('en-US') }}</td>
                <td class="whitespace-nowrap px-2">{{ row.output.toLocaleString('en-US') }}</td>
                <td class="whitespace-nowrap px-2">{{ row.cacheRead }}</td>
                <td class="whitespace-nowrap px-2 ps-6" :class="row.speed === '—' ? 'text-[var(--app-fg3)]' : ''">
                  <template v-if="row.speed === '—'">—</template>
                  <template v-else>{{ row.speed }}<span class="ms-0.5 text-[11px] text-[var(--app-fg3)]">tok/s</span></template>
                </td>
                <td class="whitespace-nowrap px-2 pe-4" :class="row.firstToken === '—' ? 'text-[var(--app-fg3)]' : ''">
                  {{ row.firstToken }}
                </td>
              </tr>
            </tbody>
          </table>

          <!-- Providers / Models Aggregate Table -->
          <table v-else class="w-full min-w-[640px] border-separate border-spacing-0 text-[13px] tabular-nums">
            <thead>
              <tr class="h-9 bg-[var(--app-subtle)] text-xs text-[var(--app-fg2)] [&>th]:border-b [&>th]:border-[var(--ccs-border)]">
                <th class="whitespace-nowrap px-3 ps-4 text-start font-medium">
                  {{ activeTab === 'providers' ? u.columns.provider : u.columns.model }}
                </th>
                <th class="whitespace-nowrap px-3 text-start font-medium">{{ u.columns.requests }}</th>
                <th class="whitespace-nowrap px-3 text-start font-medium">{{ u.columns.cost }}</th>
                <th class="whitespace-nowrap px-3 text-start font-medium">{{ u.columns.tokens }}</th>
                <th class="whitespace-nowrap px-3 ps-7 text-start font-medium">{{ u.columns.successRate }}</th>
                <th class="whitespace-nowrap px-3 text-start font-medium">{{ u.columns.speed }}</th>
                <th class="whitespace-nowrap px-3 pe-4 text-start font-medium">{{ u.columns.firstToken }}</th>
              </tr>
            </thead>
            <tbody class="text-[var(--ccs-fg)]">
              <tr
                v-for="row in aggregates[activeTab]"
                :key="row.name"
                class="h-10 [&:not(:last-child)>td]:border-b [&>td]:border-[var(--ccs-border)]"
              >
                <td class="whitespace-nowrap px-3 ps-4" :class="activeTab === 'models' ? 'font-mono text-xs' : ''">
                  {{ row.name }}
                </td>
                <td class="whitespace-nowrap px-3">{{ new Intl.NumberFormat(intlLoc).format(Math.round(row.requests)) }}</td>
                <td class="whitespace-nowrap px-3">{{ formatUsd(row.cost) }}</td>
                <td class="whitespace-nowrap px-3">{{ formatCompactTokens(row.tokens, lang) }}</td>
                <td class="whitespace-nowrap px-3 ps-7">{{ row.success.toFixed(row.success >= 99.95 ? 0 : 1) }}%</td>
                <td class="whitespace-nowrap px-3">
                  {{ Math.round(row.speed) }}<span class="ms-0.5 text-[11px] text-[var(--app-fg3)]">tok/s</span>
                </td>
                <td class="whitespace-nowrap px-3 pe-4">{{ row.firstToken.toFixed(1) }}s</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip, useCcSwitchWorkbench } from './ccswitchContext'
import {
  CC_SWITCH_APP_MAP,
  CC_SWITCH_DEMO_APPS,
  INITIAL_USAGE_LOGS,
  SITE_PROVIDER_PLACEHOLDER,
  USAGE_APP_BASELINES,
  USAGE_APP_KEYS,
  USAGE_REFERENCE_NOW,
  aggregatePoints,
  buildHeatmapCells,
  buildUsageAggregates,
  buildUsageBuckets,
  calcBucketPoint,
  formatCompactTokens,
  formatUsd,
  isSameDay,
  niceCeiling,
  resolveRangeWindow,
  splitTokenBreakdown,
  startOfDay,
  type UsageStatsPoint,
} from './ccswitchDemoData'
import CcSwitchAppIcon from './CcSwitchAppIcon.vue'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchPopover from './CcSwitchPopover.vue'
import type { CcsUsageMetric, CcsUsagePreset, CcsUsageRange } from './ccswitchTypes'

const { lang, msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()
const workbench = useCcSwitchWorkbench()

const u = computed(() => msg.value.window.usage)
const intlLoc = computed(() => (lang.value === 'zh' ? 'zh-CN' : 'en-US'))

const metric = ref<CcsUsageMetric>('tokens')
const selectedApp = ref<string>('all')
const range = ref<CcsUsageRange>({ preset: 'all' })
const activeTab = ref<'logs' | 'providers' | 'models'>('logs')
const moreMetricsOpen = ref(false)
const syncing = ref(false)
let syncTimer: ReturnType<typeof setTimeout> | undefined

// Calendar popover state
const PRESET_KEYS: CcsUsagePreset[] = ['today', '1d', '7d', '14d', '30d', 'all']
const rangeAnchorRef = ref<HTMLElement | null>(null)
const rangeOpen = ref(false)
const draftStart = ref<Date>(USAGE_REFERENCE_NOW)
const draftEnd = ref<Date>(USAGE_REFERENCE_NOW)
const draftLiveEnd = ref(false)
const pickingField = ref<'start' | 'end'>('start')
const calendarMonth = ref<Date>(new Date(USAGE_REFERENCE_NOW.getFullYear(), USAGE_REFERENCE_NOW.getMonth(), 1))

function appSpec(id: string) {
  return CC_SWITCH_APP_MAP[id] ?? CC_SWITCH_APP_MAP.claude
}

const usageStripApps = computed(() =>
  CC_SWITCH_DEMO_APPS.filter((a) => a.id in USAGE_APP_BASELINES && workbench.visibleApps.value.includes(a.id)),
)

const pad2 = (n: number) => String(n).padStart(2, '0')
const formatYmd = (d: Date) => `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
const formatHm = (d: Date) => `${pad2(d.getHours())}:${pad2(d.getMinutes())}`

const rangeLabel = computed(() => {
  if (range.value.preset !== 'custom') return u.value.presets[range.value.preset]
  const f = (d: Date) =>
    d.toLocaleString(intlLoc.value, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  return range.value.liveEnd ? `${f(range.value.start)} → ${u.value.liveEndNow}` : `${f(range.value.start)} - ${f(range.value.end)}`
})

function openRangePopover() {
  if (!rangeOpen.value) {
    const win = resolveRangeWindow(range.value)
    draftStart.value = win.start
    draftEnd.value = win.end
    draftLiveEnd.value = range.value.preset === 'custom' && range.value.liveEnd
    pickingField.value = 'start'
    calendarMonth.value = new Date(win.start.getFullYear(), win.start.getMonth(), 1)
  }
  rangeOpen.value = !rangeOpen.value
}

function selectPreset(preset: CcsUsagePreset) {
  range.value = { preset }
  rangeOpen.value = false
}

function applyCustomRange() {
  range.value = {
    preset: 'custom',
    start: draftStart.value,
    end: draftLiveEnd.value ? USAGE_REFERENCE_NOW : draftEnd.value,
    liveEnd: draftLiveEnd.value,
  }
  rangeOpen.value = false
}

function shiftCalendarMonth(delta: number) {
  calendarMonth.value = new Date(calendarMonth.value.getFullYear(), calendarMonth.value.getMonth() + delta, 1)
}

function resetCalendarMonth() {
  calendarMonth.value = new Date(USAGE_REFERENCE_NOW.getFullYear(), USAGE_REFERENCE_NOW.getMonth(), 1)
}

const calendarMonthLabel = computed(() =>
  calendarMonth.value.toLocaleDateString(intlLoc.value, { year: 'numeric', month: 'long' }),
)

const calendarWeekdays = computed(() => {
  const base = new Date(2026, 9, 4)
  return Array.from({ length: 7 }, (_, i) =>
    new Date(base.getFullYear(), base.getMonth(), base.getDate() + i).toLocaleDateString(intlLoc.value, { weekday: 'narrow' }),
  )
})

const calendarDays = computed(() => {
  const first = new Date(calendarMonth.value.getFullYear(), calendarMonth.value.getMonth(), 1)
  const start = new Date(first)
  start.setDate(first.getDate() - first.getDay())
  return Array.from({ length: 42 }, (_, i) => {
    const d = new Date(start)
    d.setDate(start.getDate() + i)
    return d
  })
})

function mergeDate(timeFrom: Date, dateFrom: Date): Date {
  return new Date(dateFrom.getFullYear(), dateFrom.getMonth(), dateFrom.getDate(), timeFrom.getHours(), timeFrom.getMinutes())
}

function pickCalendarDay(day: Date) {
  if (draftLiveEnd.value || pickingField.value === 'start') {
    const next = mergeDate(draftStart.value, day)
    draftStart.value = next
    if (!draftLiveEnd.value) {
      if (next > draftEnd.value) draftEnd.value = mergeDate(draftEnd.value, day)
      pickingField.value = 'end'
    }
  } else {
    const next = mergeDate(draftEnd.value, day)
    if (next < draftStart.value) draftStart.value = mergeDate(draftStart.value, day)
    else draftEnd.value = next
  }
  if (day.getMonth() !== calendarMonth.value.getMonth()) {
    calendarMonth.value = new Date(day.getFullYear(), day.getMonth(), 1)
  }
}

function calendarDayClass(day: Date): string {
  const inMonth = day.getMonth() === calendarMonth.value.getMonth()
  const startDay = startOfDay(draftStart.value)
  const endDay = startOfDay(draftLiveEnd.value ? USAGE_REFERENCE_NOW : draftEnd.value)
  const isEdge = isSameDay(day, startDay) || isSameDay(day, endDay)
  const inRange = day >= startDay && day <= endDay
  const isToday = isSameDay(day, USAGE_REFERENCE_NOW)
  if (isEdge) return 'bg-[var(--app-action)] font-medium text-white'
  if (inRange) return 'bg-[var(--app-selected)] text-[var(--ccs-fg)]'
  if (!inMonth) return 'text-[var(--app-fg3)]'
  return isToday
    ? 'text-[var(--ccs-fg)] ring-1 ring-[var(--app-border-strong)] hover:bg-[var(--app-surface)]'
    : 'text-[var(--ccs-fg)] hover:bg-[var(--app-surface)]'
}

// Calculations
const targetApps = computed(() => (selectedApp.value === 'all' ? USAGE_APP_KEYS : [selectedApp.value]))
const buckets = computed(() => (range.value.preset === 'all' ? [] : buildUsageBuckets(range.value, lang.value)))

const appTotals = computed<Record<string, UsageStatsPoint>>(() =>
  Object.fromEntries(
    targetApps.value.map((appId) => {
      if (range.value.preset === 'all') return [appId, USAGE_APP_BASELINES[appId]]
      const pts = buckets.value.map((b) => calcBucketPoint(appId, b))
      return [appId, aggregatePoints(pts)]
    }),
  ),
)

const totals = computed(() => aggregatePoints(Object.values(appTotals.value)))
const secondaryBreakdown = computed(() => {
  const s = splitTokenBreakdown(totals.value)
  return [
    { label: u.value.freshInput, value: s.freshInput },
    { label: u.value.output, value: s.output },
    { label: u.value.cacheWrite, value: s.cacheWrite },
    { label: u.value.cacheRead, value: s.cacheRead },
  ]
})

const kpiCards = computed(() => [
  { key: 'cost' as const, label: u.value.totalCost, value: formatUsd(totals.value.cost) },
  {
    key: 'requests' as const,
    label: u.value.totalRequests,
    value: new Intl.NumberFormat(intlLoc.value).format(Math.round(totals.value.requests)),
  },
  {
    key: 'tokens' as const,
    label: u.value.realTokens,
    value: formatCompactTokens(totals.value.tokens, lang.value),
    help: { title: u.value.realTokensHelpTitle, body: u.value.realTokensHelp },
  },
  {
    key: 'hitRate' as const,
    label: u.value.cacheHitRate,
    value: totals.value.tokens > 0 ? `${totals.value.hit.toFixed(1)}%` : '—',
    help: { title: u.value.hitRateHelpTitle, body: u.value.hitRateHelp },
  },
])

// Heatmap
const heatmapCells = computed(() => buildHeatmapCells(metric.value, selectedApp.value))
const monthMarkers = computed(() => {
  const list: Array<{ week: number; text: string }> = []
  for (let i = 0; i < 12; i++) {
    const week = Math.round((i * 53) / 12) + 2
    if (week < 52) list.push({ week, text: u.value.months[(i + 9) % 12] })
  }
  return list
})

const LIGHT_CELLS = ['bg-[var(--app-subtle)]', 'bg-sky-200', 'bg-sky-400', 'bg-sky-600', 'bg-sky-800']
const DARK_CELLS = ['', 'dark:bg-sky-950', 'dark:bg-sky-800', 'dark:bg-sky-600', 'dark:bg-sky-400']
function heatmapLevelClass(level: number): string {
  return `${LIGHT_CELLS[level]} ${DARK_CELLS[level]}`
}

// Bar Chart
const bucketValues = computed(() =>
  buckets.value.map((b) => {
    const pt = aggregatePoints(targetApps.value.map((appId) => calcBucketPoint(appId, b)))
    return metric.value === 'hitRate' ? pt.hit : pt[metric.value]
  }),
)
const isHourly = computed(() => buckets.value[0]?.hourly ?? false)
const chartMax = computed(() => (metric.value === 'hitRate' ? 100 : niceCeiling(Math.max(0, ...bucketValues.value))))
const labelStride = computed(() => Math.max(1, Math.ceil(buckets.value.length / 8)))

function formatAxisTick(val: number): string {
  if (metric.value === 'cost') return `$${formatCompactTokens(val, 'en')}`
  if (metric.value === 'hitRate') return `${Math.round(val)}%`
  return formatCompactTokens(val, lang.value)
}

function formatBarValue(val: number): string {
  if (metric.value === 'cost') return formatUsd(val, 4)
  if (metric.value === 'hitRate') return `${val.toFixed(1)}%`
  return new Intl.NumberFormat(intlLoc.value).format(Math.round(val))
}

// Tables
const resolveProviderName = (name: string) =>
  name === SITE_PROVIDER_PLACEHOLDER ? workbench.siteName.value : name

const filteredLogs = computed(() => {
  const rows =
    selectedApp.value === 'all' ? INITIAL_USAGE_LOGS : INITIAL_USAGE_LOGS.filter((r) => r.app === selectedApp.value)
  return rows.map((row) => ({
    ...row,
    provider: resolveProviderName(row.provider),
  }))
})
const aggregates = computed(() => ({
  providers: buildUsageAggregates('providers', appTotals.value).map((row) => ({
    ...row,
    name: resolveProviderName(row.name),
  })),
  models: buildUsageAggregates('models', appTotals.value),
}))
const tabCounts = computed(() => ({
  logs: Math.round(totals.value.requests),
  providers: aggregates.value.providers.length,
  models: aggregates.value.models.length,
}))

function triggerSync() {
  if (syncing.value) return
  syncing.value = true
  if (syncTimer) clearTimeout(syncTimer)
  syncTimer = setTimeout(() => {
    syncing.value = false
  }, 1000)
}

onBeforeUnmount(() => {
  if (syncTimer) clearTimeout(syncTimer)
})
</script>
