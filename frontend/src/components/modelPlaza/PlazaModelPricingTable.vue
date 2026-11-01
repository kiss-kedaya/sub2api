<template>
  <UTable
    :data="rows"
    :columns="columns"
    :get-row-id="(row: PlazaRow) => row.key"
    :ui="{
      base: 'w-full min-w-[1000px] table-auto tabular-nums',
      thead: 'bg-elevated/50',
      th: 'px-3 py-2 text-[11px] font-medium uppercase tracking-wide text-muted',
      tbody: '[&>tr]:hover:bg-elevated/40',
      td: 'px-3 py-2.5 align-middle text-xs'
    }"
  >
    <!-- 模型名 + 非 token 计费模式徽章;分时时段行额外标注时段 -->
    <template #model-cell="{ row: { original: { model: m, period } } }">
      <div class="flex flex-wrap items-center gap-1.5">
        <span class="text-sm font-medium text-highlighted">{{ m.name }}</span>
        <!-- 时段徽章紧跟模型名,其余徽章排在后面,空间不足时先换行的是它们 -->
        <UBadge
          v-if="period"
          color="neutral"
          variant="subtle"
          size="sm"
          class="whitespace-nowrap font-mono"
          :title="timePricingRowHint(m)"
        >
          <span v-if="m.time_pricing?.weekdays_only" class="mr-1 font-sans">{{
            t('modelPlaza.table.timePricingWeekdays')
          }}</span>
          {{ formatTimeWindow(period) }}
        </UBadge>
        <UBadge
          v-if="platform && m.platform !== platform"
          :label="platformLabel(m.platform)"
          color="neutral"
          variant="outline"
          size="sm"
        />
        <UBadge
          v-if="billingMode(m) !== BILLING_MODE_TOKEN"
          :label="billingModeLabel(m)"
          color="neutral"
          variant="subtle"
          size="sm"
        />
        <UBadge
          v-if="m.long_context_basis === 'marginal'"
          :label="t('modelPlaza.table.marginalBadge')"
          :title="t('modelPlaza.table.tierHintMarginal')"
          color="neutral"
          variant="subtle"
          size="sm"
        />
        <UBadge
          v-if="m.pricing?.max_reasoning_effort_multiplier"
          :label="t('modelPlaza.table.maxReasoningMultiplierBadge', { multiplier: m.pricing.max_reasoning_effort_multiplier })"
          :title="t('modelPlaza.table.maxReasoningMultiplierHint', { multiplier: m.pricing.max_reasoning_effort_multiplier })"
          color="warning"
          variant="subtle"
          size="sm"
        />
      </div>
    </template>

    <!-- token 计费:输入 / 输出 / 缓存(写/读),有阶梯时每档一行;档位标签只放输入列,其余列按行对齐 -->
    <template #paidInput-cell="{ row: { original: { model: m, period } } }">
      <template v-if="billingMode(m) === BILLING_MODE_TOKEN">
        <template v-if="tokenIntervals(m).length">
          <div
            v-for="(iv, idx) in tokenIntervals(m)"
            :key="idx"
            class="whitespace-nowrap text-xs leading-5"
          >
            <span class="mr-1 font-sans font-normal text-dimmed" :title="tierHint(m)">{{ tierLabel(iv) }}</span>
            {{ paidPerMillion(iv.input_price, period) }}
          </div>
        </template>
        <template v-else>{{ paidPerMillion(m.pricing?.input_price, period) }}</template>
      </template>
      <!-- 按次 / 按图片计费:实付区整体合并,阶梯芯片或单一按次价 -->
      <template v-else>
        <div v-if="requestIntervals(m).length" class="flex flex-wrap items-center gap-1.5">
          <UBadge
            v-for="(iv, idx) in requestIntervals(m)"
            :key="idx"
            color="neutral"
            variant="subtle"
            class="gap-1 font-mono"
          >
            <span class="font-sans text-dimmed">{{ tierLabel(iv) }}</span>
            {{ paidRequestPrice(m, iv.per_request_price)
            }}<span class="font-sans text-dimmed">{{ perUnitSuffix(m) }}</span>
          </UBadge>
        </div>
        <template v-else-if="m.pricing?.per_request_price != null">
          <span class="font-mono font-semibold text-highlighted">
            {{ paidRequestPrice(m, m.pricing.per_request_price) }}
          </span>
          <span class="ml-1 text-xs text-dimmed">{{ perUnitSuffix(m) }}</span>
        </template>
        <span v-else class="text-dimmed">-</span>
      </template>
    </template>

    <template #paidOutput-cell="{ row: { original: { model: m, period } } }">
      <template v-if="billingMode(m) === BILLING_MODE_TOKEN">
        <template v-if="tokenIntervals(m).length">
          <div
            v-for="(iv, idx) in tokenIntervals(m)"
            :key="idx"
            class="whitespace-nowrap text-xs leading-5"
            :title="tierHint(m)"
          >
            {{ paidPerMillion(iv.output_price, period) }}
          </div>
        </template>
        <template v-else>{{ paidPerMillion(m.pricing?.output_price, period) }}</template>
      </template>
    </template>

    <template #paidCache-cell="{ row: { original: { model: m, period } } }">
      <template v-if="billingMode(m) === BILLING_MODE_TOKEN">
        <template v-if="hasTierCachePricing(tokenIntervals(m))">
          <div
            v-for="(iv, idx) in tokenIntervals(m)"
            :key="idx"
            class="whitespace-nowrap font-mono text-xs leading-5 text-default"
            :title="tierHint(m)"
          >
            <template v-if="iv.cache_write_price != null || iv.cache_write_1h_price != null || iv.cache_read_price != null">
              <span class="font-sans font-normal text-dimmed">{{ t('modelPlaza.table.cacheWriteShort') }}</span>
              {{ paidPerMillion(iv.cache_write_price, period) }}
              <template v-if="iv.cache_write_1h_price != null"
                ><span class="font-sans font-normal text-dimmed"> (1h </span>{{ paidPerMillion(iv.cache_write_1h_price, period)
                }}<span class="font-sans font-normal text-dimmed">)</span></template
              >
              <span class="ml-1 font-sans font-normal text-dimmed">{{ t('modelPlaza.table.cacheReadShort') }}</span>
              {{ paidPerMillion(iv.cache_read_price, period) }}
            </template>
            <span v-else class="text-dimmed">-</span>
          </div>
        </template>
        <div v-else-if="hasCachePricing(m)" class="space-y-0.5 font-mono text-xs text-default">
          <div>
            <span class="mr-1 font-sans font-normal text-dimmed">{{ t('modelPlaza.table.cacheWrite') }}</span>
            {{ paidPerMillion(m.pricing?.cache_write_price, period)
            }}<template v-if="m.pricing?.cache_write_1h_price != null"
              ><span class="font-sans font-normal text-dimmed"> (1h </span>{{ paidPerMillion(m.pricing.cache_write_1h_price, period)
              }}<span class="font-sans font-normal text-dimmed">)</span></template
            >
          </div>
          <div>
            <span class="mr-1 font-sans font-normal text-dimmed">{{ t('modelPlaza.table.cacheRead') }}</span>
            {{ paidPerMillion(m.pricing?.cache_read_price, period) }}
          </div>
        </div>
        <span v-else class="text-dimmed">-</span>
      </template>
    </template>

    <!-- 官方价格(参考价,不乘倍率;官方有阶梯时每档一行) -->
    <template #officialInput-cell="{ row: { original: { model: m } } }">
      <template v-if="officialIntervals(m).length">
        <div v-for="(iv, idx) in officialIntervals(m)" :key="idx" class="whitespace-nowrap leading-5">
          <span class="mr-1 font-sans text-dimmed" :title="t('modelPlaza.table.tierHint')">{{ tierLabel(iv) }}</span>
          {{ official(iv.input_price) }}
        </div>
      </template>
      <template v-else>{{ official(m.official_pricing?.input_price) }}</template>
    </template>

    <template #officialOutput-cell="{ row: { original: { model: m } } }">
      <template v-if="officialIntervals(m).length">
        <div
          v-for="(iv, idx) in officialIntervals(m)"
          :key="idx"
          class="whitespace-nowrap leading-5"
          :title="t('modelPlaza.table.tierHint')"
        >
          {{ official(iv.output_price) }}
        </div>
      </template>
      <template v-else>{{ official(m.official_pricing?.output_price) }}</template>
    </template>

    <template #officialCache-cell="{ row: { original: { model: m } } }">
      <template v-if="hasTierCachePricing(officialIntervals(m))">
        <div
          v-for="(iv, idx) in officialIntervals(m)"
          :key="idx"
          class="whitespace-nowrap font-mono text-xs leading-5 text-muted"
          :title="t('modelPlaza.table.tierHint')"
        >
          <template v-if="iv.cache_write_price != null || iv.cache_write_1h_price != null || iv.cache_read_price != null">
            <span class="font-sans text-dimmed">{{ t('modelPlaza.table.cacheWriteShort') }}</span>
            {{ official(iv.cache_write_price) }}
            <template v-if="iv.cache_write_1h_price != null"
              ><span class="font-sans text-dimmed"> (1h </span>{{ official(iv.cache_write_1h_price)
              }}<span class="font-sans text-dimmed">)</span></template
            >
            <span class="ml-1 font-sans text-dimmed">{{ t('modelPlaza.table.cacheReadShort') }}</span>
            {{ official(iv.cache_read_price) }}
          </template>
          <span v-else class="text-dimmed">-</span>
        </div>
      </template>
      <div
        v-else-if="m.official_pricing && hasOfficialCache(m.official_pricing)"
        class="space-y-0.5 font-mono text-xs text-muted"
      >
        <div>
          <span class="mr-1 font-sans font-normal text-dimmed">{{ t('modelPlaza.table.cacheWrite') }}</span>
          {{ official(m.official_pricing.cache_write_price)
          }}<template v-if="m.official_pricing.cache_write_1h_price != null"
            ><span class="font-sans text-dimmed"> (1h </span>{{ official(m.official_pricing.cache_write_1h_price)
            }}<span class="font-sans text-dimmed">)</span></template
          >
        </div>
        <div>
          <span class="mr-1 font-sans font-normal text-dimmed">{{ t('modelPlaza.table.cacheRead') }}</span>
          {{ official(m.official_pricing.cache_read_price) }}
        </div>
      </div>
      <span v-else class="text-dimmed">-</span>
    </template>

    <!-- 折扣倍率(分时时段行展示 生效倍率×时段倍率;生图独立倍率行展示独立倍率;专属倍率划线展示原倍率) -->
    <template #rate-cell="{ row: { original: { model: m, period } } }">
      <span
        v-if="period"
        class="font-bold text-primary"
        :title="t('modelPlaza.table.timePricingRateHint', { rate: effectiveRate, multiplier: period.multiplier })"
        >{{ periodRate(period) }}x</span
      >
      <span v-else-if="usesIndependentImageRate(m)" class="font-bold text-highlighted">{{ requestRate(m) }}x</span>
      <template v-else-if="hasCustomRate">
        <span class="mr-1 text-dimmed line-through">{{ rateMultiplier }}x</span>
        <span class="font-bold text-primary">{{ effectiveRate }}x</span>
      </template>
      <span v-else class="font-bold text-highlighted">{{ effectiveRate }}x</span>
    </template>
  </UTable>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { formatScaled, resolveIntervalPrices } from '@/utils/pricing'
import { platformLabel } from '@/utils/platformColors'
import {
  BILLING_MODE_TOKEN,
  BILLING_MODE_IMAGE,
  type BillingMode
} from '@/constants/channel'
import type { PlazaModel, PlazaTimePricingPeriod } from '@/api/modelPlaza'
import type { UserPricingInterval } from '@/api/channels'

const props = defineProps<{
  models: PlazaModel[]
  /** 分组平台;组合分组里与之不同的模型带平台徽章。 */
  platform?: string
  /** 分组默认倍率。 */
  rateMultiplier: number
  /** 用户专属倍率;与默认不同,实付价按此计算并划线展示原倍率。 */
  userRateMultiplier?: number | null
  /** 生图独立倍率:true 时图片计费模型的实付倍率取 imageRateMultiplier,不取分组/专属倍率。 */
  imageRateIndependent?: boolean
  imageRateMultiplier?: number | null
  /**
   * 高峰窗口描述(含倍率与服务器时区标注),空串/缺省 = 分组未启用高峰。
   * 表格所有价格均为不含高峰因子的口径,该窗口仅用于分时时段行的 tooltip 披露:
   * 与高峰重叠的部分实付还会再乘高峰倍率。
   */
  peakWindow?: string
  peakRateMultiplier?: number | null
}>()

const { t } = useI18n()

const PER_MILLION = 1_000_000

/** 表格行:每个模型一行标准价;配置了分时倍率的模型再按时段各加一行。 */
interface PlazaRow {
  model: PlazaModel
  period: PlazaTimePricingPeriod | null
  key: string
}

/** 实付区与官方区两级表头:组标题 + 「$ / 1M token」单位。 */
function groupHeader(labelKey: string, strong: boolean) {
  return () => h('div', { class: ['border-b pb-1.5 normal-case tracking-normal', strong ? 'border-accented' : 'border-default'] }, [
    h('span', { class: ['text-xs font-semibold', strong ? 'text-highlighted' : 'text-muted'] }, t(labelKey)),
    h('span', { class: 'ml-1 text-[11px] font-normal text-dimmed' }, t('modelPlaza.table.unitPerMillion'))
  ])
}

const isTokenRow = (row: PlazaRow) => billingMode(row.model) === BILLING_MODE_TOKEN
const PAID_TD = 'font-mono text-highlighted'
const OFFICIAL_TD = 'font-mono text-muted'

// 按次/按图片计费的行:实付「输入」格横跨三列,后两格隐藏(列数与表头保持一致)。
const columns = computed<TableColumn<PlazaRow>[]>(() => [
  {
    id: 'model',
    header: t('modelPlaza.table.model'),
    meta: { class: { th: 'w-[25%] pl-5 pr-4 border-e border-default', td: 'whitespace-normal pl-5 pr-4 border-e border-default' } }
  },
  {
    id: 'paid',
    header: groupHeader('modelPlaza.table.paidPrice', true),
    meta: { class: { th: 'pt-2.5 pb-0 text-center' } },
    columns: [
      {
        id: 'paidInput',
        header: t('modelPlaza.table.input'),
        meta: {
          class: { th: 'w-[11%]', td: (cell) => (isTokenRow(cell.row.original) ? `${PAID_TD} font-semibold` : '') },
          colspan: { td: (cell) => (isTokenRow(cell.row.original) ? '1' : '3') }
        }
      },
      {
        id: 'paidOutput',
        header: t('modelPlaza.table.output'),
        meta: { class: { th: 'w-[9%]', td: (cell) => (isTokenRow(cell.row.original) ? `${PAID_TD} font-semibold` : 'hidden') } }
      },
      {
        id: 'paidCache',
        header: t('modelPlaza.table.cache'),
        meta: { class: { th: 'w-[14%]', td: (cell) => (isTokenRow(cell.row.original) ? '' : 'hidden') } }
      }
    ]
  },
  {
    id: 'official',
    header: groupHeader('modelPlaza.table.officialPrice', false),
    meta: { class: { th: 'pt-2.5 pb-0 text-center border-s border-default' } },
    columns: [
      { id: 'officialInput', header: t('modelPlaza.table.input'), meta: { class: { th: 'w-[11%] border-s border-default', td: `${OFFICIAL_TD} border-s border-default` } } },
      { id: 'officialOutput', header: t('modelPlaza.table.output'), meta: { class: { th: 'w-[8%]', td: OFFICIAL_TD } } },
      { id: 'officialCache', header: t('modelPlaza.table.cache'), meta: { class: { th: 'w-[14%]' } } }
    ]
  },
  {
    id: 'rate',
    header: t('modelPlaza.table.rate'),
    meta: { class: { th: 'w-[8%] pl-3 pr-5 text-right border-s border-default', td: 'pl-3 pr-5 text-right font-mono border-s border-default' } }
  }
])

/**
 * 展示顺序:
 * 1. token 计费的排在前,按图/按次计费的沉到末尾——它们的官方 token 价与实付的按张/按次价不同量纲,混排无意义;
 * 2. 组内按官方输出价从高到低,无官方价的排最后;
 * 3. 同价按名称降序(新版本号在前,如 gpt-5.6 先于 gpt-5.5)。
 */
const sortedModels = computed(() => {
  return [...props.models].sort((a, b) => {
    const ta = billingMode(a) === BILLING_MODE_TOKEN
    const tb = billingMode(b) === BILLING_MODE_TOKEN
    if (ta !== tb) return ta ? -1 : 1
    const pa = a.official_pricing?.output_price ?? null
    const pb = b.official_pricing?.output_price ?? null
    if (pa != null && pb != null && pa !== pb) return pb - pa
    if (pa != null && pb == null) return -1
    if (pa == null && pb != null) return 1
    return b.name.localeCompare(a.name)
  })
})

const effectiveRate = computed(() => props.userRateMultiplier ?? props.rateMultiplier)
const hasCustomRate = computed(
  () => props.userRateMultiplier != null && props.userRateMultiplier !== props.rateMultiplier
)

function billingMode(m: PlazaModel): BillingMode {
  return (m.pricing?.billing_mode || BILLING_MODE_TOKEN) as BillingMode
}

function billingModeLabel(m: PlazaModel): string {
  return billingMode(m) === BILLING_MODE_IMAGE
    ? t('modelPlaza.table.perImage')
    : t('modelPlaza.table.perRequest')
}

/** 价格统一保底 2 位小数,更长的有效小数原样保留。 */
const MIN_DECIMALS = 2

const rows = computed<PlazaRow[]>(() =>
  sortedModels.value.flatMap((m) => {
    const base: PlazaRow = { model: m, period: null, key: `${m.platform}:${m.name}` }
    const periodRows = timePeriods(m).map<PlazaRow>((p, idx) => ({
      model: m,
      period: p,
      key: `${m.platform}:${m.name}:${idx}`
    }))
    return [base, ...periodRows]
  })
)

/** 时段行的生效倍率 = 生效倍率 × 时段倍率(去掉浮点噪声)。 */
function periodRate(period: PlazaTimePricingPeriod): number {
  return Math.round(effectiveRate.value * period.multiplier * 1000) / 1000
}

/** 实付价 = 渠道单价 × 生效倍率(时段行再乘时段倍率),按 $/1M token 展示。 */
function paidPerMillion(value: number | null | undefined, period: PlazaTimePricingPeriod | null = null): string {
  if (value == null) return '-'
  const rate = period ? periodRate(period) : effectiveRate.value
  return formatScaled(value * rate, PER_MILLION, MIN_DECIMALS)
}

/** 图片计费模型且分组开启生图独立倍率:实付倍率取独立倍率,与计费口径一致。 */
function usesIndependentImageRate(m: PlazaModel): boolean {
  return billingMode(m) === BILLING_MODE_IMAGE && props.imageRateIndependent === true
}

/** 按次/按图片行的生效倍率。 */
function requestRate(m: PlazaModel): number {
  return usesIndependentImageRate(m) ? (props.imageRateMultiplier ?? 1) : effectiveRate.value
}

/** 按次 / 按图片单价(乘该行生效倍率,不换算 1M)。 */
function paidRequestPrice(m: PlazaModel, value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value * requestRate(m), 1, MIN_DECIMALS)
}

/** 官方参考价不乘倍率。 */
function official(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value, PER_MILLION, MIN_DECIMALS)
}

/** 非 token 计费的单位后缀:按图片 → “/ 张”,按次 → “/ 次”。 */
function perUnitSuffix(m: PlazaModel): string {
  return billingMode(m) === BILLING_MODE_IMAGE
    ? t('modelPlaza.table.perUnitImage')
    : t('modelPlaza.table.perUnitRequest')
}

function hasCachePricing(m: PlazaModel): boolean {
  return m.pricing?.cache_write_price != null || m.pricing?.cache_write_1h_price != null || m.pricing?.cache_read_price != null
}

function hasOfficialCache(o: NonNullable<PlazaModel['official_pricing']>): boolean {
  return o.cache_write_price != null || o.cache_read_price != null || o.cache_write_1h_price != null
}

/** 分时倍率时段(后端只给出倍率 ≠ 1 的时段,已升序)。 */
function timePeriods(m: PlazaModel): PlazaTimePricingPeriod[] {
  return m.time_pricing?.periods ?? []
}

/**
 * 时段行 tooltip:仅工作日生效的配置换用带周末回落说明的文案;
 * 分组启用高峰倍率时追加披露——本行价格不含高峰因子,与高峰窗口重叠的部分实付再乘高峰倍率。
 */
function timePricingRowHint(m: PlazaModel): string {
  const key = m.time_pricing?.weekdays_only
    ? 'modelPlaza.table.timePricingRowHintWeekdays'
    : 'modelPlaza.table.timePricingRowHint'
  let hint = t(key, { timezone: m.time_pricing?.timezone })
  if (props.peakWindow) {
    hint += t('modelPlaza.table.timePricingRowHintPeak', {
      window: props.peakWindow,
      multiplier: props.peakRateMultiplier ?? 1
    })
  }
  return hint
}

/** “00:30–08:30”;整分钟的 HH:mm:ss 省略秒。 */
function formatTimeWindow(p: PlazaTimePricingPeriod): string {
  const clock = (v: string) => v.replace(/^(\d{2}:\d{2}):00$/, '$1')
  return `${clock(p.start_time)}–${clock(p.end_time)}`
}

/** 上下文档位按下限升序展示(后端已升序,此处兜底)。 */
function sortByContext(intervals: UserPricingInterval[]): UserPricingInterval[] {
  return [...intervals].sort((a, b) => a.min_tokens - b.min_tokens)
}

/** token 模式的阶梯定价(内联进输入/输出/缓存列)。 */
function tokenIntervals(m: PlazaModel): UserPricingInterval[] {
  return sortByContext(m.pricing?.intervals ?? []).map(iv => resolveIntervalPrices(iv, m.pricing!))
}

/** 官方阶梯(后端按目录规则合成,不受分组开关影响)。 */
function officialIntervals(m: PlazaModel): UserPricingInterval[] {
  return sortByContext(m.official_pricing?.intervals ?? [])
}

/** 任一档带缓存价才按档渲染缓存列;否则沿用平价的写入/读取两行。 */
function hasTierCachePricing(intervals: UserPricingInterval[]): boolean {
  return intervals.some((iv) =>
    iv.cache_write_price != null || iv.cache_write_1h_price != null || iv.cache_read_price != null ||
    iv.cache_write_multiplier != null || iv.cache_read_multiplier != null
  )
}

/** 档位说明:整单按档计价,或(平台旧规则)仅超出部分按档计价。 */
function tierHint(m: PlazaModel): string {
  return m.long_context_basis === 'marginal'
    ? t('modelPlaza.table.tierHintMarginal')
    : t('modelPlaza.table.tierHint')
}

/** 按次/按图模式的阶梯定价(仅保留配了按次价的档位)。 */
function requestIntervals(m: PlazaModel): UserPricingInterval[] {
  return (m.pricing?.intervals ?? []).filter((iv) => iv.per_request_price != null)
}

/**
 * 档位标签:优先后端/管理员给出的 tier_label,否则按区间生成统一形态——
 * 有上限为「≤上限」,末档为「>下限」;档位升序排列,相邻的 ≤100K / ≤200K 即表示 (100K,200K]。
 */
function tierLabel(iv: UserPricingInterval): string {
  if (iv.tier_label) return iv.tier_label
  const { min_tokens: min, max_tokens: max } = iv
  return max == null ? `>${formatTokenCount(min)}` : `≤${formatTokenCount(max)}`
}

function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${trimZero(n / 1_000_000)}M`
  if (n >= 1_000) return `${trimZero(n / 1_000)}K`
  return String(n)
}

function trimZero(n: number): string {
  return String(Math.round(n * 100) / 100)
}
</script>
