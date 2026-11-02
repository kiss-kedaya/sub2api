import { describe, expect, it } from 'vitest'
import { consolePageKey, isConsoleLayoutRoute } from '../consoleShell'

describe('console shell', () => {
  it('wraps authenticated routes only', () => {
    expect(isConsoleLayoutRoute({ requiresAuth: true })).toBe(true)
    expect(isConsoleLayoutRoute({ requiresAuth: false })).toBe(false)
    expect(isConsoleLayoutRoute({})).toBe(false)
    expect(isConsoleLayoutRoute(undefined)).toBe(false)
  })

  it('keeps the same page key when only the query or id changes', () => {
    const pattern = '/tickets/:id(\\d+)?'
    expect(consolePageKey({ matched: [{ path: pattern }], path: '/tickets/1' })).toBe(pattern)
    expect(consolePageKey({ matched: [{ path: pattern }], path: '/tickets/2?tab=chat' })).toBe(pattern)
    expect(consolePageKey({ matched: [{ path: '/admin/accounts' }], path: '/admin/accounts' })).toBe('/admin/accounts')
  })
})