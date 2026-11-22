import { webcrypto } from 'node:crypto'
import { describe, expect, it } from 'vitest'

import {
  API_KEY_SEAL_IV_BYTES,
  API_KEY_SEAL_KDF_ITERATIONS,
  API_KEY_SEAL_SALT_BYTES,
  API_KEY_SEAL_TAG_BYTES,
  ApiKeySealDecryptError,
  ApiKeySealFormatError,
  ApiKeySealUnsupportedError,
  isApiKeySealSupported,
  openApiKeySeal,
  sealApiKey,
} from '../apikeySeal'
import type { ApiKeySecretSeal } from '@/types'

const subtle = webcrypto.subtle as unknown as SubtleCrypto
const getRandomValues = <T extends ArrayBufferView>(array: T): T =>
  webcrypto.getRandomValues(array as unknown as Uint8Array) as unknown as T
const env = { subtle, getRandomValues }

const KEY = 'sk-fixture-key'
const PASSWORD = 'correct horse battery staple'
const decodeBase64 = (text: string): Uint8Array => {
  const binary = atob(text)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i)
  return out
}

describe('API key local seal (WebCrypto)', () => {
  it('round-trips the plaintext with the fixed contract parameters', async () => {
    const seal = await sealApiKey(KEY, PASSWORD, env)

    expect(seal.version).toBe(1)
    expect(seal.kdf.algo).toBe('PBKDF2-SHA256')
    expect(seal.kdf.iterations).toBe(API_KEY_SEAL_KDF_ITERATIONS)
    expect(API_KEY_SEAL_KDF_ITERATIONS).toBe(310000)
    expect(decodeBase64(seal.kdf.salt)).toHaveLength(API_KEY_SEAL_SALT_BYTES)

    const body = decodeBase64(seal.sealed)
    expect(body.length).toBeGreaterThan(API_KEY_SEAL_IV_BYTES + API_KEY_SEAL_TAG_BYTES)

    await expect(openApiKeySeal(seal, PASSWORD, env)).resolves.toBe(KEY)
  })

  it('uses a fresh salt and IV for every seal so identical keys never share ciphertext', async () => {
    const first = await sealApiKey(KEY, PASSWORD, env)
    const second = await sealApiKey(KEY, PASSWORD, env)

    expect(first.kdf.salt).not.toBe(second.kdf.salt)
    expect(first.sealed).not.toBe(second.sealed)
    await expect(openApiKeySeal(first, PASSWORD, env)).resolves.toBe(KEY)
    await expect(openApiKeySeal(second, PASSWORD, env)).resolves.toBe(KEY)
  })

  it('rejects a wrong password without leaking whether the password or the payload was wrong', async () => {
    const seal = await sealApiKey(KEY, PASSWORD, env)
    await expect(openApiKeySeal(seal, 'not the password', env)).rejects.toBeInstanceOf(ApiKeySealDecryptError)
  })

  it('rejects a tampered ciphertext/tag', async () => {
    const seal = await sealApiKey(KEY, PASSWORD, env)
    const body = decodeBase64(seal.sealed)
    body[body.length - 1] ^= 0xff
    let tampered = ''
    for (const byte of body) tampered += String.fromCharCode(byte)
    const broken: ApiKeySecretSeal = { ...seal, sealed: btoa(tampered) }

    await expect(openApiKeySeal(broken, PASSWORD, env)).rejects.toBeInstanceOf(ApiKeySealDecryptError)
  })

  it.each([
    { name: 'unsupported version', patch: { version: 2 } },
    { name: 'unknown KDF', patch: { kdf: { algo: 'MD5', iterations: 310000, salt: 'c2FsdA==' } } },
    { name: 'lowered iteration count', patch: { kdf: { algo: 'PBKDF2-SHA256', iterations: 1000, salt: 'c2FsdA==' } } },
    { name: 'empty salt', patch: { kdf: { algo: 'PBKDF2-SHA256', iterations: 310000, salt: '' } } },
    { name: 'short sealed body', patch: { sealed: 'AAAA' } },
    { name: 'invalid base64', patch: { sealed: '!!!' } },
  ])('refuses a malformed seal: $name', async ({ patch }) => {
    const seal = await sealApiKey(KEY, PASSWORD, env)
    const malformed = { ...seal, ...patch } as unknown as ApiKeySecretSeal
    await expect(openApiKeySeal(malformed, PASSWORD, env)).rejects.toBeInstanceOf(ApiKeySealFormatError)
  })

  it('fails loudly instead of degrading when WebCrypto is missing', async () => {
    expect(isApiKeySealSupported({ subtle: null, getRandomValues })).toBe(false)
    await expect(sealApiKey(KEY, PASSWORD, { subtle: null, getRandomValues })).rejects.toBeInstanceOf(
      ApiKeySealUnsupportedError,
    )
    const seal = await sealApiKey(KEY, PASSWORD, env)
    await expect(openApiKeySeal(seal, PASSWORD, { subtle: null, getRandomValues })).rejects.toBeInstanceOf(
      ApiKeySealUnsupportedError,
    )
  })

  it('requires a CSPRNG even when subtle is present', async () => {
    expect(isApiKeySealSupported({ subtle, getRandomValues: null })).toBe(false)
    await expect(sealApiKey(KEY, PASSWORD, { subtle, getRandomValues: null })).rejects.toBeInstanceOf(
      ApiKeySealUnsupportedError,
    )
  })

  it('never emits an all-zero IV even if the CSPRNG is broken', async () => {
    const brokenRandom = <T extends ArrayBufferView>(array: T): T => {
      (array as unknown as Uint8Array).fill(0)
      return array
    }
    await expect(sealApiKey(KEY, PASSWORD, { subtle, getRandomValues: brokenRandom })).rejects.toBeInstanceOf(
      ApiKeySealUnsupportedError,
    )
  })

  it('rejects empty plaintext or password instead of sealing nothing', async () => {
    await expect(sealApiKey('', PASSWORD, env)).rejects.toBeInstanceOf(ApiKeySealFormatError)
    await expect(sealApiKey(KEY, '', env)).rejects.toBeInstanceOf(ApiKeySealFormatError)
  })
})