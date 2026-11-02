<template>
  <AppLayout>
    <div class="model-comparison space-y-5">
      <AdminPageHeader :description="text('同一模型在各分组的实付价、价差和最稳渠道；价格已乘分组倍率，按 1M token 展示。', 'Effective prices per group for the same model, with spread and the most stable option. Prices already include the group ratio, shown per 1M tokens.')">
        <template #title>
          <span class="inline-flex flex-wrap items-center gap-2">
            {{ text('模型比价', 'Model price comparison') }}
            <span class="badge badge-primary">{{ text('L2 渠道运营', 'L2 channel operations') }}</span>
          </span>
        </template>
        <template #actions>
          <RouterLink to="/admin/groups" class="btn btn-secondary">{{ text('分组与倍率', 'Groups and ratios') }}</RouterLink>
          <RouterLink to="/model-plaza?embedded=1" class="btn btn-secondary">{{ text('模型广场', 'Model plaza') }}</RouterLink>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="load">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
            {{ text('刷新', 'Refresh') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="!offers.length" @click="exportCsv">{{ text('导出 CSV', 'Export CSV') }}</button>
        </template>
      </AdminPageHeader>

      <p v-if="error" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ error }}</p>
      <p v-if="usingPublicFallback" class="rounded-lg bg-primary-50 p-3 text-sm text-primary-800 dark:bg-dark-800 dark:text-primary-300">
        {{ text('当前后端没有管理端全量接口，已回退到公开模型广场；专属分组可能缺失。', 'The admin endpoint is unavailable; fell back to the public model plaza, so exclusive groups may be missing.') }}
      </p>

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" :aria-label="text('比价概览', 'Comparison overview')">
        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('可比模型', 'Comparable models') }}</p>
            <span class="rounded-lg bg-primary-50 p-2 text-primary-600 dark:bg-primary-950/40"><Icon name="chartBar" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums">{{ comparableCount }}</p>
          <p class="mt-2 text-xs text-gray-500">{{ text('至少两个分组同时提供', 'Offered by at least two groups') }}</p>
        </article>
        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('覆盖分组', 'Groups covered') }}</p>
            <span class="rounded-lg bg-sky-50 p-2 text-sky-600 dark:bg-sky-950/40"><Icon name="users" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums">{{ groupCount }}</p>
          <p class="mt-2 text-xs text-gray-500">{{ text('有可售模型的分组', 'Groups with sellable models') }}</p>
        </article>
        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('最大价差', 'Largest spread') }}</p>
            <span class="rounded-lg bg-amber-50 p-2 text-amber-600 dark:bg-amber-950/40"><Icon name="chart" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums">{{ maxSpreadLabel }}</p>
          <p class="mt-2 truncate text-xs text-gray-500">{{ maxSpread ? maxSpread.name : text('暂无可比模型', 'No comparable models yet') }}</p>
        </article>
        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('降智异常分组', 'Suspect groups') }}</p>
            <span class="rounded-lg bg-red-50 p-2 text-red-600 dark:bg-red-950/40"><Icon name="shield" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums" :class="suspectGroups ? 'text-red-600 dark:text-red-400' : ''">{{ suspectGroups }}</p>
          <p class="mt-2 text-xs text-gray-500">{{ text('近 1 小时检测为异常', 'Flagged by quality checks in the last hour') }}</p>
        </article>
      </section>

      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div>
            <h2 class="text-base font-semibold">{{ text('分组倍率速览', 'Group ratios') }}</h2>
            <p class="mt-1 text-xs text-gray-500">{{ text('倍率、账号数和模型数一眼对齐；价格页按生效倍率计算。', 'Ratios, account counts and model counts in one table. Effective prices use these ratios.') }}</p>
          </div>
          <span class="text-xs text-gray-500">{{ groupRows.length }} {{ text('个分组', 'groups') }}</span>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[820px] text-left text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800">
              <tr>
                <th class="px-5 py-3">{{ text('分组', 'Group') }}</th>
                <th class="px-4 py-3">{{ text('平台', 'Platform') }}</th>
                <th class="px-4 py-3">{{ text('倍率', 'Ratio') }}</th>
                <th class="px-4 py-3">{{ text('账号', 'Accounts') }}</th>
                <th class="px-4 py-3">{{ text('模型', 'Models') }}</th>
                <th class="px-4 py-3">{{ text('降智检测', 'Quality') }}</th>
                <th class="px-5 py-3">{{ text('类型', 'Type') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in groupRows" :key="row.id" class="border-t border-gray-100 dark:border-dark-700">
                <td class="px-5 py-3">
                  <p class="font-medium">{{ row.name }}</p>
                  <p class="mt-0.5 text-xs text-gray-500">#{{ row.id }}</p>
                </td>
                <td class="px-4 py-3">{{ row.platform }}</td>
                <td class="px-4 py-3 tabular-nums">{{ rateLabel(row.rate_multiplier) }}</td>
                <td class="px-4 py-3 tabular-nums">
                  <template v-if="row.activeAccountCount != null">{{ row.activeAccountCount }} / {{ row.accountCount ?? '—' }}</template>
                  <span v-else class="text-gray-400">—</span>
                </td>
                <td class="px-4 py-3 tabular-nums">{{ row.modelCount }}</td>
                <td class="px-4 py-3"><span class="badge" :class="qualityClass(row.quality_status)">{{ qualityLabel(row.quality_status) }}</span></td>
                <td class="px-5 py-3">
                  <span v-if="row.subscription_type === 'subscription'" class="badge badge-purple">{{ text('订阅', 'Subscription') }}</span>
                  <span v-else-if="row.is_exclusive" class="badge badge-warning">{{ text('专属', 'Exclusive') }}</span>
                  <span v-else class="badge badge-gray">{{ text('公开', 'Public') }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div>
            <h2 class="text-base font-semibold">{{ text('模型比价', 'Model comparison') }}</h2>
            <p class="mt-1 text-xs text-gray-500">{{ text('最低价按输入价排序；价差 = 最高输入价 / 最低输入价 − 1。', 'Cheapest is ranked by input price; spread = highest input / lowest input − 1.') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <input v-model="search" class="input w-44 text-sm" :placeholder="text('搜索模型', 'Search model')" :aria-label="text('搜索模型', 'Search model')" />
            <select v-model="platform" class="input w-auto text-sm" :aria-label="text('平台', 'Platform')">
              <option value="">{{ text('全部平台', 'All platforms') }}</option>
              <option v-for="item in platforms" :key="item" :value="item">{{ item }}</option>
            </select>
            <select v-model.number="groupId" class="input w-auto text-sm" :aria-label="text('分组', 'Group')">
              <option :value="0">{{ text('全部分组', 'All groups') }}</option>
              <option v-for="item in groupOptions" :key="item.value" :value="item.value">{{ item.label }}</option>
            </select>
            <select v-model="billingMode" class="input w-auto text-sm" :aria-label="text('计费方式', 'Billing mode')">
              <option value="">{{ text('全部计费', 'All billing modes') }}</option>
              <option value="token">{{ text('按 token', 'Per token') }}</option>
              <option value="per_request">{{ text('按次', 'Per request') }}</option>
              <option value="image">{{ text('按图', 'Per image') }}</option>
            </select>
            <select v-model="sort" class="input w-auto text-sm" :aria-label="text('排序', 'Sort')">
              <option value="spread">{{ text('价差从大到小', 'Largest spread') }}</option>
              <option value="input">{{ text('输入价从低到高', 'Lowest input') }}</option>
              <option value="output">{{ text('输出价从低到高', 'Lowest output') }}</option>
              <option value="name">{{ text('模型名称', 'Model name') }}</option>
            </select>
            <label class="flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-300">
              <input v-model="onlyComparable" type="checkbox" />
              {{ text('只看多分组', 'Multi-group only') }}
            </label>
          </div>
        </div>

        <div class="overflow-x-auto">
          <table v-if="pageRows.length" class="w-full min-w-[1120px] text-left text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800">
              <tr>
                <th class="px-5 py-3">{{ text('模型', 'Model') }}</th>
                <th class="px-4 py-3">{{ text('最低价', 'Cheapest') }}</th>
                <th class="px-4 py-3">{{ text('最高价', 'Most expensive') }}</th>
                <th class="px-4 py-3">{{ text('价差', 'Spread') }}</th>
                <th class="px-4 py-3">{{ text('最划算', 'Best value') }}</th>
                <th class="px-4 py-3">{{ text('最稳', 'Most stable') }}</th>
                <th class="px-5 py-3"></th>
              </tr>
            </thead>
            <tbody>
              <template v-for="row in pageRows" :key="row.key">
                <tr class="border-t border-gray-100 align-top dark:border-dark-700">
                  <td class="px-5 py-3">
                    <p class="font-medium">{{ row.name }}</p>
                    <p class="mt-0.5 text-xs text-gray-500">
                      {{ row.platform }} · {{ row.offers.length }} {{ text('个分组', 'groups') }}
                      <span v-if="row.multiTier" class="badge badge-gray ml-1">{{ text('多档', 'Tiered') }}</span>
                    </p>
                  </td>
                  <td class="px-4 py-3">
                    <template v-if="row.cheapest">
                      <p class="font-medium">{{ row.cheapest.groupName }}</p>
                      <p class="mt-0.5 text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ pricePair(row.cheapest) }}</p>
                      <p class="mt-0.5 text-[11px] text-gray-400">{{ text('倍率', 'Ratio') }} {{ rateLabel(row.cheapest.groupRate) }}</p>
                    </template>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td class="px-4 py-3">
                    <template v-if="row.expensive">
                      <p class="font-medium">{{ row.expensive.groupName }}</p>
                      <p class="mt-0.5 text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ pricePair(row.expensive) }}</p>
                    </template>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td class="px-4 py-3">
                    <span v-if="row.spreadValue != null" class="badge" :class="spreadClass(row.spreadValue)">{{ formatSpread(row.spreadValue) }}</span>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td class="px-4 py-3">
                    <template v-if="row.bestValue">
                      <p class="font-medium">{{ row.bestValue.groupName }}</p>
                      <p class="mt-0.5 text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ pricePair(row.bestValue) }}</p>
                      <p class="mt-1 text-[11px] text-gray-400">{{ text('综合价最低', 'Lowest blended price') }}</p>
                    </template>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td class="px-4 py-3">
                    <template v-if="row.bestStable">
                      <p class="font-medium">{{ row.bestStable.groupName }}</p>
                      <p class="mt-0.5 text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ pricePair(row.bestStable) }}</p>
                      <span class="badge badge-success mt-1">{{ text('检测正常', 'Healthy') }}</span>
                    </template>
                    <span v-else class="text-xs text-gray-400">{{ text('无检测正常分组', 'No healthy group') }}</span>
                  </td>
                  <td class="px-5 py-3 text-right">
                    <button type="button" class="inline-flex items-center gap-1 text-xs text-primary-600" @click="toggle(row.key)">
                      {{ expanded.has(row.key) ? text('收起', 'Collapse') : text('展开', 'Expand') }}
                      <Icon :name="expanded.has(row.key) ? 'chevronDown' : 'chevronRight'" size="sm" />
                    </button>
                  </td>
                </tr>
                <tr v-if="expanded.has(row.key)" class="bg-gray-50/60 dark:bg-dark-800/40">
                  <td colspan="7" class="px-5 py-4">
                    <div class="overflow-x-auto rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
                      <table class="w-full min-w-[860px] text-left text-xs">
                        <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800">
                          <tr>
                            <th class="px-4 py-2">{{ text('分组', 'Group') }}</th>
                            <th class="px-3 py-2">{{ text('倍率', 'Ratio') }}</th>
                            <th class="px-3 py-2">{{ text('计费', 'Billing') }}</th>
                            <th class="px-3 py-2">{{ text('输入 / 输出', 'Input / output') }}</th>
                            <th class="px-3 py-2">{{ text('缓存写 / 读', 'Cache write / read') }}</th>
                            <th class="px-3 py-2">{{ text('状态', 'Status') }}</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr v-for="offer in sortedOffers(row)" :key="offer.key" class="border-t border-gray-100 dark:border-dark-700">
                            <td class="px-4 py-2">
                              <p class="font-medium">{{ offer.groupName }}</p>
                              <p class="text-[11px] text-gray-500">#{{ offer.groupId }} · {{ offer.platform }}</p>
                            </td>
                            <td class="px-3 py-2 tabular-nums">{{ rateLabel(offer.groupRate) }}</td>
                            <td class="px-3 py-2">{{ billingLabel(offer.billingMode) }}</td>
                            <td class="px-3 py-2 tabular-nums">{{ pricePair(offer) }}</td>
                            <td class="px-3 py-2 tabular-nums">
                              <template v-if="offer.billingMode === 'token'">{{ formatMoney(offer.cacheWritePerMillion) }} / {{ formatMoney(offer.cacheReadPerMillion) }}</template>
                              <span v-else class="text-gray-400">—</span>
                            </td>
                            <td class="px-3 py-2">
                              <span class="badge" :class="qualityClass(offer.qualityStatus)">{{ qualityLabel(offer.qualityStatus) }}</span>
                              <span v-if="offer.isExclusive" class="badge badge-warning ml-1">{{ text('专属', 'Exclusive') }}</span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
          <div v-else class="p-10 text-center text-sm text-gray-500">
            {{ loading ? text('加载中…', 'Loading…') : text('没有匹配的模型。', 'No models match the filters.') }}
          </div>
        </div>

        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 px-5 py-3 text-xs text-gray-500 dark:border-dark-700">
          <span>{{ text('显示', 'Showing') }} {{ rangeStart }}–{{ rangeEnd }} / {{ filtered.length }}</span>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-secondary" :disabled="page <= 1" @click="page--">{{ text('上一页', 'Previous') }}</button>
            <span>{{ page }} / {{ pageCount }}</span>
            <button type="button" class="btn btn-secondary" :disabled="page >= pageCount" @click="page++">{{ text('下一页', 'Next') }}</button>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import Icon from '@/components/icons/Icon.vue'
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
const expanded = reactive(new Set<string>())

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

function spreadClass(value: number): string {
  if (value >= 0.5) return 'badge-danger'
  if (value >= 0.2) return 'badge-warning'
  return 'badge-gray'
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

function qualityClass(status?: string): string {
  if (status === 'healthy') return 'badge-success'
  if (status === 'suspect') return 'badge-danger'
  return 'badge-gray'
}

function toggle(key: string) {
  if (expanded.has(key)) expanded.delete(key)
  else expanded.add(key)
}

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
