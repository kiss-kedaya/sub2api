import { computed, reactive, type ComputedRef } from 'vue'
import type { DropdownMenuItem } from '@nuxt/ui'
import type { Column } from '@/components/common/types'

/**
 * "显示列" menu for admin list pages (Nuxt UI dashboard template): a UDropdownMenu of
 * checkboxes that hides columns and remembers the choice in localStorage. Fixed columns
 * (identity, actions) always stay visible and are not listed.
 */
export function useListColumnMenu(options: {
  storageKey: string
  columns: ComputedRef<Column[]>
  defaultHidden?: string[]
  fixed?: string[]
}) {
  const fixed = new Set(options.fixed ?? [])
  const hidden = reactive(new Set<string>(load()))

  function load(): string[] {
    try {
      const raw = localStorage.getItem(options.storageKey)
      if (raw) {
        const parsed = JSON.parse(raw)
        if (Array.isArray(parsed)) return parsed.filter((key): key is string => typeof key === 'string')
      }
    } catch {
      // Unreadable settings fall back to the defaults.
    }
    return options.defaultHidden ?? []
  }

  function save() {
    try {
      localStorage.setItem(options.storageKey, JSON.stringify([...hidden]))
    } catch {
      // Storage full or blocked: the choice still applies for this visit.
    }
  }

  const isVisible = (key: string) => fixed.has(key) || !hidden.has(key)

  function setVisible(key: string, visible: boolean) {
    if (fixed.has(key)) return
    if (visible) hidden.delete(key)
    else hidden.add(key)
    save()
  }

  const visibleColumns = computed(() => options.columns.value.filter((col) => isVisible(col.key)))

  const menuItems = computed<DropdownMenuItem[]>(() =>
    options.columns.value
      .filter((col) => !fixed.has(col.key) && col.label)
      .map((col) => ({
        label: col.label,
        type: 'checkbox' as const,
        checked: isVisible(col.key),
        onUpdateChecked: (checked: boolean) => setVisible(col.key, checked),
        onSelect: (event: Event) => event.preventDefault(),
      })),
  )

  return { hidden, isVisible, setVisible, visibleColumns, menuItems }
}
