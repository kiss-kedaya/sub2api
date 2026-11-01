<template>
  <div class="relative" ref="containerRef">
    <button
      ref="triggerRef"
      type="button"
      @click="toggle"
      :disabled="disabled"
      :aria-expanded="isOpen"
      :aria-haspopup="true"
      :id="id"
      :aria-label="ariaLabel ?? 'Select option'"
      :aria-describedby="ariaDescribedby"
      :class="[
        'select-trigger',
        size === 'sm' && 'select-trigger-sm',
        isOpen && 'select-trigger-open',
        error && 'select-trigger-error',
        disabled && 'select-trigger-disabled'
      ]"
      @keydown.down.prevent="onTriggerKeyDown"
      @keydown.up.prevent="onTriggerKeyDown"
    >
      <span class="select-value">
        <slot name="selected" :option="selectedOption">
          {{ selectedLabel }}
        </slot>
      </span>
      <span
        v-if="clearable && hasValue && !disabled"
        class="select-clear"
        role="button"
        tabindex="-1"
        aria-label="Clear selection"
        @click.stop="clearSelection"
        @mousedown.stop
        @keydown.enter.stop.prevent="clearSelection"
      >
        <UIcon name="i-lucide-x" class="size-4" />
      </span>
      <span class="select-icon">
        <UIcon
          name="i-lucide-chevron-down"
          :class="['size-5 transition-transform duration-150', isOpen && 'rotate-180']"
        />
      </span>
    </button>

    <!-- Teleport dropdown to body to escape stacking context -->
    <Teleport to="body">
      <Transition name="select-dropdown">
        <div
          v-if="isOpen"
          ref="dropdownRef"
          class="select-dropdown-portal"
          :class="[instanceId]"
          :style="dropdownStyle"
          role="listbox"
          @click.stop
          @mousedown.stop
          @keydown="onDropdownKeyDown"
        >
          <!-- Search input -->
          <div v-if="isSearchable" class="select-search">
            <UIcon name="i-lucide-search" class="size-4 shrink-0 text-gray-400" />
            <input
              ref="searchInputRef"
              v-model="searchQuery"
              type="text"
              :placeholder="searchPlaceholderText"
              :aria-label="searchPlaceholderText"
              class="select-search-input"
              @click.stop
            />
          </div>

          <!-- Options list -->
          <div v-if="menuTitle" class="select-popover-head">
            <strong>{{ menuTitle }}</strong>
            <span class="select-popover-count">{{ selectableCount }}</span>
          </div>
          <div class="select-options" ref="optionsListRef">
            <div
              v-for="(option, index) in filteredOptions"
              :key="`${typeof getOptionValue(option)}:${String(getOptionValue(option) ?? '')}`"
              role="option"
              :aria-selected="isSelected(option)"
              :aria-disabled="isOptionDisabled(option)"
              @click.stop="!isOptionDisabled(option) && selectOption(option)"
              @mouseenter="handleOptionMouseEnter(option, index)"
              :class="[
                'select-option',
                isGroupHeaderOption(option) && 'select-option-group',
                isSelected(option) && 'select-option-selected',
                isOptionDisabled(option) && !isGroupHeaderOption(option) && 'select-option-disabled',
                focusedIndex === index && !isGroupHeaderOption(option) && 'select-option-focused'
              ]"
            >
              <slot name="option" :option="option" :selected="isSelected(option)">
                <UIcon
                  v-if="option._creatable"
                  name="i-lucide-search"
                  class="size-4 flex-shrink-0 text-gray-400"
                />
                <span v-if="getOptionFamily(option)" class="select-option-family">{{ getOptionFamily(option) }}</span>
                <span class="select-option-label" :class="option._creatable && 'italic text-gray-500 dark:text-dark-300'">{{ getOptionLabel(option) }}</span>
                <UIcon
                  v-if="isSelected(option)"
                  name="i-lucide-check"
                  class="size-4 shrink-0 text-highlighted dark:text-primary-500"
                />
              </slot>
            </div>

            <!-- Empty state -->
            <div v-if="filteredOptions.length === 0" class="select-empty">
              {{ props.loading ? t('common.loading') : emptyTextDisplay }}
            </div>
          </div>
          <div v-if="showKeyboardHint" class="select-popover-foot">
            {{ t('common.selectKeyboardHint') }}
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// Instance ID for unique click-outside detection
const instanceId = `select-${Math.random().toString(36).substring(2, 9)}`

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
  /** 弹层底部是否显示快捷键提示 */
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
  showKeyboardHint: true,
  size: 'md'
})

const emit = defineEmits<Emits>()

const isOpen = ref(false)
const searchQuery = ref('')
const focusedIndex = ref(-1)
const containerRef = ref<HTMLElement | null>(null)
const triggerRef = ref<HTMLButtonElement | null>(null)
const searchInputRef = ref<HTMLInputElement | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const optionsListRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<'bottom' | 'top'>('bottom')
const triggerRect = ref<DOMRect | null>(null)
const dropdownViewportPadding = 8
const dropdownMinimumWidth = 200

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

// Computed style for teleported dropdown
const dropdownStyle = computed(() => {
  if (!triggerRect.value) return {}

  const rect = triggerRect.value
  const viewportRight = Math.max(dropdownViewportPadding, window.innerWidth - dropdownViewportPadding)
  const left = Math.min(
    Math.max(dropdownViewportPadding, rect.left),
    viewportRight
  )
  const availableWidth = Math.max(0, viewportRight - left)
  const preferredMinWidth = Math.max(dropdownMinimumWidth, rect.width)
  const minWidth = Math.min(preferredMinWidth, availableWidth)
  const style: Record<string, string> = {
    position: 'fixed',
    left: `${left}px`,
    minWidth: `${minWidth}px`,
    maxWidth: `${availableWidth}px`,
    zIndex: '100000020'
  }

  if (dropdownPosition.value === 'top') {
    style.bottom = `${window.innerHeight - rect.top + 4}px`
  } else {
    style.top = `${rect.bottom + 4}px`
  }

  return style
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

const hasValue = computed(
  () => props.modelValue !== null && props.modelValue !== undefined && props.modelValue !== ''
)

const filteredOptions = computed(() => {
  let opts = props.options as any[]
  // 远程搜索模式不在本地过滤（选项即服务端搜索结果的一页）。
  if (isSearchable.value && searchQuery.value && !props.remote) {
    const query = searchQuery.value.toLowerCase()
    opts = opts.filter((opt) => {
      // Match label
      if (getOptionLabel(opt).toLowerCase().includes(query)) return true
      // Also match description if present
      if (opt.description && String(opt.description).toLowerCase().includes(query)) return true
      return false
    })
    // In creatable mode, always prepend a fuzzy search option
    if (props.creatable && searchQuery.value.trim()) {
      const trimmed = searchQuery.value.trim()
      const prefix = props.creatablePrefix || t('common.search')
      opts = [{ [props.valueKey]: trimmed, [props.labelKey]: `${prefix} "${trimmed}"`, _creatable: true }, ...opts]
    }
  }
  return opts
})

const isSelected = (option: any): boolean => {
  return getOptionValue(option) === props.modelValue
}

const selectableCount = computed(
  () => filteredOptions.value.filter((option) => !isGroupHeaderOption(option)).length
)

const findNextEnabledIndex = (startIndex: number): number => {
  const opts = filteredOptions.value
  if (opts.length === 0) return -1
  for (let offset = 0; offset < opts.length; offset++) {
    const idx = (startIndex + offset) % opts.length
    if (!isOptionDisabled(opts[idx])) return idx
  }
  return -1
}

const findPrevEnabledIndex = (startIndex: number): number => {
  const opts = filteredOptions.value
  if (opts.length === 0) return -1
  for (let offset = 0; offset < opts.length; offset++) {
    const idx = (startIndex - offset + opts.length) % opts.length
    if (!isOptionDisabled(opts[idx])) return idx
  }
  return -1
}

const handleOptionMouseEnter = (option: any, index: number) => {
  if (isOptionDisabled(option) || isGroupHeaderOption(option)) return
  focusedIndex.value = index
}

// Update trigger rect periodically while open to follow scroll/resize
const updateTriggerRect = () => {
  if (containerRef.value) {
    triggerRect.value = containerRef.value.getBoundingClientRect()
  }
}

const calculateDropdownPosition = () => {
  if (!containerRef.value) return
  updateTriggerRect()

  nextTick(() => {
    if (!dropdownRef.value || !triggerRect.value) return
    const dropdownHeight = dropdownRef.value.offsetHeight || 240
    const spaceBelow = window.innerHeight - triggerRect.value.bottom
    const spaceAbove = triggerRect.value.top

    if (spaceBelow < dropdownHeight && spaceAbove > dropdownHeight) {
      dropdownPosition.value = 'top'
    } else {
      dropdownPosition.value = 'bottom'
    }
  })
}

const toggle = () => {
  if (props.disabled) return
  isOpen.value = !isOpen.value
}

watch(isOpen, (open) => {
  if (open) {
    calculateDropdownPosition()
    // Reset focused index to current selection or first item
    if (filteredOptions.value.length === 0) {
      focusedIndex.value = -1
    } else {
      const selectedIdx = filteredOptions.value.findIndex(isSelected)
      const initialIdx = selectedIdx >= 0 ? selectedIdx : 0
      focusedIndex.value = isOptionDisabled(filteredOptions.value[initialIdx])
        ? findNextEnabledIndex(initialIdx + 1)
        : initialIdx
    }

    if (isSearchable.value) {
      nextTick(() => searchInputRef.value?.focus())
    }
    // Add scroll listener to update position
    window.addEventListener('scroll', updateTriggerRect, { capture: true, passive: true })
    window.addEventListener('resize', calculateDropdownPosition)
  } else {
    searchQuery.value = ''
    focusedIndex.value = -1
    // 关闭时取消仍在排队的远程搜索（避免关闭后尾随 emit 一次 search(''))。
    if (remoteSearchTimer) {
      clearTimeout(remoteSearchTimer)
      remoteSearchTimer = null
    }
    window.removeEventListener('scroll', updateTriggerRect, { capture: true })
    window.removeEventListener('resize', calculateDropdownPosition)
  }
})

// 远程搜索：输入防抖后交给父组件请求（!isOpen 抑制关闭重置 searchQuery 触发的空 query）。
watch(searchQuery, (query) => {
  if (!props.remote || !isOpen.value) return
  if (remoteSearchTimer) clearTimeout(remoteSearchTimer)
  remoteSearchTimer = setTimeout(() => {
    remoteSearchTimer = null
    emit('search', query.trim())
  }, REMOTE_SEARCH_DEBOUNCE_MS)
})

const selectOption = (option: any) => {
  const value = getOptionValue(option) ?? null
  emit('update:modelValue', value)
  emit('change', value, option)
  isOpen.value = false
  triggerRef.value?.focus()
}

const clearSelection = () => {
  if (props.disabled) return
  emit('update:modelValue', null)
  emit('change', null, null)
}

// Keyboards
const onTriggerKeyDown = () => {
  if (!isOpen.value) {
    isOpen.value = true
  }
}

const onDropdownKeyDown = (e: KeyboardEvent) => {
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      focusedIndex.value = findNextEnabledIndex(focusedIndex.value + 1)
      if (focusedIndex.value >= 0) scrollToFocused()
      break
    case 'ArrowUp':
      e.preventDefault()
      focusedIndex.value = findPrevEnabledIndex(focusedIndex.value - 1)
      if (focusedIndex.value >= 0) scrollToFocused()
      break
    case 'Enter':
      e.preventDefault()
      if (focusedIndex.value >= 0 && focusedIndex.value < filteredOptions.value.length) {
        const opt = filteredOptions.value[focusedIndex.value]
        if (!isOptionDisabled(opt)) selectOption(opt)
      }
      break
    case 'Escape':
      e.preventDefault()
      isOpen.value = false
      triggerRef.value?.focus()
      break
    case 'Tab':
      isOpen.value = false
      break
  }
}

const scrollToFocused = () => {
  nextTick(() => {
    const list = optionsListRef.value
    if (!list) return
    const focusedEl = list.children[focusedIndex.value] as HTMLElement
    if (!focusedEl) return

    if (focusedEl.offsetTop < list.scrollTop) {
      list.scrollTop = focusedEl.offsetTop
    } else if (focusedEl.offsetTop + focusedEl.offsetHeight > list.scrollTop + list.offsetHeight) {
      list.scrollTop = focusedEl.offsetTop + focusedEl.offsetHeight - list.offsetHeight
    }
  })
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  // Check if click is inside THIS specific instance's dropdown or trigger
  const isInDropdown = !!target.closest(`.${instanceId}`)
  const isInTrigger = containerRef.value?.contains(target)

  if (!isInDropdown && !isInTrigger && isOpen.value) {
    isOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  window.removeEventListener('scroll', updateTriggerRect, { capture: true })
  window.removeEventListener('resize', calculateDropdownPosition)
  if (remoteSearchTimer) {
    clearTimeout(remoteSearchTimer)
    remoteSearchTimer = null
  }
})
</script>

<style scoped>
@reference "../../style.css";
.select-trigger {
  @apply flex w-full items-center justify-between gap-2;
  @apply rounded-[10px] px-4 py-2.5 text-sm;
  @apply bg-white dark:bg-dark-800;
  @apply border border-gray-200 dark:border-dark-600;
  @apply text-gray-900 dark:text-gray-100;
  @apply transition-all duration-200;
  @apply focus:border-gray-400 focus:outline-hidden focus:ring-1 focus:ring-gray-300/70 dark:focus:ring-dark-500;
  @apply hover:border-gray-300 dark:hover:border-dark-500;
  @apply cursor-pointer;
}

.select-trigger-open {
  @apply border-gray-400 ring-1 ring-gray-300/70 dark:border-dark-500 dark:ring-dark-500;
}

.select-trigger-sm {
  @apply rounded-md px-2.5 py-1 text-xs;
}

.select-trigger-error {
  @apply border-red-500 focus:border-red-500 focus:ring-red-500/30;
}

.select-trigger-disabled {
  @apply cursor-not-allowed bg-gray-100 opacity-60 dark:bg-dark-900;
}

.select-value {
  @apply flex-1 truncate text-left;
}

.select-icon {
  @apply flex-shrink-0 text-gray-400 dark:text-dark-400;
}

.select-clear {
  @apply flex flex-shrink-0 cursor-pointer items-center justify-center;
  @apply rounded text-gray-400 transition-colors;
  @apply hover:text-gray-600 dark:hover:text-gray-200;
}

</style>

<style>
@reference "../../style.css";
.select-dropdown-portal {
  @apply w-max min-w-[200px];
  @apply bg-white dark:bg-dark-800;
  @apply rounded-[14px];
  @apply border border-gray-200 dark:border-dark-700;
  @apply overflow-hidden;
  box-shadow:
    0 18px 44px -16px rgb(15 23 42 / 0.35),
    0 2px 8px -2px rgb(15 23 42 / 0.12);
  pointer-events: auto !important;
}

.select-dropdown-portal .select-popover-head {
  @apply flex items-center justify-between gap-3 px-3 pb-2 pt-2.5;
  @apply border-b border-gray-100 dark:border-dark-700;
}

.select-dropdown-portal .select-popover-head strong {
  @apply text-xs font-semibold uppercase tracking-wider;
  @apply text-gray-500 dark:text-gray-400;
}

.select-dropdown-portal .select-popover-count {
  @apply text-[11px] text-gray-400 dark:text-dark-400;
}

.select-dropdown-portal .select-option-family {
  @apply inline-flex flex-shrink-0 items-center rounded-md px-1.5 py-0.5;
  @apply bg-gray-100 text-[10px] font-semibold uppercase tracking-wide text-gray-500;
  @apply dark:bg-dark-700 dark:text-gray-400;
}

.select-dropdown-portal .select-popover-foot {
  @apply border-t border-gray-100 px-3 py-1.5 text-center text-[11px];
  @apply text-gray-400 dark:border-dark-700 dark:text-dark-400;
}

.select-dropdown-portal .select-search {
  @apply flex items-center gap-2 px-3 py-2;
  @apply border-b border-gray-100 dark:border-dark-700;
}

.select-dropdown-portal .select-search-input {
  @apply flex-1 bg-transparent text-sm;
  @apply text-gray-900 dark:text-gray-100;
  @apply placeholder:text-gray-400 dark:placeholder:text-dark-400;
  @apply focus:outline-none;
}

.select-dropdown-portal .select-options {
  @apply max-h-64 overflow-y-auto py-1 outline-none;
}

.select-dropdown-portal .select-option {
  @apply flex items-center justify-between gap-2;
  @apply mx-1 rounded-lg px-3 py-2 text-sm;
  @apply text-gray-700 dark:text-gray-300;
  @apply cursor-pointer transition-colors duration-150;
  @apply hover:bg-gray-100/70 dark:hover:bg-dark-700;
  pointer-events: auto !important;
}

.select-dropdown-portal .select-option-selected {
  @apply bg-primary-50 font-medium dark:bg-primary-900/20;
  @apply text-primary-700 dark:text-primary-300;
}

.select-dropdown-portal .select-option-focused {
  @apply bg-gray-100 dark:bg-dark-700;
}

.select-dropdown-portal .select-option-disabled {
  @apply cursor-not-allowed opacity-40;
}

.select-dropdown-portal .select-option-group {
  @apply cursor-default select-none;
  @apply bg-gray-50 dark:bg-dark-900;
  @apply text-[11px] font-bold uppercase tracking-wider;
  @apply text-gray-500 dark:text-gray-400;
}

.select-dropdown-portal .select-option-group:hover {
  @apply bg-gray-50 dark:bg-dark-900;
}

.select-dropdown-portal .select-option-label {
  @apply flex-1 min-w-0 truncate text-left;
}

.select-dropdown-portal .select-empty {
  @apply px-4 py-8 text-center text-sm;
  @apply text-gray-500 dark:text-dark-400;
}

.select-dropdown-enter-active,
.select-dropdown-leave-active {
  transition: opacity 150ms cubic-bezier(0.2, 0, 0, 1), transform 150ms cubic-bezier(0.2, 0, 0, 1);
}

.select-dropdown-enter-from,
.select-dropdown-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

/* Light theme as Nuxt UI's SelectMenu trigger: hairline border, 6px radius, xs shadow and a
   neutral focus ring. The doubled class outranks the scoped utilities; night theme unchanged. */
html:not(.dark) .select-trigger.select-trigger {
  border-color: var(--ui-border-accented);
  border-radius: calc(var(--ui-radius) * 1.5);
  box-shadow: 0 1px 2px rgb(10 10 10 / 0.05);
  transition: border-color 150ms ease, box-shadow 150ms ease;
}

html:not(.dark) .select-trigger.select-trigger:is(:focus-visible, .select-trigger-open) {
  border-color: var(--ui-border-inverted);
  box-shadow: 0 0 0 1px var(--ui-border-inverted);
}

html:not(.dark) .select-trigger.select-trigger-error {
  border-color: var(--ui-error);
}

html:not(.dark) .select-trigger .select-icon {
  color: var(--ui-text-dimmed);
}

/* Light theme as Nuxt UI's SelectMenu content: hairline ring, 8px radius, shadow-lg, compact
   rows with a soft highlight, the current value marked by its check. Night theme unchanged. */
html:not(.dark) .select-dropdown-portal {
  border-color: var(--ui-border);
  border-radius: calc(var(--ui-radius) * 2);
  box-shadow: 0 10px 15px -3px rgb(10 10 10 / 0.08), 0 4px 6px -4px rgb(10 10 10 / 0.06);
}

html:not(.dark) .select-dropdown-portal :is(.select-search, .select-popover-head, .select-popover-foot) {
  border-color: var(--ui-border);
}

html:not(.dark) .select-dropdown-portal .select-option {
  border-radius: calc(var(--ui-radius) * 1.5);
  padding: 6px 8px;
  color: var(--ui-text);
}

html:not(.dark) .select-dropdown-portal :is(.select-option:hover, .select-option-focused) {
  background-color: color-mix(in oklab, var(--ui-bg-elevated) 50%, transparent);
  color: var(--ui-text-highlighted);
}

html:not(.dark) .select-dropdown-portal .select-option-selected {
  background-color: transparent;
  color: var(--ui-text-highlighted);
}

html:not(.dark) .select-dropdown-portal .select-option-selected:is(:hover, .select-option-focused) {
  background-color: color-mix(in oklab, var(--ui-bg-elevated) 50%, transparent);
}

html:not(.dark) .select-dropdown-portal .select-option-group,
html:not(.dark) .select-dropdown-portal .select-option-group:hover {
  background-color: transparent;
  color: var(--ui-text-highlighted);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0;
  text-transform: none;
}

html:not(.dark) .select-dropdown-portal .select-popover-head strong {
  color: var(--ui-text-highlighted);
  letter-spacing: 0;
  text-transform: none;
}
</style>
