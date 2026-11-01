import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar collapsible groups', () => {
  it('lets the user collapse a group even while a child route is active', () => {
    // The expand state must come from the user's override first, falling back
    // to the active-route heuristic only when the user has not clicked yet.
    expect(componentSource).toContain('const groupExpandOverrides = ref<Map<string, boolean>>(new Map())')
    expect(componentSource).not.toContain('expandedGroups.value.has(item.path) || isGroupActive(item)')
  })
})

describe('AppSidebar operations center group', () => {
  it('renders tier headers inside a collapsible group', () => {
    // Section children become UNavigationMenu label items (expanded) and flyout headers (rail).
    expect(componentSource).toContain("child.section\n          ? { type: 'label'")
    expect(componentSource).toContain('sidebar-child-section')
    expect(componentSource).toContain('v-if="row.section"')
  })

  it('groups account, channel and diagnostics entries under one expandable parent', () => {
    expect(componentSource).toContain("path: '/admin/ops-center'")
    expect(componentSource).toContain("label: locale.value === 'zh' ? '运营中心' : 'Operations center'")
    expect(componentSource).toContain("path: '#ops-tier-accounts'")
    expect(componentSource).toContain("path: '#ops-tier-channels'")
    expect(componentSource).toContain("path: '#ops-tier-diagnostics'")
    expect(componentSource).toContain("path: '#ops-tier-platform'")
  })

  it('no longer shows the standalone ops entry next to the group', () => {
    expect(componentSource).not.toContain("{ path: '/admin/ops', label: t('nav.ops')")
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    // The header is UDashboardSidebar's own (no overflow); the old `@utility sidebar-header`
    // is gone from style.css, so nothing there can clip the badge either.
    const sidebarHeaderBlockMatch = styleSource.match(/@utility sidebar-header\s*\{[\s\S]*?\n\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
    expect(componentSource).toContain('<VersionBadge')
  })
})

describe('AppSidebar user navigation', () => {
  it('hides the legacy subscriptions entry while keeping the route available', () => {
    expect(componentSource).not.toContain("{ path: '/subscriptions'")
  })

  it('includes Infinite Canvas next to API keys', () => {
    expect(componentSource).toContain("{ path: '/infinite-canvas'")
    expect(componentSource).toContain("t('nav.infiniteCanvas')")
  })
})

describe('AppSidebar desktop sizing', () => {
  it('pins the expanded rail to a readable width instead of the resizable default', () => {
    // UDashboardSidebar's resizable size defaulted to 15rem with a 10rem floor, and the
    // stored value survived reloads, so the console sidebar could collapse into a strip.
    expect(componentSource).toContain(".console-sidebar:not([data-collapsed='true']) {\n    width: 14rem;")
    expect(componentSource).toContain('@media (min-width: 1024px)')
    expect(componentSource).not.toMatch(/\n\s+resizable\b/)
  })

  it('restores the main-branch row density for expanded navigation links', () => {
    expect(componentSource).toContain('min-height: 2.5rem;')
    expect(componentSource).toContain("gap: 0.75rem;")
    expect(componentSource).toContain(".console-sidebar:not([data-collapsed='true']) [data-slot='link']")
  })
})
