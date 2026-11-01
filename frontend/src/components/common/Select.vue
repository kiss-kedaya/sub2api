<template>
  <div class="relative">
    <USelectMenu
      :id="id"
      :model-value="selectedKey"
      :items="menuItems"
      value-key="value"
      label-key="label"
      :search-input="searchInputProps"
      ignore-filter
      :reset-model-value-on-clear="false"
      :clear="clearProps"
      :disabled="disabled"
      :color="error ? 'error' : 'neutral'"
      :highlight="error"
      :size="size === 'sm' ? 'sm' : 'md'"
      :content="contentProps"
      :ui="menuUi"
      :aria-label="ariaLabel ?? 'Select option'"
      :aria-describedby="ariaDescribedby"
      v-model:search-term="searchTerm"
      @update:model-value="onPick"
      @update:open="onOpenChange"
      @clear="clearSelection"
    >
      <span :class="hasDisplayValue ? 'select-value truncate' : 'select-value truncate text-dimmed'">
        <slot name="selected" :option="selectedOption">
          {{ selectedLabel }}
        </slot>
      </span>

      <template v-if="menuTitle" #content-top>
        <div class="select-popover-head flex items-center justify-between gap-3 px-2.5 pb-1 pt-2">
          <span class="text-xs font-semibold text-highlighted">{{ menuTitle }}</span>
          <span class="select-popover-count text-xs text-dimmed">{{ selectableCount }}</span>
        </div>
      </template>

      <template v-if="$slots.option" #item="{ item }">
        <slot name="option" :option="optionOf(item)" :selected="isSelected(optionOf(item))"></slot>
      </template>

      <template v-if="familyKey || hasCreatableItem" #item-leading="{ item }">
        <UIcon v-if="isCreatable(item)" name="i-lucide-search" class="size-4 shrink-0 text-dimmed" />
        <UBadge
          v-else-if="familyKey && getOptionFamily(optionOf(item))"
          :label="getOptionFamily(optionOf(item))"
          color="neutral"
          variant="soft"
          size="sm"
          class="select-option-family shrink-0"
        />
      </template>

      <template #empty>
        <span class="select-empty">{{ props.loading ? t('common.loading') : emptyTextDisplay }}</span>
      </template>

      <template v-if="showKeyboardHint" #content-bottom>
        <div class="select-popover-foot border-t border-default px-2.5 py-1.5 text-center text-xs text-dimmed">
          {{ t('common.selectKeyboardHint') }}
        </div>
      </template>
    </USelectMenu>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

export interface SelectOption {
  value: string | number | boolean | null
  label: string
  disabled?: boolean
  [key: string]: unknown
}

interface Props {
  modelValue: string | number | boolean | null | undefined
  options: SelectOption[] | Array<Record<string, unknown>>
  placeholder?: string
  disabled?: boolean
  error?: boolean
  searchable?: boolean | 'auto'
  searchPlaceholder?: string
  emptyText?: string
  valueKey?: string
  labelKey?: string
  creatable?: boolean
  creatablePrefix?: string
  clearable?: boolean
  id?: string
  ariaLabel?: string
  ariaDescribedby?: string
  /** 远程搜索模式：输入不在本地过滤 options，而是防抖后 emit('search', query)，由父组件请求数据更新 options */
  remote?: boolean
  /** 远程搜索模式下的加载态：options 为空时下拉显示 loading 文案 */
  loading?: boolean
  /** 弹层顶部标题，例如「模型库」；留空则不显示 */
  menuTitle?: string
  /** 选项对象中用作小标签的字段名，例如 family */
  familyKey?: string
  /** 弹层底部是否显示快捷键提示（默认不显示，与 Nuxt UI 菜单一致） */
  showKeyboardHint?: boolean
  /** 触发器尺寸 */
  size?: 'sm' | 'md'
}

interface Emits {
  (e: 'update:modelValue', value: string | number | boolean | null): void
  (e: 'change', value: string | number | boolean | null, option: SelectOption | null): void
  (e: 'search', query: string): void
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false,
  error: false,
  searchable: 'auto',
  creatable: false,
  creatablePrefix: '',
  clearable: false,
  valueKey: 'value',
  labelKey: 'label',
  remote: false,
  loading: false,
  menuTitle: '',
  familyKey: '',
  showKeyboardHint: false,
  size: 'md'
})

const emit = defineEmits<Emits>()

/** One row handed to USelectMenu. `value` is a typed key, so '', null, numbers and booleans
 *  stay distinct and never collide with reka's empty-string "no selection" value. */
interface MenuItem {
  value: string
  label: string
  type?: 'label'
  disabled?: boolean
  class?: string
  option: any
  creatable?: boolean
}

const isOpen = ref(false)
const searchTerm = ref('')

// i18n placeholders
const placeholderText = computed(() => props.placeholder ?? t('common.selectOption'))
const searchPlaceholderText = computed(() => props.searchPlaceholder ?? t('common.searchPlaceholder'))
const emptyTextDisplay = computed(() => props.emptyText ?? t('common.noOptionsFound'))

// 远程搜索的防抖间隔（对齐 OpenAIFastPolicyUserSelector 的 300ms 惯例）。
const REMOTE_SEARCH_DEBOUNCE_MS = 300
let remoteSearchTimer: ReturnType<typeof setTimeout> | null = null

const isSearchable = computed(() => {
  // 远程搜索模式始终显示搜索框（选项只是服务端结果的一页）。
  if (props.remote) return true
  if (props.searchable === 'auto') return props.options.length > 5
  return props.searchable
})

const getOptionValue = (option: any): any => {
  if (typeof option === 'object' && option !== null) {
    return option[props.valueKey]
  }
  return option
}

const getOptionLabel = (option: any): string => {
  if (typeof option === 'object' && option !== null) {
    return String(option[props.labelKey] ?? '')
  }
  return String(option ?? '')
}

const getOptionFamily = (option: any): string => {
  if (!props.familyKey || typeof option !== 'object' || option === null) return ''
  return String(option[props.familyKey] ?? '')
}

const isOptionDisabled = (option: any): boolean => {
  if (typeof option === 'object' && option !== null) {
    return !!option.disabled
  }
  return false
}

const isGroupHeaderOption = (option: any): boolean => {
  if (typeof option === 'object' && option !== null) {
    return option.kind === 'group'
  }
  return false
}

const keyOf = (value: unknown): string => {
  if (value === undefined) return 'undefined:'
  if (value === null) return 'null:'
  return `${typeof value}:${String(value)}`
}

const selectedOption = computed(() => {
  return props.options.find((opt) => getOptionValue(opt) === props.modelValue) || null
})

const selectedLabel = computed(() => {
  if (selectedOption.value) {
    return getOptionLabel(selectedOption.value)
  }
  // In creatable mode, show the raw value if no matching option
  if (props.creatable && props.modelValue) {
    return String(props.modelValue)
  }
  return placeholderText.value
})

const hasDisplayValue = computed(() => !!selectedOption.value || (props.creatable && !!props.modelValue))

const hasValue = computed(
  () => props.modelValue !== null && props.modelValue !== undefined && props.modelValue !== ''
)

// No option matches → a key no item carries, so nothing shows as selected.
const selectedKey = computed(() => (selectedOption.value ? keyOf(props.modelValue) : '__none__'))

const filteredOptions = computed(() => {
  let opts = props.options as any[]
  // 远程搜索模式不在本地过滤（选项即服务端搜索结果的一页）。
  if (isSearchable.value && searchTerm.value && !props.remote) {
    const query = searchTerm.value.toLowerCase()
    opts = opts.filter((opt) => {
      // Match label
      if (getOptionLabel(opt).toLowerCase().includes(query)) return true
      // Also match description if present
      if (opt.description && String(opt.description).toLowerCase().includes(query)) return true
      return false
    })
  }
  return opts
})

// In creatable mode, a typed query is always offered first as a fuzzy-search value.
const creatableItem = computed<MenuItem | null>(() => {
  if (!props.creatable || !isSearchable.value || props.remote) return null
  const trimmed = searchTerm.value.trim()
  if (!trimmed) return null
  const prefix = props.creatablePrefix || t('common.search')
  const option = { [props.valueKey]: trimmed, [props.labelKey]: `${prefix} "${trimmed}"`, _creatable: true }
  return { value: `create:${trimmed}`, label: getOptionLabel(option), option, creatable: true, class: 'italic' }
})

const hasCreatableItem = computed(() => !!creatableItem.value)

const menuItems = computed<MenuItem[]>(() => {
  const items: MenuItem[] = filteredOptions.value.map((option) => {
    if (isGroupHeaderOption(option)) {
      return { value: `group:${getOptionLabel(option)}`, label: getOptionLabel(option), type: 'label', option }
    }
    return {
      value: keyOf(getOptionValue(option)),
      label: getOptionLabel(option),
      disabled: isOptionDisabled(option),
      option
    }
  })
  return creatableItem.value ? [creatableItem.value, ...items] : items
})

const itemByKey = computed(() => {
  const map = new Map<string, MenuItem>()
  for (const item of menuItems.value) {
    if (item.type !== 'label' && !map.has(item.value)) map.set(item.value, item)
  }
  return map
})

const selectableCount = computed(
  () => filteredOptions.value.filter((option) => !isGroupHeaderOption(option)).length
)

const optionOf = (item: unknown): any =>
  typeof item === 'object' && item !== null && 'option' in item ? (item as MenuItem).option : null

const isCreatable = (item: unknown): boolean =>
  typeof item === 'object' && item !== null && !!(item as MenuItem).creatable

const isSelected = (option: any): boolean => {
  return option !== null && getOptionValue(option) === props.modelValue
}

const searchInputProps = computed(() =>
  isSearchable.value
    ? {
        placeholder: searchPlaceholderText.value,
        'aria-label': searchPlaceholderText.value,
        ui: { base: 'select-search-input' }
      }
    : false
)

const clearProps = computed(() =>
  props.clearable && hasValue.value && !props.disabled
    ? { 'aria-label': 'Clear selection', class: 'select-clear' }
    : undefined
)

// The old dropdown stopped click/mousedown at its edge; hand-rolled "click outside" handlers in
// other panels rely on that, because the menu is portalled outside their DOM.
const stopPropagation = (event: Event) => event.stopPropagation()
const contentProps = computed(() => ({
  side: 'bottom' as const,
  sideOffset: 8,
  collisionPadding: 8,
  position: 'popper' as const,
  onClick: stopPropagation,
  onMousedown: stopPropagation
}))

const menuUi = {
  base: 'select-menu-trigger w-full',
  value: 'select-value',
  content: 'select-dropdown-portal z-[60] w-max min-w-[min(max(200px,var(--reka-combobox-trigger-width)),calc(100vw-16px))] max-w-[calc(100vw-16px)]',
  itemLabel: 'select-option-label',
  item: 'select-option',
  trailingIcon: 'transition-transform duration-200 group-data-[state=open]:rotate-180'
}

const onPick = (key: unknown) => {
  const item = typeof key === 'string' ? itemByKey.value.get(key) : undefined
  if (!item) return
  const value = getOptionValue(item.option) ?? null
  emit('update:modelValue', value)
  emit('change', value, item.option)
}

const clearSelection = () => {
  if (props.disabled) return
  emit('update:modelValue', null)
  emit('change', null, null)
}

const cancelRemoteSearch = () => {
  if (remoteSearchTimer) {
    clearTimeout(remoteSearchTimer)
    remoteSearchTimer = null
  }
}

const onOpenChange = (open: boolean) => {
  isOpen.value = open
  // 关闭时取消仍在排队的远程搜索（避免关闭后尾随 emit 一次 search(''))。
  if (!open) cancelRemoteSearch()
}

// 远程搜索：输入防抖后交给父组件请求（!isOpen 抑制关闭重置 searchTerm 触发的空 query）。
watch(searchTerm, (query) => {
  if (!props.remote || !isOpen.value) return
  cancelRemoteSearch()
  remoteSearchTimer = setTimeout(() => {
    remoteSearchTimer = null
    emit('search', query.trim())
  }, REMOTE_SEARCH_DEBOUNCE_MS)
})

onUnmounted(cancelRemoteSearch)
</script>
