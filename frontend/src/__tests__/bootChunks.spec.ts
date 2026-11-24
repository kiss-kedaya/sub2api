// @vitest-environment node
// (vite.config.ts pulls in esbuild, which refuses to load inside jsdom)
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { createBootGraph } from '../../vite.config'

type Mod = { isEntry?: boolean; imports?: string[]; dynamicImports?: string[] }

// A module graph in the shape Rollup hands to `manualChunks`: static imports are `importedIds`, `import()` is
// `dynamicallyImportedIds` and is a different list, which is the whole point of the helper.
function graph(modules: Record<string, Mod>) {
  return {
    getModuleIds: vi.fn(() => Object.keys(modules)[Symbol.iterator]()),
    getModuleInfo: (id: string) =>
      modules[id]
        ? { isEntry: !!modules[id].isEntry, importedIds: modules[id].imports ?? [], dynamicallyImportedIds: modules[id].dynamicImports ?? [] }
        : null,
  }
}

const lib = (name: string) => `/repo/node_modules/${name}/index.js`

describe('createBootGraph (which third-party modules does the first page load need?)', () => {
  const modules: Record<string, Mod> = {
    '/repo/src/main.ts': { isEntry: true, imports: ['/repo/src/api.ts'], dynamicImports: ['/repo/src/views/Keys.vue'] },
    '/repo/src/api.ts': { imports: [lib('axios'), lib('shared')] },
    [lib('axios')]: { imports: [lib('axios') + '/lib/adapter.js'] },
    [lib('axios') + '/lib/adapter.js']: {},
    [lib('shared')]: {},
    '/repo/src/views/Keys.vue': { imports: [lib('qrcode'), lib('shared')], dynamicImports: ['/repo/src/views/Pay.vue'] },
    [lib('qrcode')]: { imports: [lib('qrcode') + '/lib/encoder.js'] },
    [lib('qrcode') + '/lib/encoder.js']: {},
    '/repo/src/views/Pay.vue': { imports: [lib('stripe')] },
    [lib('stripe')]: {},
  }

  it('includes what the entry reaches through static imports, however deep', () => {
    const ctx = graph(modules)
    const atBoot = createBootGraph()
    expect(atBoot(lib('axios'), ctx)).toBe(true)
    expect(atBoot(lib('axios') + '/lib/adapter.js', ctx)).toBe(true)
  })

  it('leaves out what only a lazily loaded route (or a route of a route) imports', () => {
    const ctx = graph(modules)
    const atBoot = createBootGraph()
    expect(atBoot(lib('qrcode'), ctx)).toBe(false)
    expect(atBoot(lib('qrcode') + '/lib/encoder.js', ctx)).toBe(false)
    expect(atBoot(lib('stripe'), ctx)).toBe(false)
    expect(atBoot('/repo/src/views/Keys.vue', ctx)).toBe(false)
  })

  it('keeps a library that the first page and a lazy route both use in the boot set', () => {
    const ctx = graph(modules)
    expect(createBootGraph()(lib('shared'), ctx)).toBe(true)
  })

  it('terminates on import cycles', () => {
    const ctx = graph({
      '/repo/src/main.ts': { isEntry: true, imports: [lib('a')] },
      [lib('a')]: { imports: [lib('b')] },
      [lib('b')]: { imports: [lib('a'), '/repo/src/main.ts'] },
    })
    const atBoot = createBootGraph()
    expect(atBoot(lib('b'), ctx)).toBe(true)
  })

  it('walks the graph once, not once per module Rollup asks about', () => {
    const ctx = graph(modules)
    const atBoot = createBootGraph()
    for (const id of Object.keys(modules)) atBoot(id, ctx)
    expect(ctx.getModuleIds).toHaveBeenCalledTimes(1)
  })

  it('treats an id Rollup has no info for as not loaded at boot', () => {
    expect(createBootGraph()('/repo/node_modules/ghost/index.js', graph(modules))).toBe(false)
  })
})

describe('vite.config.ts', () => {
  const config = readFileSync(resolve(__dirname, '../../vite.config.ts'), 'utf8')

  // Without the condition the fallback is the union of every route's third-party code in one file that every visitor
  // downloads before the first paint (about 1.5 MB before compression at the time of writing).
  it('only puts third-party modules into the shared vendor-misc chunk when the first page load needs them', () => {
    const fallback = config.indexOf("return 'vendor-misc'")
    expect(fallback).toBeGreaterThan(0)
    const before = config.slice(Math.max(0, fallback - 120), fallback)
    expect(before).toMatch(/if \(!isLoadedAtBoot\(id, ctx\)\) return undefined\s*$/)
  })
})
