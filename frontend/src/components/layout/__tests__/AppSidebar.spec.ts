import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')
// The nav declarations (groups and pages) live in consoleNav.tsx, shared with the Ctrl/⌘K palette.
const navPath = resolve(dirname(fileURLToPath(import.meta.url)), '../consoleNav.tsx')
const navSource = readFileSync(navPath, 'utf8')

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
    expect(componentSource).toContain("child.kind === 'section'\n        ? sectionItem(child)")
    expect(componentSource).toContain('sidebar-child-section')
    expect(componentSource).toContain('v-if="row.section"')
  })

  it('groups the former operations-center tiers into their own sidebar groups', () => {
    // 运营中心 used to hold four tiers (accounts, channels, diagnostics, platform tools); they are
    // now groups of their own, with the overview page first in "运营".
    expect(navSource).toContain("link('/admin/ops-center', text('运营总览', 'Operations overview'))")
    for (const key of ['admin-overview', 'admin-accounts', 'admin-billing', 'admin-operations', 'admin-monitoring', 'admin-security', 'admin-system']) {
      expect(navSource).toContain(`key: '${key}'`)
    }
    expect(navSource).toContain("link('/admin/account-ops', text('账号通知', 'Account notifications'))")
    expect(navSource).toContain("link('/admin/channel-operations', text('渠道运维', 'Channel operations'))")
    expect(navSource).toContain("link('/admin/connections', text('连接诊断', 'Connections'))")
    expect(navSource).toContain("link('/admin/serverless', text('地区路由', 'Regional routing'))")
  })

  it('no longer shows the standalone ops entry next to the group', () => {
    expect(navSource).not.toContain("link('/admin/ops', t('nav.ops')")
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
    expect(navSource).not.toContain("link('/subscriptions'")
  })

  it('includes Infinite Canvas next to API keys', () => {
    expect(navSource).toContain("link('/keys', t('nav.apiKeys')),\n          link('/infinite-canvas', t('nav.infiniteCanvas'))")
  })
})

describe('AppSidebar icons', () => {
  it('names only icons that exist, in a file the icon bundler scans (.tsx)', () => {
    // The client icon bundle is built from the icon names found in .vue / .tsx sources.
    expect(navPath.endsWith('.tsx')).toBe(true)
    const lucide = JSON.parse(readFileSync(resolve(process.cwd(), 'node_modules/@iconify-json/lucide/icons.json'), 'utf8'))
    const known = new Set([...Object.keys(lucide.icons), ...Object.keys(lucide.aliases ?? {})])
    const used = [...new Set([...navSource.matchAll(/i-lucide-([a-z0-9-]+)/g)].map((match) => match[1]))]
    expect(used.length).toBeGreaterThan(10)
    expect(used.filter((name) => !known.has(name))).toEqual([])
  })
})
