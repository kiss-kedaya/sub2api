import { webcrypto } from 'node:crypto'
import { describe, expect, it } from 'vitest'

import vectors from './fixtures/goInteropVectors.json'
import { detectSignerCrypto, type SignerCrypto } from '../cryptoProvider'
import { hexToBytes } from '../encoding'
import {
  formatShare,
  combineShares,
  normalizeShareText,
  parseAndCombine,
  parseShare,
  ShareFormatError,
  splitSecret,
  verifySharesOpenWallet,
  type ShardShare
} from '../shardFormat'

const secureEnv = {
  isSecureContext: true,
  getRandomValues: (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array)
}

function backend(name: 'webcrypto' | 'noble'): SignerCrypto {
  return detectSignerCrypto({
    forceBackend: name,
    env: { ...secureEnv, subtle: name === 'webcrypto' ? (webcrypto.subtle as unknown as SubtleCrypto) : null }
  })
}

const backends = [backend('noble'), backend('webcrypto')] as const

function deterministicStream(hex: string): (length: number) => Uint8Array {
  const source = hexToBytes(hex)
  let offset = 0
  return (length: number) => {
    if (offset + length > source.length) throw new Error('randomness stream exhausted')
    const out = source.slice(offset, offset + length)
    offset += length
    return out
  }
}

describe.each(backends.map((crypto, index) => [index === 0 ? 'noble' : 'webcrypto', crypto] as const))(
  '%s backend reproduces the Go golden vectors',
  (_name, crypto) => {
    it.each(vectors.shard_vectors)('$name split/format is byte-identical', async vector => {
      const shares = await splitSecret(
        hexToBytes(vector.secret_hex),
        { threshold: vector.threshold, total: vector.total, random: deterministicStream(vector.randomness_hex) },
        crypto
      )
      const formatted: string[] = []
      for (const share of shares) formatted.push(await formatShare(share, crypto))
      expect(formatted).toEqual(vector.shares)

      const recovered = await parseAndCombine(vector.shares, crypto)
      expect(Array.from(recovered)).toEqual(Array.from(hexToBytes(vector.secret_hex)))
    })

    it('parses every Go share and re-formats it canonically', async () => {
      for (const text of vectors.shard_vectors[1].shares) {
        const share = await parseShare(text, crypto)
        expect(share.version).toBe(1)
        expect(share.threshold).toBe(2)
        expect(share.total).toBe(3)
        expect(share.data).toHaveLength(32)
        expect(await formatShare(share, crypto)).toBe(text)
      }
    })

    it('recovers from any two shares in any order', async () => {
      const secret = hexToBytes(vectors.shard_vectors[2].secret_hex)
      const shares = vectors.shard_vectors[2].shares
      for (const [first, second] of [[0, 1], [0, 2], [1, 2], [2, 0], [1, 0], [2, 1]]) {
        const recovered = await parseAndCombine([shares[first], shares[second]], crypto)
        expect(Array.from(recovered)).toEqual(Array.from(secret))
      }
    })

    it('rejects a single share and duplicate indexes', async () => {
      const shares = await Promise.all(vectors.shard_vectors[0].shares.map(text => parseShare(text, crypto)))
      await expect(parseAndCombine([vectors.shard_vectors[0].shares[0]], crypto)).rejects.toMatchObject({ code: 'threshold' })
      expect(() => combineShares([shares[0], shares[0]])).toThrow(ShareFormatError)
    })

    it('detects a one-character typo through the checksum', async () => {
      const text = vectors.shard_vectors[2].shares[0]
      const position = text.indexOf('-') + 6
      const replacement = text[position] === 'A' ? 'B' : 'A'
      const corrupted = text.slice(0, position) + replacement + text.slice(position + 1)
      await expect(parseShare(corrupted, crypto)).rejects.toMatchObject({ code: expect.stringMatching(/checksum|format/) })
    })

    it('rejects shares from two different splits only via the commitment', async () => {
      const first = await splitSecret(new Uint8Array(32).fill(0x22), {}, crypto)
      const second = await splitSecret(new Uint8Array(32).fill(0x22), {}, crypto)
      const mixed = combineShares([first[0], second[1]] as ShardShare[])
      const verdict = await verifySharesOpenWallet(
        [await formatShare(first[0], crypto), await formatShare(second[1], crypto)],
        vectors.shard_vectors[1].commitment_b64,
        crypto
      )
      expect(verdict.ok).toBe(false)
      expect(mixed).toHaveLength(32)
    })

    it('validates split parameters like the server', async () => {
      await expect(splitSecret(new Uint8Array(32), { threshold: 1, total: 3 }, crypto)).rejects.toMatchObject({ code: 'parameters' })
      await expect(splitSecret(new Uint8Array(32), { threshold: 3, total: 2 }, crypto)).rejects.toMatchObject({ code: 'parameters' })
      await expect(splitSecret(new Uint8Array(0), {}, crypto)).rejects.toMatchObject({ code: 'parameters' })
      await expect(splitSecret(new Uint8Array(256), {}, crypto)).rejects.toMatchObject({ code: 'parameters' })
    })
  }
)

describe('share text normalization', () => {
  it('accepts lowercase, spaced, tabbed and full-width input', async () => {
    const crypto = backends[0]
    const canonical = vectors.shard_vectors[2].shares[0]
    const variants = [
      canonical.toLowerCase(),
      canonical.replace(/-/g, ' \t '),
      `  ${canonical}\n`,
      canonical.replace(/S2V1/, 'Ｓ２Ｖ１').replace(/-/g, '－'),
      `\u200b${canonical}\uFEFF`
    ]
    for (const variant of variants) {
      const parsed = await parseShare(variant, crypto)
      expect(await formatShare(parsed, crypto)).toBe(canonical)
    }
  })

  it('reports an invalid character instead of dropping it', () => {
    expect(() => normalizeShareText(vectors.shard_vectors[2].shares[0].replace(/-/, '@'))).toThrow(ShareFormatError)
    try {
      normalizeShareText('S2V1-ACBA-@@@@')
    } catch (error) {
      expect((error as ShareFormatError).detail).toContain('invalid-character')
    }
  })

  it('rejects wrong prefixes and truncated shares', async () => {
    const crypto = backends[0]
    await expect(parseShare('S3V1-AEBA-GAIC', crypto)).rejects.toMatchObject({ code: 'format' })
    await expect(parseShare('S2V1-', crypto)).rejects.toMatchObject({ code: 'format' })
    await expect(parseShare('not-a-share', crypto)).rejects.toMatchObject({ code: 'format' })
  })
})
