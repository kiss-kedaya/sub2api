import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
// Docs moved from the navbar to the sidebar's bottom group, declared in consoleNav.tsx.
const sidebarSource = readFileSync(resolve(dir, '../consoleNav.tsx'), 'utf8')
const homeViewSource = readFileSync(resolve(dir, '../../../views/HomeView.vue'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')

describe('doc_url sanitization', () => {
  it('the sidebar nav imports the sanitized internal docs fallback', () => {
    expect(sidebarSource).toContain("import { resolveDocsUrl } from '@/utils/docsUrl'")
  })

  it('the sidebar nav resolves its configured docs URL', () => {
    expect(sidebarSource).toContain('resolveDocsUrl(appStore.docUrl)')
  })

  it('HomeView imports sanitizeUrl', () => {
    expect(homeViewSource).toContain("import { sanitizeUrl } from '@/utils/url'")
  })

  it('HomeView resolves configured docs without hiding the internal fallback', () => {
    expect(homeViewSource).toContain('resolveDocsUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl')
  })

  it('KeyUsageView imports sanitizeUrl', () => {
    expect(keyUsageViewSource).toContain("import { sanitizeUrl } from '@/utils/url'")
  })

  it('KeyUsageView resolves configured docs without hiding the internal fallback', () => {
    expect(keyUsageViewSource).toContain('resolveDocsUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl')
  })
})
