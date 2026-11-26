import { describe, expect, it } from 'vitest'
import { consolePageKey, isConsoleLayoutRoute } from '../consoleShell'

describe('console shell', () => {
  it('wraps authenticated routes and authenticated embedded model plaza without shell teardown', () => {
    expect(isConsoleLayoutRoute({ requiresAuth: true })).toBe(true)
    expect(isConsoleLayoutRoute({ requiresAuth: false })).toBe(false)
    expect(isConsoleLayoutRoute({})).toBe(false)
    expect(isConsoleLayoutRoute(undefined)).toBe(false)

    // Embedded model plaza keeps the persistent console shell when signed in
    expect(isConsoleLayoutRoute({ requiresAuth: false }, { path: '/model-plaza', query: { embedded: '1' } }, true)).toBe(true)
    // Anonymous or non-embedded model plaza stays outside the console shell
    expect(isConsoleLayoutRoute({ requiresAuth: false }, { path: '/model-plaza', query: { embedded: '1' } }, false)).toBe(false)
    expect(isConsoleLayoutRoute({ requiresAuth: false }, { path: '/model-plaza', query: {} }, true)).toBe(false)
  })

  it('keeps the same page key when only the query or id changes', () => {
    const pattern = '/tickets/:id(\\d+)?'
    expect(consolePageKey({ matched: [{ path: pattern }], path: '/tickets/1' })).toBe(pattern)
    expect(consolePageKey({ matched: [{ path: pattern }], path: '/tickets/2?tab=chat' })).toBe(pattern)
    expect(consolePageKey({ matched: [{ path: '/admin/accounts' }], path: '/admin/accounts' })).toBe('/admin/accounts')
  })
})