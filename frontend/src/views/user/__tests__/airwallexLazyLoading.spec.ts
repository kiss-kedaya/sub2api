import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const frontendRoot = resolve(__dirname, '../../../..')

function readFrontendFile(path: string): string {
  return readFileSync(resolve(frontendRoot, path), 'utf8')
}

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    if (statSync(full).isDirectory()) return name === '__tests__' ? [] : sourceFiles(full)
    return /\.(ts|vue)$/.test(name) ? [full] : []
  })
}

// The Airwallex SDK injects two scripts from static.airwallex.com / checkout.airwallex.com as soon as its module is
// evaluated. Anything that makes it part of the shared `vendor-misc` chunk makes every page view of every user call a
// third-party host (measured: 2 requests and ~134 KB more per page, ~30% longer to network idle).
describe('Airwallex lazy-loading contract', () => {
  it('only the payment page imports the SDK, and only dynamically', () => {
    const importers = sourceFiles(resolve(frontendRoot, 'src'))
      .map((file) => ({ file, source: readFileSync(file, 'utf8') }))
      .filter(({ source }) => source.includes('@airwallex/'))

    expect(importers.map(({ file }) => file.replace(/\\/g, '/').split('/src/')[1])).toEqual(['views/user/AirwallexPaymentView.vue'])
    const source = importers[0].source
    expect(source).toContain("await import('@airwallex/components-sdk')")
    expect(source).not.toMatch(/^import .* from ['"]@airwallex\//m)
  })

  it('keeps the SDK out of the shared vendor chunk', () => {
    const viteConfig = readFrontendFile('vite.config.ts')
    const rule = viteConfig.indexOf("id.includes('/@airwallex/')")
    const miscFallback = viteConfig.indexOf("return 'vendor-misc'")

    expect(rule).toBeGreaterThan(-1)
    expect(viteConfig.slice(rule, miscFallback)).toContain("return 'vendor-airwallex'")
    expect(rule).toBeLessThan(miscFallback)
  })
})
