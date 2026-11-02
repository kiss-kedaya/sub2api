import { describe, expect, it } from 'vitest'
import { resolveInternalNavigation } from '../navigationLinks'

function link(href: string, attrs: Record<string, string> = {}): HTMLAnchorElement {
  const anchor = document.createElement('a')
  anchor.setAttribute('href', href)
  for (const [name, value] of Object.entries(attrs)) anchor.setAttribute(name, value)
  return anchor
}

describe('internal anchor fallback', () => {
  it('keeps same-origin links inside the router', () => {
    expect(resolveInternalNavigation(link('/tickets/1?tab=chat#m2'), window.location)).toBe('/tickets/1?tab=chat#m2')
  })

  it('leaves links that need the browser alone', () => {
    expect(resolveInternalNavigation(link('https://example.com/x'), window.location)).toBeNull()
    expect(resolveInternalNavigation(link('/tickets', { target: '_blank' }), window.location)).toBeNull()
    expect(resolveInternalNavigation(link('/file.csv', { download: '' }), window.location)).toBeNull()
    expect(resolveInternalNavigation(link('/api/v1/export'), window.location)).toBeNull()
    expect(resolveInternalNavigation(link('#section'), window.location)).toBeNull()
    expect(resolveInternalNavigation(link('/tickets', { 'data-native-navigation': '' }), window.location)).toBeNull()
  })
})