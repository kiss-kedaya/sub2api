import { webcrypto } from 'node:crypto'
import { describe, expect, it } from 'vitest'

import vectors from './fixtures/goInteropVectors.json'
import { detectSignerCrypto } from '../cryptoProvider'
import { base64ToBytes, hexToBytes } from '../encoding'
import {
  buildSignerSetupPayload,
  generateSignerShardSet,
  matchesShownShare,
  recoverDataKeyFromShares,
  shareSlot,
  shareTextForSlot,
  wipeSignerShardSet
} from '../material'
import { parseAndCombine } from '../shardFormat'

const crypto = detectSignerCrypto({
  forceBackend: 'noble',
  env: {
    isSecureContext: true,
    getRandomValues: (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array),
    subtle: null
  }
})

function stream(hex: string): (length: number) => Uint8Array {
  const source = hexToBytes(hex)
  let offset = 0
  return (length: number) => {
    const out = source.slice(offset, offset + length)
    offset += length
    return out
  }
}

describe('signer material', () => {
  const golden = vectors.shard_vectors[1]

  it('generates the same set as the Go kernel for a fixed key and coefficient stream', async () => {
    const set = await generateSignerShardSet(crypto, {
      dataKey: hexToBytes(golden.secret_hex),
      random: stream(golden.randomness_hex)
    })
    expect(set.shareTexts).toEqual(golden.shares)
    expect(set.commitment).toBe(golden.commitment_b64)
    expect(shareSlot(1)).toBe('A')
    expect(shareSlot(2)).toBe('B')
    expect(shareSlot(3)).toBe('C')
    expect(shareTextForSlot(set, 'C')).toBe(golden.shares[2])
    wipeSignerShardSet(set)
    expect(Array.from(set.dataKey)).toEqual(Array.from(new Uint8Array(32)))
  })

  it('recovers the data key from any two shares and verifies the commitment', async () => {
    const forPair = await recoverDataKeyFromShares(crypto, [golden.shares[1], golden.shares[2]], golden.commitment_b64)
    expect(Array.from(forPair)).toEqual(Array.from(hexToBytes(golden.secret_hex)))
    await expect(recoverDataKeyFromShares(crypto, [golden.shares[1], golden.shares[2]], vectors.shard_vectors[0].commitment_b64))
      .rejects.toThrow(/commitment/)
  })

  it('builds a setup payload that only carries share A plus the typed-back share', async () => {
    const set = await generateSignerShardSet(crypto, {
      dataKey: hexToBytes(golden.secret_hex),
      random: stream(golden.randomness_hex)
    })
    const payload = await buildSignerSetupPayload(crypto, {
      dataKey: set.dataKey,
      shareA: shareTextForSlot(set, 'A'),
      confirmShare: shareTextForSlot(set, 'C'),
      commitment: set.commitment,
      totpCode: '123456',
      riskPhrase: '我已离线保存分片，丢失无法找回'
    })
    expect(payload.data_key).toBe(btoa(String.fromCharCode(...set.dataKey)))
    expect(payload.share_a).toBe(golden.shares[0])
    expect(payload.confirm_share).toBe(golden.shares[2])
    expect(payload.dk_commitment).toBe(golden.commitment_b64)
    // The API body never contains the untyped-back operator share.
    expect(JSON.stringify(payload)).not.toContain(golden.shares[1])
    expect(base64ToBytes(payload.data_key)).toHaveLength(32)
  })

  it('rejects a backfilled share that was not shown', async () => {
    const set = await generateSignerShardSet(crypto, {
      dataKey: hexToBytes(golden.secret_hex),
      random: stream(golden.randomness_hex)
    })
    expect(await matchesShownShare(crypto, golden.shares[2], [set.shareTexts[1], set.shareTexts[2]])).toBe(1)
    expect(await matchesShownShare(crypto, golden.shares[0], [set.shareTexts[1], set.shareTexts[2]])).toBeNull()
    expect(await matchesShownShare(crypto, 'S2V1-BROKEN', [set.shareTexts[1]])).toBeNull()
  })

  it('produces a valid 2-of-3 set from the real CSPRNG', async () => {
    const set = await generateSignerShardSet(crypto)
    expect(set.shareTexts).toHaveLength(3)
    const recovered = await parseAndCombine([set.shareTexts[0], set.shareTexts[2]], crypto)
    expect(Array.from(recovered)).toEqual(Array.from(set.dataKey))
    const single = await parseAndCombine([set.shareTexts[1]], crypto).catch(error => error)
    expect(single).toMatchObject({ code: 'threshold' })
    wipeSignerShardSet(set)
  })
})
