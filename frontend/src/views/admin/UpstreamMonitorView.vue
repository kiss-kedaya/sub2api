<template>
  <AppLayout>
    <div class="flex flex-col gap-4">
      <AdminPageHeader
        :title="text('上游监控', 'Upstream monitor')"
        :description="text(
          '余额、消费、告警、上游倍率与变动记录集中在一页。数据只来自已有的上游探测快照和本地汇总表，页面不会额外请求上游，也不扫 usage_logs。',
          'Balances, spend, alerts and upstream rates in one place. Everything reads the existing probe snapshots plus local rollups; the page adds no upstream calls and never scans usage_logs.'
        )"
      >
        <template #actions>
          <UButton icon="i-lucide-download" color="neutral" variant="outline" :label="text('导出 CSV', 'Export CSV')" :loading="exporting" @click="exportCsv" />
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="text('刷新', 'Refresh')" :loading="busy" @click="refresh" />
          <UButton icon="i-lucide-scan-line" :label="text('记录当前快照', 'Record current snapshots')" :loading="sampling" @click="pullSamples" />
        </template>
      </AdminPageHeader>

      <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" :aria-label="text('上游概览', 'Upstream overview')">
        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('上游总余额', 'Total upstream balance') }}</p>
          <div class="mt-2 space-y-1">
            <p v-for="currency in overview?.currencies ?? []" :key="currency.unit" class="text-2xl font-semibold tabular-nums">
              {{ formatMoney(currency.total, currency.unit) }}
            </p>
            <p v-if="!overview?.currencies?.length" class="text-2xl font-semibold text-muted">—</p>
          </div>
          <p class="mt-2 text-xs text-muted">{{ overview?.with_balance ?? 0 }} / {{ overview?.total_accounts ?? 0 }} {{ text('个账号有余额', 'accounts with a balance') }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('今日消费（估算）', 'Spend today (estimated)') }}</p>
          <div class="mt-2 space-y-1">
            <p v-for="item in overview?.spend ?? []" :key="item.unit" class="text-2xl font-semibold tabular-nums">
              {{ formatMoney(item.today, item.unit) }}
            </p>
            <p v-if="!overview?.spend?.length" class="text-2xl font-semibold text-muted">—</p>
          </div>
          <p class="mt-2 text-xs text-muted">
            <template v-for="(item, index) in overview?.spend ?? []" :key="`7d-${item.unit}`">
              <span v-if="index > 0"> · </span>{{ text('近 7 天', 'Last 7 days') }} {{ formatMoney(item.last_7_days, item.unit) }}
            </template>
            <span v-if="!overview?.spend?.length">{{ text('按余额下降推算，不含充值', 'Derived from balance drops, top-ups excluded') }}</span>
          </p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('未处理告警', 'Open alerts') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums" :class="(overview?.open_alerts ?? 0) > 0 ? 'text-amber-600 dark:text-amber-400' : ''">{{ overview?.open_alerts ?? 0 }}</p>
          <p class="mt-2 text-xs text-muted">{{ text('低余额、余额归零、倍率跳变', 'Low balance, depleted balance, rate jumps') }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('数据健康', 'Data health') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums">
            {{ overview?.healthy ?? 0 }}
            <span class="text-sm font-normal text-muted">/ {{ overview?.total_accounts ?? 0 }}</span>
          </p>
          <p class="mt-2 text-xs text-muted">{{ text('过期', 'Stale') }} {{ overview?.stale ?? 0 }} · {{ text('失败', 'Failed') }} {{ overview?.failed ?? 0 }} · {{ text('不支持', 'Unsupported') }} {{ overview?.unsupported ?? 0 }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('倍率覆盖', 'Rate coverage') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums">{{ overview?.rate_known ?? 0 }}</p>
          <p class="mt-2 text-xs text-muted">{{ text('个账号已读到上游倍率', 'accounts have an upstream rate') }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('24 小时倍率变动', 'Rate changes in 24h') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums" :class="(overview?.rate_changes_24h ?? 0) > 0 ? 'text-amber-600 dark:text-amber-400' : ''">{{ overview?.rate_changes_24h ?? 0 }}</p>
          <p class="mt-2 text-xs text-muted">{{ text('最近采样', 'Last sample') }} {{ overview?.last_sample_at ? new Date(overview.last_sample_at).toLocaleString() : '—' }}</p>
        </article>
      </section>

      <section v-if="overview?.attention?.length" class="card p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-semibold">{{ text('优先处理', 'Needs attention') }}</h3>
          <span class="text-xs text-muted">{{ text('按失败、不支持、过期排序', 'Ordered by failed, unsupported, stale') }}</span>
        </div>
        <div class="mt-3 flex flex-wrap gap-2">
          <button
            v-for="item in overview.attention"
            :key="item.account_id"
            type="button"
            class="inline-flex max-w-full items-center gap-2 rounded-full border border-default px-3 py-1 text-xs hover:bg-elevated"
            @click="openDetail(item)"
          >
            <span class="truncate font-medium">{{ item.name }}</span>
            <UBadge :color="statusColor(item.balance_status)" variant="subtle" size="sm">{{ statusLabel(item.balance_status) }}</UBadge>
            <span v-if="item.unlimited" class="text-muted">{{ text('不限', 'Unlimited') }}</span>
            <span v-else-if="item.balance !== null" class="tabular-nums text-muted">{{ formatMoney(item.balance, item.unit) }}</span>
          </button>
        </div>
      </section>

      <section class="card p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 class="font-semibold">{{ text('余额与消费趋势', 'Balance and spend trend') }}</h3>
            <p class="text-xs text-muted">{{ text('按北京时间按天汇总；余额取当天最后一次采样', 'Daily rollup on the Beijing calendar; balance is the last sample of the day') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <USelect v-if="trendUnits.length > 1" v-model="trendUnit" :items="trendUnitItems" class="min-w-28" :aria-label="text('币种', 'Currency')" />
            <div class="flex items-center gap-1 rounded-lg border border-default p-1">
              <UButton
                v-for="option in trendDayOptions"
                :key="option"
                size="xs"
                color="neutral"
                :variant="trendDays === option ? 'soft' : 'ghost'"
                :label="`${option}d`"
                @click="changeTrendDays(option)"
              />
            </div>
          </div>
        </div>

        <div v-if="trendChart" class="mt-3 overflow-x-auto">
          <svg :viewBox="`0 0 ${trendChart.width} ${trendChart.height}`" class="h-44 w-full min-w-[520px]" role="img" :aria-label="text('余额与消费趋势图', 'Balance and spend trend chart')">
            <line
              v-for="bar in trendChart.bars"
              :key="`bar-${bar.x}`"
              :x="bar.x"
              :y="bar.y"
              :width="bar.width"
              :height="bar.height"
              class="fill-amber-400/40"
            />
            <polyline :points="trendChart.line" fill="none" class="stroke-emerald-500" stroke-width="2" />
            <text
              v-for="label in trendChart.labels"
              v-show="label.show"
              :key="label.label + label.x"
              :x="label.x"
              :y="trendChart.height - 4"
              text-anchor="middle"
              class="fill-current text-[9px] text-muted"
            >{{ label.label }}</text>
          </svg>
          <div class="mt-2 flex flex-wrap items-center gap-4 text-xs text-muted">
            <span class="inline-flex items-center gap-1"><span class="inline-block h-0.5 w-4 bg-emerald-500" />{{ text('余额', 'Balance') }}</span>
            <span class="inline-flex items-center gap-1"><span class="inline-block h-2 w-3 rounded-sm bg-amber-400/60" />{{ text('当天消费', 'Spend') }}</span>
            <span>{{ text('余额峰值', 'Peak balance') }} {{ formatMoney(trendChart.maxBalance, trendUnit) }}</span>
          </div>
        </div>
        <p v-else class="mt-4 rounded-lg border border-dashed border-default p-6 text-center text-sm text-muted">
          {{ text('还没有汇总数据。点「记录当前快照」后，这里会按天累积。', 'No rollup yet. Record a snapshot and daily points start accumulating here.') }}
        </p>
      </section>

      <section class="card p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 class="font-semibold">{{ text('告警', 'Alerts') }}</h3>
            <p class="text-xs text-muted">{{ text('低余额与倍率跳变，同账号同类型 6 小时内只记一条', 'Low balance and rate jumps; at most one entry per account and kind every 6 hours') }}</p>
          </div>
          <div class="flex items-center gap-2">
            <USelect v-model="alertStatus" :items="alertStatusItems" class="min-w-32" :aria-label="text('告警状态', 'Alert status')" />
            <span class="text-xs text-muted">{{ alertTotal }} {{ text('条', 'entries') }}</span>
          </div>
        </div>

        <div v-if="alerts.length" class="mt-3 divide-y divide-default">
          <div v-for="alert in alerts" :key="alert.id" class="flex flex-wrap items-start justify-between gap-2 py-2">
            <div class="min-w-0">
              <p class="flex flex-wrap items-center gap-2 text-sm">
                <UBadge :color="alert.level === 'critical' ? 'error' : 'warning'" variant="subtle" size="sm">{{ alertKindLabel(alert.kind) }}</UBadge>
                <span class="font-medium text-highlighted">{{ alert.name || `#${alert.account_id}` }}</span>
                <span class="text-xs text-muted">{{ new Date(alert.created_at).toLocaleString() }}</span>
              </p>
              <p class="mt-1 text-sm text-muted">{{ alert.message }}</p>
            </div>
            <div class="flex items-center gap-2">
              <UBadge v-if="alert.acked_at" color="neutral" variant="subtle" size="sm">{{ text('已处理', 'Acked') }}</UBadge>
              <UButton
                v-else
                size="xs"
                color="neutral"
                variant="outline"
                icon="i-lucide-check"
                :label="text('标记已处理', 'Ack')"
                @click="acknowledge(alert)"
              />
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-scan-search" :label="text('处理', 'Open')" @click="openAlertAccount(alert)" />
            </div>
          </div>
        </div>
        <p v-else class="mt-3 rounded-lg border border-dashed border-default p-4 text-center text-sm text-muted">{{ text('当前筛选下没有告警', 'No alerts for this filter') }}</p>

        <div v-if="alertPages > 1" class="mt-3 flex items-center justify-end gap-2 text-xs text-muted">
          <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" size="xs" :disabled="alertPage <= 1" @click="loadAlerts(alertPage - 1)" />
          <span>{{ alertPage }} / {{ alertPages }}</span>
          <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" size="xs" :disabled="alertPage >= alertPages" @click="loadAlerts(alertPage + 1)" />
        </div>
      </section>
      <PageToolbar>
        <template #left>
          <UInput v-model="search" icon="i-lucide-search" class="w-64 max-w-full" :placeholder="text('搜索账号名称或备注', 'Search name or note')" :aria-label="text('搜索账号', 'Search accounts')" />
        </template>
        <template #right>
          <USelect v-model="statusFilter" :items="statusItems" class="min-w-36" :aria-label="text('余额状态', 'Balance status')" />
          <USelect v-model="platformFilter" :items="platformItems" class="min-w-36" :aria-label="text('平台', 'Platform')" />
          <USelect v-model="sort" :items="sortItems" class="min-w-36" :aria-label="text('排序', 'Sort')" />
        </template>
      </PageToolbar>

      <UTable :data="rows" :columns="columns" :loading="busy" :ui="tableUi">
        <template #account-cell="{ row }">
          <p class="flex items-center gap-2 font-medium text-highlighted">
            <span class="truncate">{{ row.original.name }}</span>
            <UIcon v-if="row.original.alert_enabled" name="i-lucide-bell-ring" class="size-3.5 text-amber-500" :title="text('已设置余额告警', 'Balance alert enabled')" />
          </p>
          <p class="truncate text-xs text-muted">{{ row.original.notes || `#${row.original.account_id} · ${row.original.platform} · ${row.original.type}` }}</p>
          <p v-if="row.original.notes" class="truncate text-xs text-muted">#{{ row.original.account_id }} · {{ row.original.platform }} · {{ row.original.type }}</p>
        </template>
        <template #balance-cell="{ row }">
          <p class="font-medium tabular-nums">{{ row.original.unlimited ? text('不限', 'Unlimited') : row.original.balance === null ? '—' : formatMoney(row.original.balance, row.original.unit) }}</p>
          <p class="text-xs text-muted">{{ row.original.balance_read_at ? new Date(row.original.balance_read_at).toLocaleString() : text('尚未采集', 'Not sampled yet') }}</p>
        </template>
        <template #status-cell="{ row }">
          <UBadge :color="statusColor(row.original.balance_status)" variant="subtle">{{ statusLabel(row.original.balance_status) }}</UBadge>
          <p v-if="row.original.alert_threshold != null" class="mt-1 text-xs text-muted">{{ text('阈值', 'Threshold') }} {{ formatMoney(row.original.alert_threshold, row.original.unit) }}</p>
          <p v-if="row.original.balance_error" class="mt-1 max-w-xs whitespace-normal text-xs text-amber-600 dark:text-amber-400">{{ row.original.balance_error }}</p>
        </template>
        <template #rate-cell="{ row }">
          <p class="tabular-nums">{{ row.original.rate === null ? '—' : `× ${row.original.rate.toFixed(4)}` }}</p>
          <p class="text-xs text-muted">
            {{ row.original.rate_sync_enabled ? text('同步已开启', 'Sync on') : text('仅观察', 'Observe only') }}
            <span v-if="row.original.rate_synced != null"> · {{ text('已写回', 'Applied') }} × {{ row.original.rate_synced.toFixed(4) }}</span>
          </p>
        </template>
        <template #sampled-cell="{ row }">{{ row.original.last_sampled_at ? new Date(row.original.last_sampled_at).toLocaleString() : '—' }}</template>
        <template #actions-cell="{ row }">
          <div class="flex items-center justify-end gap-1">
            <UButton icon="i-lucide-bell" color="neutral" variant="ghost" :aria-label="text('告警设置', 'Alert settings')" :title="text('告警设置', 'Alert settings')" @click="openDetail(row.original, true)" />
            <UButton icon="i-lucide-chart-line" color="neutral" variant="ghost" :aria-label="text('详情', 'Details')" :title="text('详情', 'Details')" @click="openDetail(row.original)" />
            <UButton
              icon="i-lucide-scan-search"
              color="neutral"
              variant="ghost"
              :aria-label="text('立即读取', 'Read now')"
              :title="text('立即读取', 'Read now')"
              :loading="probingID === row.original.account_id"
              :disabled="probingID !== null"
              @click="probeAccount(row.original)"
            />
          </div>
        </template>
        <template #empty>
          <div v-if="!busy" class="space-y-1">
            <p class="font-medium text-highlighted">{{ text('没有匹配的上游账号', 'No matching upstream accounts') }}</p>
            <p class="text-sm text-muted">{{ text('先在账号管理里为 API Key 账号开启上游余额探测；这里只读取已采集的结果。', 'Enable the upstream balance probe for API-key accounts first; this page only reads stored results.') }}</p>
          </div>
        </template>
      </UTable>

      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-3 text-sm text-muted">
        <span>{{ text(`共 ${total} 个账号`, `${total} accounts`) }}</span>
        <div class="flex items-center gap-2">
          <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" :disabled="busy || page <= 1" :aria-label="text('上一页', 'Previous page')" @click="goPage(page - 1)" />
          <span class="tabular-nums">{{ page }} / {{ pages || 1 }}</span>
          <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" :disabled="busy || page >= pages" :aria-label="text('下一页', 'Next page')" @click="goPage(page + 1)" />
        </div>
      </div>

      <section class="card p-4">
        <button type="button" class="flex w-full items-center justify-between text-left" @click="rateOpen = !rateOpen">
          <span class="font-semibold">{{ text('倍率变动记录', 'Rate change history') }}</span>
          <span class="text-xs text-muted">{{ text('最近 7 天 · 共', 'Last 7 days ·') }} {{ rateTotal }} {{ text('条', 'entries') }}</span>
        </button>
        <div v-if="rateOpen" class="mt-3 space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <USelect v-model="rateMinPct" :items="rateMinPctItems" class="min-w-36" :aria-label="text('最小变动幅度', 'Minimum move')" />
            <span class="text-xs text-muted">{{ text('只显示幅度不小于该值的变动', 'Only moves at least this large') }}</span>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[720px] text-sm">
              <thead class="text-left text-xs text-muted">
                <tr>
                  <th class="py-2 pr-4 font-medium">{{ text('账号', 'Account') }}</th>
                  <th class="py-2 pr-4 font-medium">{{ text('原倍率', 'Previous') }}</th>
                  <th class="py-2 pr-4 font-medium">{{ text('新倍率', 'New') }}</th>
                  <th class="py-2 pr-4 font-medium">{{ text('幅度', 'Move') }}</th>
                  <th class="py-2 font-medium">{{ text('时间', 'When') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="change in rateChanges" :key="change.id" class="border-t border-default">
                  <td class="py-2 pr-4">{{ change.name || `#${change.account_id}` }} <span class="text-xs text-muted">#{{ change.account_id }}</span></td>
                  <td class="py-2 pr-4 tabular-nums">× {{ change.old_rate.toFixed(4) }}</td>
                  <td class="py-2 pr-4 tabular-nums" :class="change.new_rate > change.old_rate ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'">× {{ change.new_rate.toFixed(4) }}</td>
                  <td class="py-2 pr-4 tabular-nums" :class="change.change_pct > 0 ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'">{{ formatPct(change.change_pct) }}</td>
                  <td class="py-2">{{ new Date(change.changed_at).toLocaleString() }}</td>
                </tr>
                <tr v-if="!rateChanges.length">
                  <td colspan="5" class="py-3 text-center text-muted">{{ text('没有符合条件的倍率变动', 'No rate changes match this filter') }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="ratePages > 1" class="flex items-center justify-end gap-2 text-xs text-muted">
            <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" size="xs" :disabled="ratePage <= 1" @click="loadRateChanges(ratePage - 1)" />
            <span>{{ ratePage }} / {{ ratePages }}</span>
            <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" size="xs" :disabled="ratePage >= ratePages" @click="loadRateChanges(ratePage + 1)" />
          </div>
        </div>
      </section>
    </div>

    <BaseDialog :show="detailOpen" :title="selected ? `${selected.name} · #${selected.account_id}` : ''" @close="closeDetail">
      <div v-if="selected" class="space-y-4">
        <div class="grid grid-cols-2 gap-3 text-sm">
          <div class="rounded-lg border border-default p-3">
            <p class="text-xs text-muted">{{ text('当前余额', 'Current balance') }}</p>
            <p class="mt-1 text-xl font-semibold tabular-nums">{{ selected.unlimited ? text('不限', 'Unlimited') : selected.balance === null ? '—' : formatMoney(selected.balance, selected.unit) }}</p>
            <UBadge class="mt-2" :color="statusColor(selected.balance_status)" variant="subtle">{{ statusLabel(selected.balance_status) }}</UBadge>
          </div>
          <div class="rounded-lg border border-default p-3">
            <p class="text-xs text-muted">{{ text('上游倍率', 'Upstream rate') }}</p>
            <p class="mt-1 text-xl font-semibold tabular-nums">{{ selected.rate === null ? '—' : `× ${selected.rate.toFixed(4)}` }}</p>
            <p class="mt-2 text-xs text-muted">{{ selected.rate_sync_enabled ? text('已开启倍率同步', 'Rate sync enabled') : text('仅观察，不写回', 'Observe only') }}</p>
          </div>
        </div>

        <dl class="grid grid-cols-2 gap-2 text-sm">
          <div><dt class="text-xs text-muted">{{ text('平台', 'Platform') }}</dt><dd>{{ selected.platform }} · {{ selected.type }}</dd></div>
          <div><dt class="text-xs text-muted">{{ text('账号状态', 'Account status') }}</dt><dd>{{ selected.status }}</dd></div>
        </dl>

        <p v-if="selected.balance_error" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ selected.balance_error }}</p>

        <section ref="ruleSection" class="rounded-lg border border-default p-3">
          <div class="flex items-center justify-between">
            <h4 class="text-sm font-semibold">{{ text('余额 / 倍率告警', 'Balance and rate alerts') }}</h4>
            <UBadge v-if="selected.alert_enabled" color="warning" variant="subtle" size="sm">{{ text('已开启', 'Enabled') }}</UBadge>
          </div>
          <div class="mt-3 grid gap-3 sm:grid-cols-3">
            <label class="text-xs text-muted">
              {{ text('余额阈值', 'Balance threshold') }}
              <UInput v-model.number="ruleForm.threshold" type="number" min="0" step="0.01" class="mt-1 w-full" />
            </label>
            <label class="text-xs text-muted">
              {{ text('币种', 'Currency') }}
              <UInput v-model="ruleForm.unit" class="mt-1 w-full" :placeholder="selected.unit || 'USD'" />
            </label>
            <label class="text-xs text-muted">
              {{ text('倍率提醒幅度 %', 'Rate alert move %') }}
              <UInput v-model.number="ruleForm.rate_min_pct" type="number" min="0" max="100" step="1" class="mt-1 w-full" />
            </label>
          </div>
          <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
            <label class="inline-flex items-center gap-2 text-sm">
              <USwitch v-model="ruleForm.enabled" />
              <span>{{ text('启用告警', 'Enable alerts') }}</span>
            </label>
            <div class="flex items-center gap-2">
              <UButton
                v-if="rulesByAccount[selected.account_id]"
                size="sm"
                color="error"
                variant="ghost"
                :label="text('删除规则', 'Delete rule')"
                @click="removeRule"
              />
              <UButton size="sm" :loading="savingRule" :label="text('保存规则', 'Save rule')" @click="saveRule" />
            </div>
          </div>
          <p class="mt-2 text-xs text-muted">{{ text('阈值为 0 且启用时只提醒余额归零；留空币种表示不限制币种。', 'With a zero threshold only a depleted balance alerts; an empty currency matches any unit.') }}</p>
        </section>

        <div>
          <div class="mb-2 flex items-center justify-between">
            <p class="text-sm font-medium">{{ text('余额历史', 'Balance history') }}</p>
            <div class="flex items-center gap-1">
              <UButton v-for="days in [7, 14, 30]" :key="`h-${days}`" size="xs" color="neutral" :variant="historyDays === days ? 'soft' : 'ghost'" :label="`${days}d`" @click="loadHistory(days)" />
            </div>
          </div>
          <svg v-if="chart" viewBox="0 0 600 120" class="h-32 w-full" role="img" :aria-label="text('余额历史曲线', 'Balance history chart')">
            <polyline :points="chart.polyline" fill="none" class="stroke-emerald-500" stroke-width="2" />
          </svg>
          <p v-else class="rounded-lg border border-dashed border-default p-3 text-center text-sm text-muted">{{ text('这个账号还没有足够的历史点', 'Not enough history for this account') }}</p>
        </div>

        <div>
          <p class="mb-2 text-sm font-medium">{{ text('这个账号的倍率变动', 'Rate changes for this account') }}</p>
          <div class="max-h-48 overflow-y-auto rounded-lg border border-default">
            <table class="w-full text-sm">
              <tbody>
                <tr v-for="change in detailRateChanges" :key="change.id" class="border-b border-default last:border-b-0">
                  <td class="p-2 tabular-nums text-muted">× {{ change.old_rate.toFixed(4) }}</td>
                  <td class="p-2 tabular-nums">→ × {{ change.new_rate.toFixed(4) }}</td>
                  <td class="p-2 tabular-nums" :class="change.change_pct > 0 ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'">{{ formatPct(change.change_pct) }}</td>
                  <td class="p-2 text-right text-xs text-muted">{{ new Date(change.changed_at).toLocaleString() }}</td>
                </tr>
                <tr v-if="!detailRateChanges.length"><td class="p-3 text-center text-muted">{{ text('暂无记录', 'No records') }}</td></tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" :label="text('关闭', 'Close')" @click="closeDetail" />
          <UButton icon="i-lucide-scan-search" :label="text('立即读取', 'Read now')" :loading="probingID === selected?.account_id" :disabled="!selected || probingID !== null" @click="selected && probeAccount(selected)" />
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template><script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { probeUpstreamBilling } from '@/api/admin/accounts'
import {
  ackAlert,
  deleteAlertRule,
  exportMonitorCsv,
  getHistory,
  getOverview,
  getTrend,
  listAlertRules,
  listAccounts,
  listAlerts,
  listRateChanges,
  sampleNow,
  saveAlertRule,
  type UpstreamAlert,
  type UpstreamAlertRule,
  type UpstreamMonitorAccount,
  type UpstreamMonitorOverview,
  type UpstreamMonitorPoint,
  type UpstreamRateChange,
  type UpstreamTrendPoint,
} from '@/api/admin/upstreamMonitor'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const overview = ref<UpstreamMonitorOverview | null>(null)
const rows = ref<UpstreamMonitorAccount[]>([])
const total = ref(0)
const page = ref(1)
const pages = ref(1)
const pageSize = 20
const search = ref('')
const statusFilter = ref('all')
const platformFilter = ref('all')
const sort = ref('name')
const busy = ref(false)
const sampling = ref(false)
const exporting = ref(false)
const error = ref('')
const probingID = ref<number | null>(null)

const trend = ref<UpstreamTrendPoint[]>([])
const trendDays = ref(7)
const trendUnit = ref('')
const trendDayOptions = [7, 14, 30]

const alerts = ref<UpstreamAlert[]>([])
const alertTotal = ref(0)
const alertPage = ref(1)
const alertPages = ref(1)
const alertStatus = ref('open')

const rulesByAccount = ref<Record<number, UpstreamAlertRule>>({})
const ruleForm = reactive({ account_id: 0, threshold: 0, unit: '', rate_min_pct: 10, enabled: false })
const savingRule = ref(false)

const rateChanges = ref<UpstreamRateChange[]>([])
const rateTotal = ref(0)
const ratePage = ref(1)
const ratePages = ref(1)
const rateOpen = ref(false)
const rateMinPct = ref(0)

const detailOpen = ref(false)
const selected = ref<UpstreamMonitorAccount | null>(null)
const history = ref<UpstreamMonitorPoint[]>([])
const historyDays = ref(7)
const detailRateChanges = ref<UpstreamRateChange[]>([])

const statusItems = computed(() => [
  { label: text('全部状态', 'All statuses'), value: 'all' },
  { label: text('正常', 'OK'), value: 'ok' },
  { label: text('数据过期', 'Stale'), value: 'stale' },
  { label: text('读取失败', 'Failed'), value: 'failed' },
  { label: text('不支持', 'Unsupported'), value: 'unsupported' },
  { label: text('未采集', 'Not sampled'), value: 'unknown' },
])
const platformItems = computed(() => [
  { label: text('全部平台', 'All platforms'), value: 'all' },
  ...['anthropic', 'openai', 'gemini', 'grok', 'deepseek', 'kimi', 'zhipu'].map(value => ({ label: value, value })),
])
const sortItems = computed(() => [
  { label: text('按名称', 'By name'), value: 'name' },
  { label: text('余额从低到高', 'Balance low to high'), value: 'balance_asc' },
  { label: text('余额从高到低', 'Balance high to low'), value: 'balance_desc' },
])
const alertStatusItems = computed(() => [
  { label: text('未处理', 'Open'), value: 'open' },
  { label: text('已处理', 'Acked'), value: 'acked' },
  { label: text('全部', 'All'), value: 'all' },
])
const rateMinPctItems = computed(() => [
  { label: text('全部幅度', 'Any move'), value: 0 },
  { label: text('≥ 5%', '≥ 5%'), value: 5 },
  { label: text('≥ 10%', '≥ 10%'), value: 10 },
  { label: text('≥ 20%', '≥ 20%'), value: 20 },
])
const trendUnits = computed(() => {
  const units = new Set<string>()
  for (const point of trend.value) units.add(point.unit || '')
  for (const item of overview.value?.currencies ?? []) units.add(item.unit || '')
  return Array.from(units)
})
const trendUnitItems = computed(() => trendUnits.value.map(unit => ({ label: unit || '—', value: unit })))

const columns = computed<TableColumn<UpstreamMonitorAccount>[]>(() => [
  { id: 'account', header: text('账号', 'Account') },
  { id: 'balance', header: text('余额', 'Balance') },
  { id: 'status', header: text('状态', 'Status') },
  { id: 'rate', header: text('上游倍率', 'Upstream rate') },
  { id: 'sampled', header: text('最近采样', 'Last sample'), meta: { class: { th: 'w-40', td: 'w-40' } } },
  { id: 'actions', header: () => text('操作', 'Actions'), meta: { class: { th: 'w-28 text-right', td: 'w-28' } } },
])
const tableUi = {
  base: 'table-fixed border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
  separator: 'h-0',
}

const moneySymbols: Record<string, string> = { USD: '$', CNY: '¥', EUR: '€', GBP: '£' }
function formatMoney(value: number, unit?: string): string {
  if (!Number.isFinite(value)) return '—'
  const normalized = String(unit || '').trim().toUpperCase()
  const amount = Math.abs(value) >= 1000
    ? value.toLocaleString(undefined, { maximumFractionDigits: 2 })
    : value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 4 })
  return moneySymbols[normalized] ? `${moneySymbols[normalized]}${amount}` : `${amount} ${normalized}`.trim()
}

function formatPct(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return `${value > 0 ? '+' : ''}${value.toFixed(1)}%`
}

function statusLabel(status: string): string {
  const labels: Record<string, [string, string]> = {
    ok: ['正常', 'OK'],
    stale: ['数据过期', 'Stale'],
    failed: ['读取失败', 'Failed'],
    unsupported: ['不支持', 'Unsupported'],
    unknown: ['未采集', 'Not sampled'],
  }
  const label = labels[status]
  return label ? text(label[0], label[1]) : status
}
function statusColor(status: string) {
  if (status === 'ok') return 'success' as const
  if (status === 'failed') return 'error' as const
  if (status === 'stale') return 'warning' as const
  return 'neutral' as const
}
function alertKindLabel(kind: string): string {
  const labels: Record<string, [string, string]> = {
    balance_low: ['余额偏低', 'Low balance'],
    balance_depleted: ['余额归零', 'Depleted'],
    rate_jump: ['倍率跳变', 'Rate jump'],
  }
  const label = labels[kind]
  return label ? text(label[0], label[1]) : kind
}
const trendPoints = computed(() => trend.value.filter(point => !trendUnit.value || point.unit === trendUnit.value))
const trendChart = computed(() => {
  const points = trendPoints.value
  if (!points.length) return null
  const width = 720
  const height = 160
  const padX = 10
  const padTop = 10
  const padBottom = 20
  const values = points.map(point => point.balance_close ?? 0)
  const maxBalance = Math.max(...values, 0)
  const minBalance = Math.min(...values, 0)
  const span = maxBalance - minBalance || 1
  const maxSpend = Math.max(...points.map(point => point.spend), 0) || 1
  const step = points.length > 1 ? (width - padX * 2) / (points.length - 1) : 0
  const barWidth = Math.max(3, Math.min(18, ((width - padX * 2) / Math.max(points.length, 1)) * 0.5))
  const line = points.map((point, index) => {
    const x = padX + index * step
    const y = padTop + (1 - ((point.balance_close ?? minBalance) - minBalance) / span) * (height - padTop - padBottom)
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
  const bars = points.map((point, index) => {
    const barHeight = (point.spend / maxSpend) * (height - padTop - padBottom) * 0.6
    return {
      x: padX + index * step - barWidth / 2,
      y: height - padBottom - barHeight,
      width: barWidth,
      height: Math.max(barHeight, 0),
    }
  })
  const labelStep = Math.ceil(points.length / 7)
  const labels = points.map((point, index) => ({
    x: padX + index * step,
    label: point.day.slice(5),
    show: points.length <= 10 || index % labelStep === 0,
  }))
  return { width, height, line, bars, labels, maxBalance }
})

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    const [nextOverview, nextPage] = await Promise.all([
      getOverview(),
      listAccounts({
        page: page.value,
        pageSize,
        search: search.value.trim(),
        balanceStatus: statusFilter.value === 'all' ? '' : statusFilter.value,
        platform: platformFilter.value === 'all' ? '' : platformFilter.value,
        sort: sort.value,
      }),
    ])
    overview.value = nextOverview
    rows.value = nextPage.items
    total.value = nextPage.total
    pages.value = nextPage.pages || 1
    if (page.value > pages.value) {
      page.value = pages.value
      await refresh()
    }
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('加载失败', 'Failed to load')
  } finally {
    busy.value = false
  }
}

async function loadTrend() {
  try {
    const result = await getTrend(trendDays.value)
    trend.value = result.points
    if (!trendUnit.value || !result.points.some(point => point.unit === trendUnit.value)) {
      const withBalance = result.points.find(point => point.balance_close != null)
      trendUnit.value = withBalance?.unit ?? result.points[0]?.unit ?? ''
    }
  } catch {
    trend.value = []
  }
}

async function loadAlerts(target = 1) {
  try {
    const result = await listAlerts({
      page: target,
      pageSize: 10,
      days: 30,
      status: alertStatus.value,
    })
    alerts.value = result.items
    alertTotal.value = result.total
    alertPage.value = result.page
    alertPages.value = result.pages || 1
  } catch {
    alerts.value = []
    alertTotal.value = 0
    alertPages.value = 1
  }
}

async function loadRules() {
  try {
    const result = await listAlertRules()
    const map: Record<number, UpstreamAlertRule> = {}
    for (const rule of result.items) map[rule.account_id] = rule
    rulesByAccount.value = map
  } catch {
    rulesByAccount.value = {}
  }
}

async function loadRateChanges(target = 1) {
  try {
    const result = await listRateChanges({ page: target, pageSize: 10, days: 7, minPct: rateMinPct.value })
    rateChanges.value = result.items
    rateTotal.value = result.total
    ratePage.value = result.page
    ratePages.value = result.pages || 1
  } catch {
    rateChanges.value = []
  }
}

function goPage(target: number) {
  if (target < 1 || target > pages.value) return
  page.value = target
  void refresh()
}

async function pullSamples() {
  sampling.value = true
  error.value = ''
  try {
    await sampleNow()
    await Promise.all([refresh(), loadRateChanges(ratePage.value), loadTrend(), loadAlerts(alertPage.value)])
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('采样失败', 'Sampling failed')
  } finally {
    sampling.value = false
  }
}

async function exportCsv() {
  exporting.value = true
  error.value = ''
  try {
    const blob = await exportMonitorCsv({
      search: search.value.trim(),
      balanceStatus: statusFilter.value === 'all' ? '' : statusFilter.value,
      platform: platformFilter.value === 'all' ? '' : platformFilter.value,
      sort: sort.value,
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `upstream-monitor-${new Date().toISOString().slice(0, 10)}.csv`
    link.click()
    URL.revokeObjectURL(url)
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('导出失败', 'Export failed')
  } finally {
    exporting.value = false
  }
}

async function probeAccount(row: UpstreamMonitorAccount) {
  probingID.value = row.account_id
  error.value = ''
  try {
    await probeUpstreamBilling(row.account_id)
    await sampleNow()
    await Promise.all([refresh(), loadRateChanges(ratePage.value), loadTrend(), loadAlerts(alertPage.value)])
    if (selected.value?.account_id === row.account_id) {
      await loadDetail(row.account_id)
    }
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('读取失败', 'Read failed')
  } finally {
    probingID.value = null
  }
}

async function loadDetail(accountId: number) {
  const [points, changes] = await Promise.all([
    getHistory(accountId, historyDays.value),
    listRateChanges({ page: 1, pageSize: 20, days: 30, accountId }),
  ])
  history.value = points.points
  detailRateChanges.value = changes.items
}

async function openDetail(row: UpstreamMonitorAccount, focusRule = false) {
  selected.value = row
  detailOpen.value = true
  history.value = []
  detailRateChanges.value = []
  const rule = rulesByAccount.value[row.account_id]
  ruleForm.account_id = row.account_id
  ruleForm.threshold = rule?.threshold ?? Number(((row.balance ?? 0) * 0.2).toFixed(2))
  ruleForm.unit = rule?.unit ?? (row.unit || '')
  ruleForm.rate_min_pct = rule?.rate_min_pct ?? 10
  ruleForm.enabled = rule?.enabled ?? false
  try {
    await loadDetail(row.account_id)
  } catch {
    // History is optional; the snapshot summary above is still useful.
  }
  if (focusRule && !rule) {
    void loadRules()
  }
}

async function loadHistory(days: number) {
  if (!selected.value) return
  historyDays.value = days
  try {
    const points = await getHistory(selected.value.account_id, days)
    history.value = points.points
  } catch {
    history.value = []
  }
}

async function saveRule() {
  if (!selected.value) return
  savingRule.value = true
  error.value = ''
  try {
    const saved = await saveAlertRule({
      account_id: ruleForm.account_id,
      threshold: Number(ruleForm.threshold) || 0,
      unit: ruleForm.unit.trim(),
      rate_min_pct: Number(ruleForm.rate_min_pct) || 0,
      enabled: ruleForm.enabled,
    })
    rulesByAccount.value = { ...rulesByAccount.value, [saved.account_id]: saved }
    selected.value = { ...selected.value, alert_enabled: saved.enabled, alert_threshold: saved.threshold }
    await Promise.all([refresh(), loadAlerts(alertPage.value)])
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('保存失败', 'Save failed')
  } finally {
    savingRule.value = false
  }
}

async function removeRule() {
  if (!selected.value) return
  const accountId = selected.value.account_id
  savingRule.value = true
  error.value = ''
  try {
    await deleteAlertRule(accountId)
    const next = { ...rulesByAccount.value }
    delete next[accountId]
    rulesByAccount.value = next
    selected.value = { ...selected.value, alert_enabled: false, alert_threshold: null }
    ruleForm.enabled = false
    ruleForm.threshold = 0
    await refresh()
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('删除失败', 'Delete failed')
  } finally {
    savingRule.value = false
  }
}

async function acknowledge(alert: UpstreamAlert) {
  try {
    await ackAlert(alert.id)
    await Promise.all([loadAlerts(alertPage.value), refresh()])
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('操作失败', 'Failed')
  }
}

function openAlertAccount(alert: UpstreamAlert) {
  const row = rows.value.find(item => item.account_id === alert.account_id)
  if (row) {
    void openDetail(row, true)
    return
  }
  void refresh().then(() => {
    const found = rows.value.find(item => item.account_id === alert.account_id)
    if (found) void openDetail(found, true)
  })
}

function changeTrendDays(days: number) {
  trendDays.value = days
  void loadTrend()
}

function closeDetail() {
  detailOpen.value = false
  selected.value = null
  history.value = []
  detailRateChanges.value = []
}

const chart = computed(() => {
  const points = history.value
  if (points.length < 2) return null
  const values = points.map(point => point.balance)
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const width = 600
  const height = 120
  const polyline = points.map((point, index) => {
    const x = (index / (points.length - 1)) * width
    const y = height - ((point.balance - min) / span) * (height - 12) - 6
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
  return { polyline, min, max }
})

let searchTimer: ReturnType<typeof setTimeout> | undefined
watch([statusFilter, platformFilter, sort], () => { page.value = 1; void refresh() })
watch(search, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; void refresh() }, 300)
})
watch(alertStatus, () => { alertPage.value = 1; void loadAlerts(1) })
watch(rateMinPct, () => { ratePage.value = 1; void loadRateChanges(1) })
watch(rateOpen, open => { if (open && !rateChanges.value.length) void loadRateChanges(1) })

onMounted(() => {
  void refresh()
  void loadRateChanges(1)
  void loadTrend()
  void loadAlerts(1)
  void loadRules()
})
onBeforeUnmount(() => { if (searchTimer) clearTimeout(searchTimer) })
</script>