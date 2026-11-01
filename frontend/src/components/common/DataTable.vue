<template>
  <div v-if="!isDesktopViewport" class="space-y-3">
    <template v-if="loading">
      <div v-for="i in 5" :key="i" class="rounded-lg bg-default p-4 ring ring-default">
        <div class="space-y-3">
          <div v-for="column in dataColumns" :key="column.key" class="flex justify-between">
            <div class="h-4 w-20 animate-pulse rounded-sm bg-elevated"></div>
            <div class="h-4 w-32 animate-pulse rounded-sm bg-elevated"></div>
          </div>
          <div v-if="hasActionsColumn" class="border-t border-default pt-3">
            <div class="h-8 w-full animate-pulse rounded-sm bg-elevated"></div>
          </div>
        </div>
      </div>
    </template>

    <template v-else-if="!data || data.length === 0">
      <div class="rounded-lg bg-default px-4 py-12 text-center ring ring-default">
        <slot name="empty">
          <div class="flex flex-col items-center gap-2">
            <UIcon name="i-lucide-inbox" class="size-8 text-dimmed" />
            <p class="text-sm text-muted">{{ t('empty.noData') }}</p>
          </div>
        </slot>
      </div>
    </template>

    <template v-else>
      <div
        v-if="sortableColumns.length > 0 || selectable"
        class="flex flex-wrap items-center justify-between gap-3 px-1"
        data-test="mobile-list-toolbar"
      >
        <!-- 手机端卡片没有表头，补一个排序条：选字段 + 切升降序，和服务端/本地排序共用同一套状态 -->
        <div v-if="sortableColumns.length > 0" class="flex min-w-0 items-center gap-2" data-test="mobile-sort-bar">
          <span class="shrink-0 text-xs font-medium text-muted">{{ t('common.sortBy') }}</span>
          <Select
            class="w-40"
            :model-value="sortKey || ''"
            :options="mobileSortOptions"
            :placeholder="t('common.sortBy')"
            @change="onMobileSortSelect"
          />
          <UButton
            color="neutral"
            variant="outline"
            square
            class="shrink-0"
            :icon="sortOrder === 'desc' ? 'i-lucide-arrow-down-wide-narrow' : 'i-lucide-arrow-up-narrow-wide'"
            :disabled="!sortKey"
            :title="sortOrder === 'asc' ? t('common.sortAscending') : t('common.sortDescending')"
            :aria-label="sortOrder === 'asc' ? t('common.sortAscending') : t('common.sortDescending')"
            data-test="mobile-sort-direction"
            @click="toggleMobileSortDirection"
          />
        </div>
        <UCheckbox
          v-if="selectable"
          class="ml-auto"
          :label="t('common.selectAll')"
          :model-value="headerCheckboxState"
          data-test="select-all-mobile"
          @update:model-value="toggleAllVisible(!!$event)"
        />
      </div>
      <div
        v-for="(row, index) in sortedData"
        :key="resolveRowKey(row, index)"
        class="rounded-lg bg-default p-4 ring ring-default transition-colors"
        :class="{
          'cursor-pointer': clickableRows,
          'bg-elevated/50 ring-accented': selectable && isRowSelected(row, index)
        }"
        @click="clickableRows && emit('rowClick', row)"
      >
        <div class="space-y-3">
          <div v-if="selectable" class="flex justify-end">
            <UCheckbox
              :model-value="isRowSelected(row, index)"
              :aria-label="getRowSelectionLabel(row, index)"
              data-test="select-row"
              @click.stop
              @update:model-value="toggleRowSelection(row, index, !!$event)"
            />
          </div>
          <div
            v-for="column in dataColumns"
            :key="column.key"
            :data-field="column.key"
            class="flex min-w-0 items-start justify-between gap-4"
          >
            <span class="text-xs font-medium text-muted">
              {{ column.label }}
            </span>
            <div class="min-w-0 max-w-full text-right text-sm text-default">
              <slot :name="`cell-${column.key}`" :row="row" :value="row[column.key]" :expanded="actionsExpanded">
                {{ column.formatter ? column.formatter(row[column.key], row) : row[column.key] }}
              </slot>
            </div>
          </div>
          <div v-if="hasActionsColumn" class="border-t border-default pt-3">
            <slot name="cell-actions" :row="row" :value="row['actions']" :expanded="actionsExpanded"></slot>
          </div>
        </div>
      </div>
    </template>
  </div>
  <!-- Desktop: Nuxt UI table (TanStack). Sorting, selection and windowing stay in this component;
       the table renders the rows it is given. -->
  <UTable
    v-else
    ref="tableRef"
    :data="tableRows"
    :columns="tableColumns"
    :loading="loading"
    :get-row-id="getTableRowId"
    :row-selection="rowSelectionState"
    :sorting="sortingState"
    :sorting-options="{ manualSorting: true }"
    :watch-options="{ deep: false }"
    :meta="tableMeta"
    :ui="tableUi"
    data-data-table=""
    :data-clickable="clickableRows || undefined"
    :data-hidden-start="hiddenAtStart || undefined"
    :data-hidden-end="hiddenAtEnd || undefined"
    :style="pinOffsetStyle"
    @click="onTableClick"
  >
    <template v-if="selectable" #__select-header>
      <UCheckbox
        :model-value="headerCheckboxState"
        :aria-label="t('common.selectAll')"
        data-test="select-all"
        @update:model-value="toggleAllVisible(!!$event)"
      />
    </template>
    <template v-if="selectable" #__select-cell="{ row }">
      <div v-if="row.original.skeleton" class="size-4 animate-pulse rounded-sm bg-elevated"></div>
      <UCheckbox
        v-else
        :model-value="isRowSelected(row.original.row, row.original.index)"
        :aria-label="getRowSelectionLabel(row.original.row, row.original.index)"
        data-test="select-row"
        @click.stop
        @update:model-value="toggleRowSelection(row.original.row, row.original.index, !!$event)"
      />
    </template>
    <template v-for="column in columns" #[`${column.key}-header`]>
      <div :class="['flex items-center', getHeaderContentAlignmentClass(column)]">
        <UButton
          v-if="column.sortable"
          color="neutral"
          variant="ghost"
          :icon="getSortIcon(column.key)"
          :class="['data-table-sort font-semibold text-highlighted', getSortButtonClass(column)]"
          :ui="{ leadingIcon: sortKey === column.key ? 'text-highlighted' : 'text-dimmed' }"
          :data-sort="getColumnAriaSort(column.key)"
          @click="handleSort(column.key)"
        >
          <slot :name="`header-${column.key}`" :column="column" :sort-key="sortKey" :sort-order="sortOrder">
            {{ column.label }}
          </slot>
        </UButton>
        <slot v-else :name="`header-${column.key}`" :column="column" :sort-key="sortKey" :sort-order="sortOrder">
          <span>{{ column.label }}</span>
        </slot>
      </div>
    </template>

    <template v-for="column in columns" #[`${column.key}-cell`]="{ row }">
      <div v-if="row.original.skeleton" class="h-4 w-3/4 min-w-8 animate-pulse rounded-sm bg-elevated"></div>
      <slot
        v-else
        :name="`cell-${column.key}`"
        :row="row.original.row"
        :value="row.original.row[column.key]"
        :expanded="actionsExpanded"
      >
        {{ column.formatter
          ? column.formatter(row.original.row[column.key], row.original.row)
          : row.original.row[column.key] }}
      </slot>
    </template>

    <template #empty>
      <slot name="empty">
        <div class="flex flex-col items-center gap-2">
          <UIcon name="i-lucide-inbox" class="size-8 text-dimmed" />
          <p class="text-sm text-muted">{{ t('empty.noData') }}</p>
        </div>
      </slot>
    </template>

    <!-- Windowed rendering: spacer rows stand in for the rows outside the window. -->
    <template v-if="virtualPaddingTop > 0 && !loading" #body-top>
      <tr aria-hidden="true">
        <td :colspan="tableColumnCount" :style="{ height: virtualPaddingTop + 'px', padding: 0, border: 'none' }"></td>
      </tr>
    </template>
    <template v-if="virtualPaddingBottom > 0 && !loading" #body-bottom>
      <tr aria-hidden="true">
        <td :colspan="tableColumnCount" :style="{ height: virtualPaddingBottom + 'px', padding: 0, border: 'none' }"></td>
      </tr>
    </template>
  </UTable>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, onUpdated, watch, nextTick } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useVirtualizer, observeElementRect as observeElementRectDefault } from '@tanstack/vue-virtual'
import { useI18n } from 'vue-i18n'
import type { Column } from './types'
import Select, { type SelectOption } from './Select.vue'

const { t } = useI18n()

const desktopViewportQuery = '(min-width: 768px)'
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(desktopViewportQuery).matches
)

const emit = defineEmits<{
  sort: [key: string, order: 'asc' | 'desc']
  rowClick: [row: any]
  'update:selectedKeys': [keys: Array<string | number>]
  selectionChange: [keys: Array<string | number>]
}>()

// 表格实例与滚动容器（UTable 的根元素就是横向/纵向滚动容器）
const tableRef = ref<{ $el?: HTMLElement } | null>(null)
const tableWrapperRef = computed<HTMLElement | null>(() => tableRef.value?.$el ?? null)
const isScrollable = ref(false)
const actionsColumnNeedsExpanding = ref(false)

// --- 虚拟滚动「整表空白」根治 ---
// 根因:本组件根 .table-wrapper 为 flex:1 / min-h-0,高度由父级 flex 链决定。@tanstack 虚拟化器
// 仅在 observeElementRect 回调里写 scrollRect;一旦该回调读到 0 高度(加载瞬间 flex 未结算,或
// 滚动中动态行高校正触发的 reflow),scrollRect 被钉死为 0 → calculateRange 返回 null → 整表空白。
// 对策(见下方 virtualizer 选项):
//   1) 覆写 observeElementRect,直接丢弃 height<=0 的读数,scrollRect 永不被钉成 0;
//   2) initialRect 给一屏兜底高度,首个有效读数到来前也有行可渲染,绝不空白。
// 兜底高度:表格区域大致 = 视口高度 - 顶栏/外边距/筛选/分页 ≈ 320px
const estimatedViewportHeight = () => {
  if (typeof window === 'undefined') return 600
  return Math.max(window.innerHeight - 320, 400)
}

// 覆写默认 observeElementRect:过滤掉 0 高度读数(根治整表空白的关键)
const observeElementRectNonZero = (
  instance: any,
  cb: (rect: { width: number; height: number }) => void
) => observeElementRectDefault(instance, (rect) => {
  if (rect.height > 0) cb(rect)
})

// 检查是否可滚动
// Columns hidden under a pinned column (scrolled past at the start, or still ahead at the end)
// get a hairline on that pinned column's inner edge.
const hiddenAtStart = ref(false)
const hiddenAtEnd = ref(false)
const updateScrollEdges = () => {
  const el = tableWrapperRef.value
  if (!el) return
  hiddenAtStart.value = el.scrollLeft > 0
  hiddenAtEnd.value = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
}

const checkScrollable = () => {
  if (tableWrapperRef.value) {
    isScrollable.value = tableWrapperRef.value.scrollWidth > tableWrapperRef.value.clientWidth
    updateScrollEdges()
  }
}
// 检查操作列是否需要展开
const checkActionsColumnWidth = () => {
  if (!props.expandableActions) {
    actionsColumnNeedsExpanding.value = false
    actionsExpanded.value = false
    return
  }
  if (!tableWrapperRef.value) return

  // 查找第一行的操作列单元格
  const firstActionCell = tableWrapperRef.value.querySelector('tbody tr:first-child td:last-child')
  if (!firstActionCell) return

  // 查找操作列内容的容器div
  const actionsContainer = firstActionCell.querySelector('div')
  if (!actionsContainer) return

  // 临时展开以测量完整宽度
  const wasExpanded = actionsExpanded.value
  actionsExpanded.value = true

  // 等待DOM更新
  nextTick(() => {
    // 测量所有按钮的总宽度
    const actionItems = actionsContainer.querySelectorAll('button, a, [role="button"]')
    if (actionItems.length <= 2) {
      actionsColumnNeedsExpanding.value = false
      actionsExpanded.value = wasExpanded
      return
    }

    // 计算所有按钮的总宽度（包括gap）
    let totalWidth = 0
    actionItems.forEach((item, index) => {
      totalWidth += (item as HTMLElement).offsetWidth
      if (index < actionItems.length - 1) {
        totalWidth += 4 // gap-1 = 4px
      }
    })

    // 获取单元格可用宽度（减去padding）
    const cellWidth = (firstActionCell as HTMLElement).clientWidth - 32 // 减去左右padding

    // 如果总宽度超过可用宽度，需要展开功能
    actionsColumnNeedsExpanding.value = totalWidth > cellWidth

    // 恢复原来的展开状态
    actionsExpanded.value = wasExpanded
  })
}
// 监听尺寸变化
let resizeObserver: ResizeObserver | null = null
let resizeHandler: (() => void) | null = null
let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null

let scrollTrackedEl: HTMLElement | null = null

const detachDesktopTableTracking = () => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (resizeHandler) {
    window.removeEventListener('resize', resizeHandler)
    resizeHandler = null
  }
  scrollTrackedEl?.removeEventListener('scroll', updateScrollEdges)
  scrollTrackedEl = null
}

const attachDesktopTableTracking = () => {
  checkScrollable()
  checkActionsColumnWidth()
  scrollTrackedEl = tableWrapperRef.value
  scrollTrackedEl?.addEventListener('scroll', updateScrollEdges, { passive: true })
  if (tableWrapperRef.value && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(() => {
      checkScrollable()
      checkActionsColumnWidth()
    })
    resizeObserver.observe(tableWrapperRef.value)
  } else {
    // 降级方案：不支持 ResizeObserver 时使用 window resize
    resizeHandler = () => {
      checkScrollable()
      checkActionsColumnWidth()
    }
    window.addEventListener('resize', resizeHandler)
  }
}

onMounted(() => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(desktopViewportQuery)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }
})

onUnmounted(() => {
  detachDesktopTableTracking()
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
    desktopViewportListener = null
  }
  desktopViewportMediaQuery = null
})
interface Props {
  columns: Column[]
  data: any[]
  loading?: boolean
  stickyFirstColumn?: boolean
  stickyActionsColumn?: boolean
  expandableActions?: boolean
  actionsCount?: number // 操作按钮总数，用于判断是否需要展开功能
  rowKey?: string | ((row: any) => string | number)
  /**
   * Default sort configuration (only applied when there is no persisted sort state)
   */
  defaultSortKey?: string
  defaultSortOrder?: 'asc' | 'desc'
  /**
   * Persist sort state (key + order) to localStorage using this key.
   * If provided, DataTable will load the stored sort state on mount.
   */
  sortStorageKey?: string
  /**
   * Enable server-side sorting mode. When true, clicking sort headers
   * will emit 'sort' events instead of performing client-side sorting.
   */
  serverSideSort?: boolean
  /** Emit 'rowClick' on row/card click and show pointer cursor (interactive cells should @click.stop) */
  clickableRows?: boolean
  /** Estimated row height in px for the virtualizer (default 56) */
  estimateRowHeight?: number
  /** Number of rows to render beyond the visible area (default 5) */
  overscan?: number
  /**
   * Only virtualize when the row count exceeds this threshold (default 100).
   * Smaller lists render in full, avoiding the scroll-compensation jank caused by
   * estimated-vs-actual row heights when rows have variable height.
   */
  virtualizeThreshold?: number
  /** Enable controlled row selection. Stable row keys are strongly recommended. */
  selectable?: boolean
  /** Selected row keys. Keys outside the current data page are preserved. */
  selectedKeys?: Array<string | number>
  /** Accessible label for a row selection checkbox. */
  selectionLabel?: string | ((row: any) => string)
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  stickyFirstColumn: true,
  stickyActionsColumn: true,
  expandableActions: true,
  defaultSortOrder: 'asc',
  serverSideSort: false,
  selectable: false,
  selectedKeys: () => []
})

const sortKey = ref<string>('')
const sortOrder = ref<'asc' | 'desc'>('asc')
const actionsExpanded = ref(false)

type PersistedSortState = {
  key: string
  order: 'asc' | 'desc'
}

const collator = new Intl.Collator(undefined, {
  numeric: true,
  sensitivity: 'base'
})

const getSortableKeys = () => {
  const keys = new Set<string>()
  for (const col of props.columns) {
    if (col.sortable) keys.add(col.key)
  }
  return keys
}

const normalizeSortKey = (candidate: string) => {
  if (!candidate) return ''
  const sortableKeys = getSortableKeys()
  return sortableKeys.has(candidate) ? candidate : ''
}

const normalizeSortOrder = (candidate: any): 'asc' | 'desc' => {
  return candidate === 'desc' ? 'desc' : 'asc'
}
const readPersistedSortState = (): PersistedSortState | null => {
  if (!props.sortStorageKey) return null
  try {
    const raw = localStorage.getItem(props.sortStorageKey)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<PersistedSortState>
    const key = normalizeSortKey(typeof parsed.key === 'string' ? parsed.key : '')
    if (!key) return null
    return { key, order: normalizeSortOrder(parsed.order) }
  } catch (e) {
    console.error('[DataTable] Failed to read persisted sort state:', e)
    return null
  }
}

const writePersistedSortState = (state: PersistedSortState) => {
  if (!props.sortStorageKey) return
  try {
    localStorage.setItem(props.sortStorageKey, JSON.stringify(state))
  } catch (e) {
    console.error('[DataTable] Failed to persist sort state:', e)
  }
}

const resolveInitialSortState = (): PersistedSortState | null => {
  const persisted = readPersistedSortState()
  if (persisted) return persisted

  const key = normalizeSortKey(props.defaultSortKey || '')
  if (!key) return null
  return { key, order: normalizeSortOrder(props.defaultSortOrder) }
}

const applySortState = (state: PersistedSortState | null) => {
  if (!state) return
  sortKey.value = state.key
  sortOrder.value = state.order
}

const getColumnAriaSort = (key: string) => {
  if (sortKey.value !== key) return 'none'
  return sortOrder.value === 'asc' ? 'ascending' : 'descending'
}

// Sortable headers are ghost buttons with one arrow icon, as in the Nuxt UI dashboard table.
const getSortIcon = (key: string) => {
  if (sortKey.value !== key) return 'i-lucide-arrow-up-down'
  return sortOrder.value === 'asc' ? 'i-lucide-arrow-up-narrow-wide' : 'i-lucide-arrow-down-wide-narrow'
}

const getColumnAlignment = (column: Column): 'start' | 'center' | 'end' => {
  const className = column.class || ''
  if (className.includes('text-center')) return 'center'
  if (className.includes('text-right')) return 'end'
  return 'start'
}

const getHeaderContentAlignmentClass = (column: Column) => {
  const alignment = getColumnAlignment(column)
  if (alignment === 'center') return 'justify-center'
  if (alignment === 'end') return 'justify-end'
  return 'justify-start'
}

// The button's own padding is pulled back so its label lines up with the cells below.
const getSortButtonClass = (column: Column) => {
  const alignment = getColumnAlignment(column)
  if (alignment === 'center') return '-mx-2.5'
  if (alignment === 'end') return '-me-2.5'
  return '-ms-2.5'
}

const isNullishOrEmpty = (value: any) => value === null || value === undefined || value === ''

const toFiniteNumberOrNull = (value: any): number | null => {
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  if (typeof value === 'boolean') return value ? 1 : 0
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return null
    const n = Number(trimmed)
    return Number.isFinite(n) ? n : null
  }
  return null
}
const toSortableString = (value: any): string => {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (value instanceof Date) return value.toISOString()
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

const compareSortValues = (a: any, b: any): number => {
  const aEmpty = isNullishOrEmpty(a)
  const bEmpty = isNullishOrEmpty(b)
  if (aEmpty && bEmpty) return 0
  if (aEmpty) return 1
  if (bEmpty) return -1

  const aNum = toFiniteNumberOrNull(a)
  const bNum = toFiniteNumberOrNull(b)
  if (aNum !== null && bNum !== null) {
    if (aNum === bNum) return 0
    return aNum < bNum ? -1 : 1
  }

  const aStr = toSortableString(a)
  const bStr = toSortableString(b)
  const res = collator.compare(aStr, bStr)
  if (res === 0) return 0
  return res < 0 ? -1 : 1
}
const resolveStableRowKey = (row: any): string | number | undefined => {
  if (typeof props.rowKey === 'function') {
    const key = props.rowKey(row)
    return key ?? undefined
  }
  if (typeof props.rowKey === 'string' && props.rowKey) {
    const key = row?.[props.rowKey]
    return key ?? undefined
  }
  const key = row?.id
  return key ?? undefined
}

const resolveRowKey = (row: any, index: number) => resolveStableRowKey(row) ?? index

const dataColumns = computed(() => props.columns.filter((column) => column.key !== 'actions'))
const columnsSignature = computed(() =>
  props.columns.map((column) => `${column.key}:${column.sortable ? '1' : '0'}`).join('|')
)

watch(
  isDesktopViewport,
  async (isDesktop) => {
    detachDesktopTableTracking()
    if (!isDesktop) return
    await nextTick()
    attachDesktopTableTracking()
  },
  { immediate: true, flush: 'post' }
)

// 数据/列变化时重新检查滚动状态
// 注意：不能监听 actionsExpanded，因为 checkActionsColumnWidth 会临时修改它，会导致无限循环
watch(
  [() => props.data.length, columnsSignature],
  async () => {
    await nextTick()
    checkScrollable()
    checkActionsColumnWidth()
  },
  { flush: 'post' }
)

// 单独监听展开状态变化，只更新滚动状态
watch(actionsExpanded, async () => {
  await nextTick()
  checkScrollable()
})
const handleSort = (key: string) => {
  let newOrder: 'asc' | 'desc' = 'asc'
  if (sortKey.value === key) {
    newOrder = sortOrder.value === 'asc' ? 'desc' : 'asc'
  }

  if (props.serverSideSort) {
    // Server-side sort mode: emit event and update internal state for UI feedback
    sortKey.value = key
    sortOrder.value = newOrder
    emit('sort', key, newOrder)
  } else {
    // Client-side sort mode: just update internal state
    sortKey.value = key
    sortOrder.value = newOrder
  }
}

// 手机端卡片布局没有表头，这里提供等价排序入口：
// 字段下拉 + 升降序切换，与服务端/本地排序共用 handleSort 同一套状态与事件。
const sortableColumns = computed(() => props.columns.filter((column) => column.sortable))
const mobileSortOptions = computed<SelectOption[]>(() =>
  sortableColumns.value.map((column) => ({ value: column.key, label: column.label }))
)

const onMobileSortSelect = (value: string | number | boolean | null) => {
  const key = typeof value === 'string' ? value : ''
  if (!key || key === sortKey.value) return
  handleSort(key)
}

const toggleMobileSortDirection = () => {
  if (sortKey.value) handleSort(sortKey.value)
}

const sortedData = computed(() => {
  // Server-side sort mode: return data as-is (server handles sorting)
  if (props.serverSideSort || !sortKey.value || !props.data) return props.data

  const key = sortKey.value
  const order = sortOrder.value

  // Stable sort (tie-break with original index) to avoid jitter when values are equal.
  return props.data
    .map((row, index) => ({ row, index }))
    .sort((a, b) => {
      const cmp = compareSortValues(a.row?.[key], b.row?.[key])
      if (cmp !== 0) return order === 'asc' ? cmp : -cmp
      return a.index - b.index
    })
    .map(item => item.row)
})

const tableColumnCount = computed(() => props.columns.length + (props.selectable ? 1 : 0))
const selectedKeySet = computed(() => new Set(props.selectedKeys))
const visibleRowKeys = computed(() =>
  (sortedData.value ?? []).map((row, index) => resolveRowKey(row, index))
)
const allVisibleSelected = computed(() =>
  visibleRowKeys.value.length > 0
  && visibleRowKeys.value.every((key) => selectedKeySet.value.has(key))
)
const someVisibleSelected = computed(() => {
  if (allVisibleSelected.value) return false
  return visibleRowKeys.value.some((key) => selectedKeySet.value.has(key))
})
// UCheckbox model of the "select all" box: checked, partly checked or empty.
const headerCheckboxState = computed<boolean | 'indeterminate'>(() =>
  allVisibleSelected.value ? true : someVisibleSelected.value ? 'indeterminate' : false
)
const emitSelection = (next: Set<string | number>) => {
  const keys = Array.from(next)
  emit('update:selectedKeys', keys)
  emit('selectionChange', keys)
}

const isRowSelected = (row: any, index: number) =>
  selectedKeySet.value.has(resolveRowKey(row, index))

const getRowSelectionLabel = (row: any, index: number) => {
  if (typeof props.selectionLabel === 'function') return props.selectionLabel(row)
  if (props.selectionLabel) return props.selectionLabel
  return `${t('common.selectOption')} ${resolveRowKey(row, index)}`
}

const toggleRowSelection = (row: any, index: number, checked: boolean) => {
  const next = new Set(props.selectedKeys)
  const key = resolveRowKey(row, index)
  if (checked) next.add(key)
  else next.delete(key)
  emitSelection(next)
}

const toggleAllVisible = (checked: boolean) => {
  const next = new Set(props.selectedKeys)
  for (const key of visibleRowKeys.value) {
    if (checked) next.add(key)
    else next.delete(key)
  }
  emitSelection(next)
}

// --- Virtual scrolling ---
// 是否启用虚拟化:仅桌面端且行数超过阈值时开启。小列表全量渲染,彻底绕开虚拟器的
// 估算/测量/滚动补偿链路,消除可变行高导致的滚动抖动。
const shouldVirtualize = computed(() =>
  isDesktopViewport.value && (sortedData.value?.length ?? 0) > (props.virtualizeThreshold ?? 100)
)

const rowVirtualizer = useVirtualizer(computed(() => ({
  count: shouldVirtualize.value ? (sortedData.value?.length ?? 0) : 0,
  getScrollElement: () => tableWrapperRef.value,
  // 用行主键(与模板 :key 一致)而非默认的 index 作为 itemSizeCache 键,
  // 这样排序/筛选/跨阈值来回都能复用正确的已测行高,而不是残留的按 index 缓存 → 消除高度校正抖动。
  getItemKey: (index: number) => {
    const row = sortedData.value?.[index]
    return row != null ? resolveRowKey(row, index) : index
  },
  estimateSize: () => props.estimateRowHeight ?? 56,
  overscan: props.overscan ?? 5,
  // 兜底高度:首个有效高度读数到来前,先按一屏渲染,避免空白帧
  initialRect: { width: 0, height: estimatedViewportHeight() },
  // 关键:过滤 0 高度读数,杜绝 scrollRect 被钉成 0 → calculateRange 返回 null → 整表空白
  observeElementRect: observeElementRectNonZero,
  // 把测量类 ResizeObserver 回调批到 rAF,避免滚动中同步 reflow 风暴导致的校正抖动/空白
  useAnimationFrameWithResizeObserver: true,
})))

const virtualItems = computed(() => rowVirtualizer.value.getVirtualItems())

const virtualPaddingTop = computed(() => {
  const items = virtualItems.value
  return items.length > 0 ? items[0].start : 0
})

const virtualPaddingBottom = computed(() => {
  const items = virtualItems.value
  if (items.length === 0) return 0
  return rowVirtualizer.value.getTotalSize() - items[items.length - 1].end
})

type RowIdentityToken = string | number | object | symbol

const rowIdentityKeys = computed<RowIdentityToken[]>(() =>
  (sortedData.value ?? []).map((row) => {
    const stableKey = resolveStableRowKey(row)
    if (stableKey !== undefined) return stableKey

    // Object references survive pure reordering but change across page/filter results.
    // Primitive rows have no stable identity, so force conservative invalidation.
    return row !== null && typeof row === 'object' ? row : Symbol('unstable-row')
  })
)
const hasSameRowIdentitySet = (
  current: RowIdentityToken[],
  previous: RowIdentityToken[]
) => {
  if (current.length !== previous.length) return false
  const currentKeys = new Set(current)
  const previousKeys = new Set(previous)
  // Duplicate keys make row-to-cache ownership ambiguous, even when the unique
  // key set looks unchanged (for example [1, 1, 2] -> [1, 2, 2]).
  if (currentKeys.size !== current.length || previousKeys.size !== previous.length) return false
  return [...currentKeys].every(key => previousKeys.has(key))
}

watch(
  rowIdentityKeys,
  (current, previous) => {
    if (hasSameRowIdentitySet(current, previous)) return

    // The virtualizer owns caches across option updates. A new page/filter result
    // must release detached rows and sizes, while pure reordering keeps them.
    rowVirtualizer.value.measureElement(null)
    rowVirtualizer.value.measure()
  },
  { flush: 'post' }
)

// 统一的渲染行列表:虚拟化开启时只取窗口内的行(需 measure 交给虚拟器测量),
// 关闭时取全部行(无需测量)。模板据此渲染,两种模式共用同一套单元格结构。
const renderRows = computed<Array<{ index: number; row: any; measure: boolean }>>(() => {
  const data = sortedData.value ?? []
  if (shouldVirtualize.value) {
    return virtualItems.value.map(vr => ({ index: vr.index, row: data[vr.index], measure: true }))
  }
  return data.map((row, index) => ({ index, row, measure: false }))
})

const hasActionsColumn = computed(() => {
  return props.columns.some(column => column.key === 'actions')
})

const hasSelectColumn = computed(() => {
  return props.columns.length > 0 && props.columns[0].key === 'select'
})

// --- Nuxt UI table wiring ---
// Each table row carries the source row and its index in the sorted data. Skeleton rows stand in
// while loading, so the header and the columns keep their place.
interface TableRow {
  index: number
  row: any
  measure: boolean
  skeleton?: boolean
}

const skeletonRows: TableRow[] = Array.from({ length: 5 }, (_, index) => ({ index, row: {}, measure: false, skeleton: true }))

const tableRows = computed<TableRow[]>(() => (props.loading ? skeletonRows : renderRows.value))

const getTableRowId = (item: TableRow) =>
  item.skeleton ? `__skeleton-${item.index}` : String(resolveRowKey(item.row, item.index))

const rowSelectionState = computed(() => {
  const state: Record<string, boolean> = {}
  if (!props.selectable || props.loading) return state
  for (const item of renderRows.value) {
    if (isRowSelected(item.row, item.index)) state[String(resolveRowKey(item.row, item.index))] = true
  }
  return state
})

const sortingState = computed(() =>
  sortKey.value ? [{ id: sortKey.value, desc: sortOrder.value === 'desc' }] : []
)
// 根据列数自适应调整内边距：列数越多，内边距越小。
// Important (`!`) so the legacy table-page skin, which styles bare th/td, does not override it.
const cellPaddingClass = computed(() => {
  const columnCount = props.columns.length
  if (columnCount >= 10) return 'px-2!'
  if (columnCount >= 7) return 'px-3!'
  if (columnCount >= 5) return 'px-4!'
  return 'px-6!'
})
// The built-in checkbox column keeps the leading padding only.
const selectCellPaddingClass = computed(() => {
  const columnCount = props.columns.length
  if (columnCount >= 10) return 'ps-2! pe-0!'
  if (columnCount >= 7) return 'ps-3! pe-0!'
  if (columnCount >= 5) return 'ps-4! pe-0!'
  return 'ps-6! pe-0!'
})

// 固定列：第一列（有勾选列时为勾选列 + 第一数据列），以及操作列。
// The second pinned column sits right after the first; its offset is measured after render.
type PinSide = 'left-first' | 'left-second' | 'right' | null

const pinFirstPair = computed(() => props.stickyFirstColumn && (props.selectable || hasSelectColumn.value))

const getColumnPin = (column: Column, index: number): PinSide => {
  if (props.stickyActionsColumn && column.key === 'actions') return 'right'
  if (!props.stickyFirstColumn) return null
  if (props.selectable) return index === 0 ? 'left-second' : null
  if (hasSelectColumn.value) {
    if (index === 0) return 'left-first'
    if (index === 1) return 'left-second'
    return null
  }
  return index === 0 ? 'left-first' : null
}

// `data-table-pin-start` / `-end` mark the pinned cell that borders the scrolling columns.
const pinClass = (pin: PinSide) => {
  if (pin === 'left-first') return pinFirstPair.value ? 'data-table-pin sticky left-0' : 'data-table-pin data-table-pin-start sticky left-0'
  if (pin === 'left-second') return 'data-table-pin data-table-pin-start sticky left-(--data-table-pin-offset)'
  if (pin === 'right') return 'data-table-pin data-table-pin-end sticky right-0'
  return ''
}

const pinOffset = ref(0)
const pinOffsetStyle = computed(() => ({ '--data-table-pin-offset': `${pinOffset.value}px` }))

const tableColumns = computed<TableColumn<TableRow>[]>(() => {
  const defs: TableColumn<TableRow>[] = []
  if (props.selectable) {
    const cls = ['w-px', selectCellPaddingClass.value, pinClass(props.stickyFirstColumn ? 'left-first' : null)].join(' ')
    defs.push({ id: '__select', enableSorting: false, meta: { class: { th: cls, td: cls } } })
  }
  props.columns.forEach((column, index) => {
    const cls = [cellPaddingClass.value, pinClass(getColumnPin(column, index)), column.class].filter(Boolean).join(' ')
    defs.push({
      id: column.key,
      accessorFn: (item: TableRow) => item.row?.[column.key],
      header: column.label,
      enableSorting: !!column.sortable,
      meta: { class: { th: column.sortable ? `${cls} cursor-pointer` : cls, td: cls } }
    })
  })
  return defs
})

// Row-level classes; read when each row renders, so prop changes apply.
const tableMeta = {
  class: {
    tr: () => (props.clickableRows ? 'cursor-pointer hover:bg-elevated/50' : '')
  }
}
// The Nuxt UI dashboard table look (customers page): an elevated header row with rounded ends,
// hairline row dividers, and a tint on selected rows. Important (`!`) marks the properties the
// legacy table-page skin also sets on bare th/td.
const tableUi = {
  root: 'relative flex-1 min-h-0 overflow-auto isolate',
  base: 'w-full min-w-max border-separate border-spacing-0',
  thead: 'sticky top-0 z-2 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0!',
  th: 'py-2! text-sm! font-semibold! text-highlighted! border-y border-default! first:border-s last:border-e first:rounded-s-lg last:rounded-e-lg',
  td: 'py-4! text-sm! text-muted! border-b border-default!',
  separator: 'h-0',
  empty: 'py-12! text-sm text-muted',
  loading: 'py-12!'
}

// UTable owns the <tr> elements; keep the attributes the rest of the app relies on
// (`data-row-id` / `data-index` for swipe selection and the virtualizer) and the row lookup
// used by `rowClick`.
const rowElementData = new WeakMap<Element, any>()

const syncRenderedRows = () => {
  const root = tableWrapperRef.value
  if (!root || !isDesktopViewport.value) return
  const table = root.querySelector('table')
  if (!table) return
  const rowEls = Array.from(table.tBodies[0]?.rows ?? []).filter((el) => el.dataset.slot === 'tr')
  const rows = tableRows.value
  rowEls.forEach((el, position) => {
    const item = rows[position]
    if (!item || item.skeleton) {
      el.removeAttribute('data-row-id')
      el.removeAttribute('data-index')
      rowElementData.delete(el)
      return
    }
    el.setAttribute('data-row-id', String(resolveRowKey(item.row, item.index)))
    el.setAttribute('data-index', String(item.index))
    rowElementData.set(el, item.row)
    if (item.measure) rowVirtualizer.value.measureElement(el)
  })
  if (pinFirstPair.value) {
    const firstHeader = table.tHead?.rows[0]?.cells[0]
    const width = firstHeader?.offsetWidth ?? 0
    if (width && width !== pinOffset.value) pinOffset.value = width
  }
}

onMounted(syncRenderedRows)
onUpdated(syncRenderedRows)
watch(tableRows, () => nextTick(syncRenderedRows), { flush: 'post' })

const onTableClick = (event: MouseEvent) => {
  const target = event.target as Element | null
  if (!target) return
  // A click anywhere in a sortable header cell sorts, as before; the sort button handles its own.
  const headerCell = target.closest('th')
  if (headerCell?.parentElement?.parentElement?.tagName === 'THEAD') {
    if (target.closest('.data-table-sort')) return
    const columnId = tableColumns.value[headerCell.cellIndex]?.id
    const column = props.columns.find((candidate) => candidate.key === columnId)
    if (column?.sortable) handleSort(column.key)
    return
  }
  if (!props.clickableRows) return
  const rowEl = target.closest('tr[data-slot="tr"]')
  if (!rowEl || !rowElementData.has(rowEl)) return
  emit('rowClick', rowElementData.get(rowEl))
}

// Init + keep persisted sort state consistent with current columns
const didInitSort = ref(false)

onMounted(() => {
  const initial = resolveInitialSortState()
  applySortState(initial)
  didInitSort.value = true
})

watch(
  columnsSignature,
  () => {
    // If current sort key is no longer sortable/visible, fall back to default/persisted.
    const normalized = normalizeSortKey(sortKey.value)
    if (!sortKey.value) {
      const initial = resolveInitialSortState()
      applySortState(initial)
      return
    }

    if (!normalized) {
      const fallback = resolveInitialSortState()
      if (fallback) {
        applySortState(fallback)
      } else {
        sortKey.value = ''
        sortOrder.value = 'asc'
      }
    }
  },
  { flush: 'post' }
)

watch(
  [sortKey, sortOrder],
  ([nextKey, nextOrder]) => {
    if (!didInitSort.value) return
    if (!props.sortStorageKey) return
    const key = normalizeSortKey(nextKey)
    if (!key) return
    writePersistedSortState({ key, order: normalizeSortOrder(nextOrder) })
  },
  { flush: 'post' }
)

defineExpose({
  virtualizer: rowVirtualizer,
  shouldVirtualize,
  sortedData,
  resolveRowKey,
  tableWrapperEl: tableWrapperRef,
})
</script>

<style>
/* Header cells are opaque: the header row stays fixed while rows scroll underneath it.
   This is the elevated/50 tint of the Nuxt UI table, flattened onto the page colour. */
[data-data-table] > table > thead > tr > th {
  background-color: color-mix(in srgb, var(--ui-bg-elevated) 50%, var(--ui-bg));
}

/* Pinned columns sit above the scrolled cells. Their background follows the row state. */
[data-data-table] > table > thead > tr > th.data-table-pin {
  z-index: 1;
}

[data-data-table] > table > tbody > tr > td.data-table-pin {
  z-index: 1;
  background-color: var(--ui-bg);
}

[data-data-table] > table > tbody > tr[data-selected='true'] > td.data-table-pin,
[data-data-table][data-clickable] > table > tbody > tr:hover > td.data-table-pin {
  background-color: color-mix(in srgb, var(--ui-bg-elevated) 50%, var(--ui-bg));
}

/* A hairline on a pinned column's inner edge, only while columns scroll underneath it. */
[data-data-table][data-hidden-start] > table > * > tr > .data-table-pin-start {
  box-shadow: inset -1px 0 0 var(--ui-border);
}

[data-data-table][data-hidden-end] > table > * > tr > .data-table-pin-end {
  box-shadow: inset 1px 0 0 var(--ui-border);
}

/* Inside a card (the table page layout and other card wrappers) the card already draws the
   outline, so the header row runs flush: no side borders or rounded ends of its own. */
:is(.card, .table-scroll-container) [data-data-table] > table > thead > tr > th {
  border-top-width: 0;
  border-inline-width: 0;
  border-radius: 0;
}

/* Keep the scrollbar visible: the global style hides scrollbars until hover. */
[data-data-table] {
  scrollbar-width: thin;
  scrollbar-color: color-mix(in srgb, var(--ui-text-dimmed) 60%, transparent) transparent;
}
</style>
