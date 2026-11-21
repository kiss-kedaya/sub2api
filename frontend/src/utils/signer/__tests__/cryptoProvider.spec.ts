import { webcrypto } from 'node:crypto'
import { describe, expect, it } from 'vitest'

import {
  detectSignerCrypto,
  detectSignerCryptoSupport,
  SignerCryptoUnavailableError
} from '../cryptoProvider'

const getRandomValues = (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array)
const subtle = webcrypto.subtle as unknown as SubtleCrypto
const secureEnv = { isSecureContext: true, getRandomValues }

describe('signer crypto capability detection', () => {
  it('disables the flow in a non-secure context, with no weak fallback', () => {
    const support = detectSignerCryptoSupport({ isSecureContext: false })
    expect(support).toEqual({ supported: false, backend: null, reason: 'insecure-context' })
    expect(() => detectSignerCrypto({ env: { isSecureContext: false } })).toThrow(SignerCryptoUnavailableError)
  })

  it('requires a CSPRNG even when WebCrypto would be present', () => {
    const support = detectSignerCryptoSupport({ isSecureContext: true, subtle, getRandomValues: null })
    expect(support.reason).toBe('no-csprng')
    expect(() => detectSignerCrypto({ forceBackend: 'noble', env: { isSecureContext: true, getRandomValues: null } }))
      .toThrow(/no-csprng/)
  })

  it('falls back to noble when WebCrypto subtle is missing', () => {
    const support = detectSignerCryptoSupport({ isSecureContext: true, subtle: null, getRandomValues })
    expect(support).toEqual({ supported: true, backend: 'noble', reason: null })
  })

  it('selects WebCrypto when subtle is available', () => {
    const support = detectSignerCryptoSupport({ isSecureContext: true, subtle, getRandomValues })
    expect(support).toEqual({ supported: true, backend: 'webcrypto', reason: null })
  })
})

describe('WebCrypto and noble produce identical output', () => {
  const webcryptoTwin = detectSignerCrypto({ forceBackend: 'webcrypto', env: { ...secureEnv, subtle } })
  const nobleTwin = detectSignerCrypto({ forceBackend: 'noble', env: { ...secureEnv, subtle: null } })
  const key = new Uint8Array(32).fill(0x5a)
  const nonce = new Uint8Array(12).fill(0x11)
  const aad = new TextEncoder().encode('sub2api-wallet:v3:')
  const plaintext = new TextEncoder().encode('{"deposit":"a","hot":"b"}')

  it('hashes identically', async () => {
    const input = new TextEncoder().encode('sub2api shard interop')
    expect(Array.from(await webcryptoTwin.sha256(input))).toEqual(Array.from(await nobleTwin.sha256(input)))
  })

  it('seals and opens across implementations', async () => {
    const sealedWeb = await webcryptoTwin.aesGcmSeal(key, nonce, plaintext, aad)
    const sealedNoble = await nobleTwin.aesGcmSeal(key, nonce, plaintext, aad)
    expect(Array.from(sealedWeb)).toEqual(Array.from(sealedNoble))
    expect(new TextDecoder().decode(await nobleTwin.aesGcmOpen(key, nonce, sealedWeb, aad))).toBe('{"deposit":"a","hot":"b"}')
    expect(new TextDecoder().decode(await webcryptoTwin.aesGcmOpen(key, nonce, sealedNoble, aad))).toBe('{"deposit":"a","hot":"b"}')
  })

  it('rejects a tampered tag', async () => {
    const sealed = await nobleTwin.aesGcmSeal(key, nonce, plaintext, aad)
    const tampered = sealed.slice()
    tampered[0] ^= 0xff
    await expect(nobleTwin.aesGcmOpen(key, nonce, tampered, aad)).rejects.toBeTruthy()
    await expect(webcryptoTwin.aesGcmOpen(key, nonce, tampered, aad)).rejects.toBeTruthy()
  })

  it('randomBytes uses the shared CSPRNG and never a Math.random fallback', () => {
    const calls: number[] = []
    const crypto = detectSignerCrypto({
      forceBackend: 'noble',
      env: {
        isSecureContext: true,
        subtle: null,
        getRandomValues: <T extends ArrayBufferView>(array: T): T => {
          calls.push((array as Uint8Array).length)
          return webcrypto.getRandomValues(array as Uint8Array) as unknown as T
        }
      }
    })
    const bytes = crypto.randomBytes(32)
    expect(bytes).toHaveLength(32)
    expect(calls).toEqual([32])
  })
})
