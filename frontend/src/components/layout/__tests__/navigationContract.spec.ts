import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const here = dirname(fileURLToPath(import.meta.url))
const read = (path: string) => readFileSync(resolve(here, path), 'utf8')

describe('navigation contract', () => {
  it('does not hard-navigate from console navigation or ticket switching', () => {
    const owned = [
      '../AppSidebar.vue',
      '../AppHeader.vue',
      '../PageNavbar.vue',
      '../UserMenu.vue',
      '../ConsoleNavigationSearch.vue',
      '../../../views/TicketsView.vue',
      '../../../composables/useTicketWorkspace.ts'
    ]
    for (const path of owned) {
      const source = read(path)
      expect(source, path).not.toMatch(/window\.location\.(?:href|assign|replace)|location\.href\s*=/)
    }
  })

  it('mounts the console shell once and keys pages by route pattern', () => {
    const app = read('../../../App.vue')
    expect(app).toContain('<UApp :locale="nuxtUiLocale">')
    expect(app).toContain('<AppLayout v-if="consoleShell">')
    expect(app).toContain('useClientAnchorNavigation(router)')
    expect(read('../AppLayout.vue')).toContain('CONSOLE_SHELL_KEY')
  })
})
