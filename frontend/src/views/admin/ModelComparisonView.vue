<template>
  <AppLayout>
    <div class="model-comparison flex flex-col gap-6">
      <AdminPageHeader :description="text('同一模型在各分组的实付价、价差和最稳渠道；价格已乘分组倍率，按 1M token 展示。', 'Effective prices per group for the same model, with spread and the most stable option. Prices already include the group ratio, shown per 1M tokens.')">
        <template #title>
          <span class="inline-flex flex-wrap items-center gap-2">
            {{ text('模型比价', 'Model price comparison') }}
            <UBadge color="neutral" variant="subtle" size="sm">{{ text('L2 渠道运营', 'L2 channel operations') }}</UBadge>
          </span>
        </template>
        <template #actions>
          <UButton to="/admin/groups" color="neutral" variant="ghost" :label="text('分组与倍率', 'Groups and ratios')" />
          <UButton to="/model-plaza?embedded=1" color="neutral" variant="ghost" :label="text('模型广场', 'Model plaza')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loading" :label="text('刷新', 'Refresh')" @click="load" />
          <UButton icon="i-lucide-download" :disabled="!offers.length" :label="text('导出 CSV', 'Export CSV')" @click="exportCsv" />
        </template>
      </AdminPageHeader>

      <UAlert v-if="error" role="alert" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="error" />
      <UAlert
        v-if="usingPublicFallback"
        color="neutral"
        variant="subtle"
        icon="i-lucide-info"
        :description="text('当前后端没有管理端全量接口，已回退到公开模型广场；专属分组可能缺失。', 'The admin endpoint is unavailable; fell back to the public model plaza, so exclusive groups may be missing.')"
      />

      <UPageGrid as="section" class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px" :aria-label="text('比价概览', 'Comparison overview')">
        <UPageCard icon="i-lucide-scale" :title="text('可比模型', 'Comparable models')" variant="subtle" :ui="statUi" :class="statClass">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ comparableCount }}</span>
          <span class="text-xs text-muted">{{ text('至少两个分组同时提供', 'Offered by at least two groups') }}</span>
        </UPageCard>
        <UPageCard icon="i-lucide-folder" :title="text('覆盖分组', 'Groups covered')" variant="subtle" :ui="statUi" :class="statClass">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ groupCount }}</span>
          <span class="text-xs text-muted">{{ text('有可售模型的分组', 'Groups with sellable models') }}</span>
        </UPageCard>
        <UPageCard icon="i-lucide-arrow-up-down" :title="text('最大价差', 'Largest spread')" variant="subtle" :ui="statUi" :class="statClass">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ maxSpreadLabel }}</span>
          <span class="truncate text-xs text-muted">{{ maxSpread ? maxSpread.name : text('暂无可比模型', 'No comparable models yet') }}</span>
        </UPageCard>
        <UPageCard icon="i-lucide-shield-alert" :title="text('降智异常分组', 'Suspect groups')" variant="subtle" :ui="statUi" :class="statClass">
          <div class="flex items-center gap-2">
            <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ suspectGroups }}</span>
            <UBadge v-if="suspectGroups" color="error" variant="subtle" size="sm">{{ text('异常', 'Suspect') }}</UBadge>
          </div>
          <span class="text-xs text-muted">{{ text('近 1 小时检测为异常', 'Flagged by quality checks in the last hour') }}</span>
        </UPageCard>
      </UPageGrid>

      <section class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <h2 class="text-base font-semibold text-highlighted">{{ text('分组倍率速览', 'Group ratios') }}</h2>
            <UPopover mode="hover">
              <UIcon name="i-lucide-info" class="size-4 text-dimmed" />
              <template #content><p class="max-w-xs p-2.5 text-xs text-muted">{{ text('倍率、账号数和模型数一眼对齐；价格页按生效倍率计算。', 'Ratios, account counts and model counts in one table. Effective prices use these ratios.') }}</p></template>
            </UPopover>
          </div>
          <span class="text-xs text-muted">{{ groupRows.length }} {{ text('个分组', 'groups') }}</span>
        </div>
        <UTable :data="groupRows" :columns="groupColumns" :ui="tableUi">
          <template #group-cell="{ row }">
            <p class="font-medium text-highlighted">{{ row.original.name }}</p>
            <p class="mt-0.5 text-xs text-muted">#{{ row.original.id }}</p>
          </template>
          <template #accounts-cell="{ row }">
            <span v-if="row.original.activeAccountCount != null" class="tabular-nums">{{ row.original.activeAccountCount }} / {{ row.original.accountCount ?? '—' }}</span>
            <span v-else class="text-dimmed">—</span>
          </template>
          <template #quality-cell="{ row }">
            <UBadge :color="qualityColor(row.original.quality_status)" variant="subtle" size="sm">{{ qualityLabel(row.original.quality_status) }}</UBadge>
          </template>
          <template #type-cell="{ row }">
            <UBadge v-if="row.original.subscription_type === 'subscription'" color="info" variant="subtle" size="sm">{{ text('订阅', 'Subscription') }}</UBadge>
            <UBadge v-else-if="row.original.is_exclusive" color="warning" variant="subtle" size="sm">{{ text('专属', 'Exclusive') }}</UBadge>
            <UBadge v-else color="neutral" variant="subtle" size="sm">{{ text('公开', 'Public') }}</UBadge>
          </template>
        </UTable>
      </section>

      <section class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <h2 class="text-base font-semibold text-highlighted">{{ text('模型比价', 'Model comparison') }}</h2>
            <UPopover mode="hover">
              <UIcon name="i-lucide-info" class="size-4 text-dimmed" />
              <template #content><p class="max-w-xs p-2.5 text-xs text-muted">{{ text('最低价按输入价排序；价差 = 最高输入价 / 最低输入价 − 1。', 'Cheapest is ranked by input price; spread = highest input / lowest input − 1.') }}</p></template>
            </UPopover>
          </div>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-1.5">
          <UInput v-model="search" icon="i-lucide-search" class="w-full sm:max-w-xs" :placeholder="text('搜索模型', 'Search model')" :aria-label="text('搜索模型', 'Search model')" />
          <div class="flex flex-wrap items-center gap-1.5">
            <USelect v-model="platformModel" class="min-w-32" :items="platformItems" :aria-label="text('平台', 'Platform')" />
            <USelect v-model="groupId" class="min-w-32" :items="groupItems" :aria-label="text('分组', 'Group')" />
            <USelect v-model="billingModel" class="min-w-32" :items="billingItems" :aria-label="text('计费方式', 'Billing mode')" />
            <USelect v-model="sort" class="min-w-36" :items="sortItems" :aria-label="text('排序', 'Sort')" />
            <UCheckbox v-model="onlyComparable" class="ml-1" :label="text('只看多分组', 'Multi-group only')" />
          </div>
        </div>

        <UTable
          v-model:expanded="expanded"
          :data="pageRows"
          :columns="comparisonColumns"
          :get-row-id="(row: ComparisonRow) => row.key"
          :empty="loading ? text('加载中…', 'Loading…') : text('没有匹配的模型。', 'No models match the filters.')"
          :ui="{ ...tableUi, tr: 'align-top data-[expanded=true]:bg-elevated/30' }"
        >
          <template #model-cell="{ row }">
            <p class="font-medium text-highlighted">{{ row.original.name }}</p>
            <p class="mt-0.5 flex flex-wrap items-center gap-1 text-xs text-muted">
              {{ row.original.platform }} · {{ row.original.offers.length }} {{ text('个分组', 'groups') }}
              <UBadge v-if="row.original.multiTier" color="neutral" variant="subtle" size="xs">{{ text('多档', 'Tiered') }}</UBadge>
            </p>
          </template>
          <template #cheapest-cell="{ row }">
            <template v-if="row.original.cheapest">
              <p class="font-medium text-default">{{ row.original.cheapest.groupName }}</p>
              <p class="mt-0.5 text-xs tabular-nums text-toned">{{ pricePair(row.original.cheapest) }}</p>
              <p class="mt-0.5 text-[11px] text-dimmed">{{ text('倍率', 'Ratio') }} {{ rateLabel(row.original.cheapest.groupRate) }}</p>
            </template>
            <span v-else class="text-dimmed">—</span>
          </template>
          <template #expensive-cell="{ row }">
            <template v-if="row.original.expensive">
              <p class="font-medium text-default">{{ row.original.expensive.groupName }}</p>
              <p class="mt-0.5 text-xs tabular-nums text-toned">{{ pricePair(row.original.expensive) }}</p>
            </template>
            <span v-else class="text-dimmed">—</span>
          </template>
          <template #spread-cell="{ row }">
            <UBadge v-if="row.original.spreadValue != null" :color="spreadColor(row.original.spreadValue)" variant="subtle" size="sm">{{ formatSpread(row.original.spreadValue) }}</UBadge>
            <span v-else class="text-dimmed">—</span>
          </template>
          <template #best-cell="{ row }">
            <template v-if="row.original.bestValue">
              <p class="font-medium text-default">{{ row.original.bestValue.groupName }}</p>
              <p class="mt-0.5 text-xs tabular-nums text-toned">{{ pricePair(row.original.bestValue) }}</p>
              <p class="mt-1 text-[11px] text-dimmed">{{ text('综合价最低', 'Lowest blended price') }}</p>
            </template>
            <span v-else class="text-dimmed">—</span>
          </template>
          <template #stable-cell="{ row }">
            <template v-if="row.original.bestStable">
              <p class="font-medium text-default">{{ row.original.bestStable.groupName }}</p>
              <p class="mt-0.5 text-xs tabular-nums text-toned">{{ pricePair(row.original.bestStable) }}</p>
              <UBadge color="success" variant="subtle" size="xs" class="mt-1">{{ text('检测正常', 'Healthy') }}</UBadge>
            </template>
            <span v-else class="text-xs text-dimmed">{{ text('无检测正常分组', 'No healthy group') }}</span>
          </template>
          <template #toggle-cell="{ row }">
            <div class="flex justify-end">
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                :trailing-icon="row.getIsExpanded() ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'"
                :label="row.getIsExpanded() ? text('收起', 'Collapse') : text('展开', 'Expand')"
                @click="row.toggleExpanded()"
              />
            </div>
          </template>
          <template #expanded="{ row }">
            <UTable :data="sortedOffers(row.original)" :columns="offerColumns" :ui="compactUi" class="rounded-lg bg-default ring ring-default">
              <template #group-cell="{ row: offer }">
                <p class="font-medium text-default">{{ offer.original.groupName }}</p>
                <p class="text-[11px] text-muted">#{{ offer.original.groupId }} · {{ offer.original.platform }}</p>
              </template>
              <template #cache-cell="{ row: offer }">
                <span v-if="offer.original.billingMode === 'token'" class="tabular-nums">{{ formatMoney(offer.original.cacheWritePerMillion) }} / {{ formatMoney(offer.original.cacheReadPerMillion) }}</span>
                <span v-else class="text-dimmed">—</span>
              </template>
              <template #status-cell="{ row: offer }">
                <div class="flex flex-wrap gap-1">
                  <UBadge :color="qualityColor(offer.original.qualityStatus)" variant="subtle" size="sm">{{ qualityLabel(offer.original.qualityStatus) }}</UBadge>
                  <UBadge v-if="offer.original.isExclusive" color="warning" variant="subtle" size="sm">{{ text('专属', 'Exclusive') }}</UBadge>
                </div>
              </template>
            </UTable>
          </template>
        </UTable>

        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4">
          <span class="text-sm text-muted">{{ text('显示', 'Showing') }} {{ rangeStart }}–{{ rangeEnd }} / {{ filtered.length }}</span>
          <UPagination v-model:page="page" :items-per-page="pageSize" :total="filtered.length" />
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { compactTableUi, statCardClass, statCardUi, templateTableUi, type SemanticColor } from '@/views/admin/ops/utils/templateUi'
import {
  getAdminModelPlaza,
  getModelPlaza,
  type ModelPlazaGroup,
} from '@/api/modelPlaza'
import * as groupsApi from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  buildModelOffers,
  compareModels,
  filterComparisons,
  sortComparisons,
  type ComparisonSort,
  type ModelComparison,
  type ModelOffer,
} from '@/utils/modelComparison'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const loading = ref(false)
const error = ref('')
const usingPublicFallback = ref(false)
const groups = ref<ModelPlazaGroup[]>([])
const adminGroups = ref<AdminGroup[]>([])
const search = ref('')
const platform = ref('')
const groupId = ref(0)
const billingMode = ref('')
const onlyComparable = ref(false)
const sort = ref<ComparisonSort>('spread')
const page = ref(1)
const pageSize = 12
/** UTable expanded state keyed by row id (the comparison key). */
const expanded = ref<Record<string, boolean>>({})
const statUi = statCardUi, statClass = statCardClass, tableUi = templateTableUi, compactUi = compactTableUi
// Reka select items cannot use '' as a value; "all" maps to a sentinel and back.
const ALL = '__all__'
const platformModel = computed({ get: () => platform.value || ALL, set: (value: string) => { platform.value = value === ALL ? '' : value } })
const billingModel = computed({ get: () => billingMode.value || ALL, set: (value: string) => { billingMode.value = value === ALL ? '' : value } })
const platformItems = computed(() => [{ value: ALL, label: text('全部平台', 'All platforms') }, ...platforms.value.map(item => ({ value: item, label: item }))])
const groupItems = computed(() => [{ value: 0, label: text('全部分组', 'All groups') }, ...groupOptions.value])
const billingItems = computed(() => [
  { value: ALL, label: text('全部计费', 'All billing modes') },
  { value: 'token', label: text('按 token', 'Per token') },
  { value: 'per_request', label: text('按次', 'Per request') },
  { value: 'image', label: text('按图', 'Per image') },
])
const sortItems = computed<Array<{ value: ComparisonSort; label: string }>>(() => [
  { value: 'spread', label: text('价差从大到小', 'Largest spread') },
  { value: 'input', label: text('输入价从低到高', 'Lowest input') },
  { value: 'output', label: text('输出价从低到高', 'Lowest output') },
  { value: 'name', label: text('模型名称', 'Model name') },
])

const offers = computed(() => buildModelOffers(groups.value))
const comparisons = computed(() => compareModels(offers.value))
const filtered = computed(() => sortComparisons(
  filterComparisons(comparisons.value, {
    search: search.value,
    platform: platform.value,
    groupId: groupId.value || null,
    billingMode: billingMode.value,
    onlyComparable: onlyComparable.value,
  }),
  sort.value,
))
const pageCount = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const pageRows = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize).map(toRow))
const rangeStart = computed(() => (filtered.value.length ? (page.value - 1) * pageSize + 1 : 0))
const rangeEnd = computed(() => Math.min(page.value * pageSize, filtered.value.length))
const comparableCount = computed(() => comparisons.value.filter(model => model.offers.length > 1).length)
const groupCount = computed(() => groups.value.length)
const suspectGroups = computed(() => groups.value.filter(group => group.quality_status === 'suspect').length)
const maxSpread = computed(() => comparisons.value.reduce<ModelComparison | null>((best, model) => {
  const value = effectiveSpread(model)
  if (value == null) return best
  if (!best) return model
  return value > (effectiveSpread(best) ?? -1) ? model : best
}, null))
const maxSpreadLabel = computed(() => {
  const model = maxSpread.value
  if (!model) return '—'
  const value = effectiveSpread(model)
  return value == null ? '—' : formatSpread(value)
})
const platforms = computed(() => Array.from(new Set(groups.value.map(group => group.platform))).sort())
const groupOptions = computed(() => groups.value
  .map(group => ({ value: group.id, label: group.name }))
  .sort((a, b) => a.label.localeCompare(b.label)))
const groupRows = computed(() => {
  const accountMap = new Map(adminGroups.value.map(group => [group.id, group]))
  return groups.value
    .map(group => {
      const admin = accountMap.get(group.id)
      return {
        ...group,
        accountCount: admin?.account_count ?? null,
        activeAccountCount: admin?.active_account_count ?? null,
        modelCount: group.models.length,
      }
    })
    .sort((a, b) => a.rate_multiplier - b.rate_multiplier || a.name.localeCompare(b.name))
})

watch([search, platform, groupId, billingMode, onlyComparable, sort], () => {
  page.value = 1
})
watch(pageCount, value => {
  if (page.value > value) page.value = value
})

function effectiveSpread(model: ModelComparison): number | null {
  return model.spread ?? model.requestSpread
}

interface ComparisonRow extends ModelComparison {
  cheapest: ModelOffer | null
  expensive: ModelOffer | null
  spreadValue: number | null
  bestValue: ModelOffer | null
}

function toRow(model: ModelComparison): ComparisonRow {
  return {
    ...model,
    cheapest: model.cheapestInput ?? model.cheapestRequest,
    expensive: model.mostExpensiveInput ?? model.mostExpensiveRequest,
    spreadValue: model.spread ?? model.requestSpread,
    bestValue: model.cheapestBlended ?? model.cheapestRequest,
  }
}

function sortedOffers(model: ModelComparison): ModelOffer[] {
  return [...model.offers].sort((a, b) => {
    const av = a.inputPerMillion ?? a.requestPrice ?? Number.MAX_SAFE_INTEGER
    const bv = b.inputPerMillion ?? b.requestPrice ?? Number.MAX_SAFE_INTEGER
    return av - bv || a.groupName.localeCompare(b.groupName)
  })
}

function formatMoney(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  if (value >= 1) return `$${value.toFixed(2)}`
  if (value >= 0.01) return `$${value.toFixed(3)}`
  return `$${value.toFixed(4)}`
}

function formatRequest(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return `$${value.toFixed(value >= 1 ? 2 : 4)}`
}

function pricePair(offer: ModelOffer): string {
  if (offer.billingMode === 'token') {
    return `${formatMoney(offer.inputPerMillion)} / ${formatMoney(offer.outputPerMillion)}`
  }
  return `${formatRequest(offer.requestPrice)} / ${text('次', 'request')}`
}

function formatSpread(value: number): string {
  return `+${(value * 100).toFixed(value >= 1 ? 0 : 1)}%`
}

function spreadColor(value: number): SemanticColor {
  if (value >= 0.5) return 'error'
  if (value >= 0.2) return 'warning'
  return 'neutral'
}

function rateLabel(value: number): string {
  return `×${Number(value.toFixed(3))}`
}

function billingLabel(mode: string): string {
  if (mode === 'token') return text('按 token', 'Per token')
  if (mode === 'image') return text('按图', 'Per image')
  if (mode === 'video') return text('按视频', 'Per video')
  return text('按次', 'Per request')
}

function qualityLabel(status?: string): string {
  if (status === 'healthy') return text('正常', 'Healthy')
  if (status === 'suspect') return text('异常', 'Suspect')
  return text('未检测', 'Not checked')
}

function qualityColor(status?: string): SemanticColor {
  if (status === 'healthy') return 'success'
  if (status === 'suspect') return 'error'
  return 'neutral'
}

type GroupRow = (typeof groupRows.value)[number]
const groupColumns = computed<TableColumn<GroupRow>[]>(() => [
  { id: 'group', header: text('分组', 'Group') },
  { accessorKey: 'platform', header: text('平台', 'Platform') },
  { id: 'ratio', header: text('倍率', 'Ratio'), cell: ({ row }) => rateLabel(row.original.rate_multiplier) },
  { id: 'accounts', header: text('账号', 'Accounts') },
  { accessorKey: 'modelCount', header: text('模型', 'Models') },
  { id: 'quality', header: text('降智检测', 'Quality') },
  { id: 'type', header: text('类型', 'Type') },
])
const comparisonColumns = computed<TableColumn<ComparisonRow>[]>(() => [
  { id: 'model', header: text('模型', 'Model') },
  { id: 'cheapest', header: text('最低价', 'Cheapest') },
  { id: 'expensive', header: text('最高价', 'Most expensive') },
  { id: 'spread', header: text('价差', 'Spread') },
  { id: 'best', header: text('最划算', 'Best value') },
  { id: 'stable', header: text('最稳', 'Most stable') },
  { id: 'toggle', header: () => '' },
])
const offerColumns = computed<TableColumn<ModelOffer>[]>(() => [
  { id: 'group', header: text('分组', 'Group') },
  { id: 'ratio', header: text('倍率', 'Ratio'), cell: ({ row }) => rateLabel(row.original.groupRate) },
  { id: 'billing', header: text('计费', 'Billing'), cell: ({ row }) => billingLabel(row.original.billingMode) },
  { id: 'price', header: text('输入 / 输出', 'Input / output'), cell: ({ row }) => pricePair(row.original) },
  { id: 'cache', header: text('缓存写 / 读', 'Cache write / read') },
  { id: 'status', header: text('状态', 'Status') },
])

function csvCell(value: unknown): string {
  const raw = value == null ? '' : String(value)
  return /[",\n]/.test(raw) ? `"${raw.replace(/"/g, '""')}"` : raw
}

function exportCsv() {
  const header = [
    'model', 'platform', 'group_id', 'group', 'rate', 'billing_mode',
    'input_per_1m', 'output_per_1m', 'cache_write_per_1m', 'cache_read_per_1m',
    'per_request', 'quality', 'exclusive', 'subscription',
  ]
  const lines = [header.join(',')]
  for (const model of filtered.value) {
    for (const offer of sortedOffers(model)) {
      lines.push([
        offer.model,
        offer.platform,
        offer.groupId,
        offer.groupName,
        offer.groupRate,
        offer.billingMode,
        offer.inputPerMillion ?? '',
        offer.outputPerMillion ?? '',
        offer.cacheWritePerMillion ?? '',
        offer.cacheReadPerMillion ?? '',
        offer.requestPrice ?? '',
        offer.qualityStatus ?? 'unknown',
        offer.isExclusive ? 1 : 0,
        offer.subscriptionType,
      ].map(csvCell).join(','))
    }
  }
  const blob = new Blob([`\uFEFF${lines.join('\r\n')}`], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `model-comparison-${new Date().toISOString().slice(0, 10)}.csv`
  link.click()
  URL.revokeObjectURL(url)
}

async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  const problems: string[] = []
  const [plazaResult, groupsResult] = await Promise.allSettled([
    getAdminModelPlaza(),
    groupsApi.getAll(),
  ])

  if (plazaResult.status === 'fulfilled') {
    groups.value = plazaResult.value.groups
    usingPublicFallback.value = false
  } else {
    try {
      const publicData = await getModelPlaza()
      groups.value = publicData.groups
      usingPublicFallback.value = true
    } catch (err) {
      problems.push(extractApiErrorMessage(err, text('模型定价加载失败', 'Failed to load model pricing')))
    }
  }
  if (groupsResult.status === 'fulfilled') {
    adminGroups.value = groupsResult.value
  } else {
    problems.push(extractApiErrorMessage(groupsResult.reason, text('分组数据加载失败', 'Failed to load groups')))
  }

  error.value = problems.join(' · ')
  loading.value = false
}

onMounted(() => {
  void load()
})
</script>
