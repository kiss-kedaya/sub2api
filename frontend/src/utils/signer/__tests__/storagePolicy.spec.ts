import { describe, expect, it } from 'vitest'

/**
 * Hard UX requirement: shard material, the data key and derived keys must
 * never reach persistent or semi-persistent browser storage. There is exactly
 * one place to enforce it mechanically: the source, not just a runtime mock.
 */
const sourceFiles = import.meta.glob(['../**/*.{ts,vue}', '../../../components/crypto/signer/**/*.{ts,vue}', '../../../api/admin/cryptoSigner.ts'], {
  query: '?raw',
  import: 'default',
  eager: true
}) as Record<string, string>

const productionFiles = Object.entries(sourceFiles).filter(([path]) => !path.includes('/__tests__/'))

describe('shard material must not be persisted', () => {
  it('finds the signer sources (guards against a broken glob)', () => {
    expect(productionFiles.length).toBeGreaterThanOrEqual(10)
    expect(productionFiles.some(([path]) => path.endsWith('shardFormat.ts'))).toBe(true)
    expect(productionFiles.some(([path]) => path.endsWith('SignerShardSetupWizard.vue'))).toBe(true)
  })

  it('never references localStorage, sessionStorage, IndexedDB or cookies', () => {
    const forbidden = /\b(localStorage|sessionStorage|indexedDB|document\.cookie)\b/
    const offenders = productionFiles
      .filter(([, source]) => forbidden.test(source))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })

  it('never puts share material in the URL', () => {
    const forbidden = /(location\.(href|search|hash)|history\.(pushState|replaceState)|URLSearchParams)/
    const offenders = productionFiles
      .filter(([, source]) => forbidden.test(source))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })

  it('does not use Math.random for key material', () => {
    const offenders = productionFiles
      .filter(([, source]) => /Math\.random\s*\(/.test(source))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })
})
