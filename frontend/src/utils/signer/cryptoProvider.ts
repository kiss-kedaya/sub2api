/**
 * Crypto runtime for the wallet shard flow.
 *
 * Two implementations share one interface and MUST produce identical bytes:
 *  - `webcrypto`: the browser WebCrypto implementation (AES-256-GCM, SHA-256).
 *  - `noble`: the pure-JS @noble fallback used when WebCrypto is missing
 *    (older Safari / hardened kiosk browsers). It is still a real AES-GCM and
 *    SHA-256 implementation; the share and vault formats do not change.
 *
 * Security rules enforced here:
 *  - A non-secure context (plain http://) disables the flow entirely. We never
 *    fall back to a weak RNG: `crypto.getRandomValues` is required on both paths.
 *  - Without `getRandomValues` the flow is disabled, even if `subtle` exists.
 */

import { gcm } from '@noble/ciphers/aes'
import { sha256 as nobleSha256 } from '@noble/hashes/sha256'
import { toArrayBuffer, utf8Bytes, zeroBytes } from './encoding'

export type SignerCryptoBackend = 'webcrypto' | 'noble'

export type SignerCryptoUnavailableCode = 'insecure-context' | 'no-csprng' | 'no-aes-gcm'

export class SignerCryptoUnavailableError extends Error {
  readonly code: SignerCryptoUnavailableCode

  constructor(code: SignerCryptoUnavailableCode) {
    super(`signer crypto unavailable: ${code}`)
    this.name = 'SignerCryptoUnavailableError'
    this.code = code
  }
}

export interface DetectedSignerCrypto {
  supported: boolean
  backend: SignerCryptoBackend | null
  reason: SignerCryptoUnavailableCode | null
}

export interface SignerCrypto {
  readonly backend: SignerCryptoBackend
  randomBytes(length: number): Uint8Array
  sha256(data: Uint8Array): Promise<Uint8Array>
  /** AES-256-GCM seal; returns ciphertext || 16-byte tag. */
  aesGcmSeal(key: Uint8Array, nonce: Uint8Array, plaintext: Uint8Array, aad: Uint8Array): Promise<Uint8Array>
  /** AES-256-GCM open; `ciphertext` includes the 16-byte tag. */
  aesGcmOpen(key: Uint8Array, nonce: Uint8Array, ciphertext: Uint8Array, aad: Uint8Array): Promise<Uint8Array>
}

export interface SignerCryptoEnvOverrides {
  isSecureContext?: boolean
  subtle?: SubtleCrypto | null
  getRandomValues?: (<T extends ArrayBufferView>(array: T) => T) | null
}

export interface DetectOptions {
  /** Test hook: force one implementation even if the environment lacks it. */
  forceBackend?: SignerCryptoBackend
  env?: SignerCryptoEnvOverrides
}

const SHA256 = 'SHA-256'
const AES_GCM = 'AES-GCM'

function globalCrypto(): Crypto | undefined {
  return typeof globalThis !== 'undefined' ? (globalThis.crypto as Crypto | undefined) : undefined
}

function readSecureContext(env?: SignerCryptoEnvOverrides): boolean | undefined {
  if (env?.isSecureContext !== undefined) return env.isSecureContext
  if (typeof globalThis !== 'undefined' && typeof (globalThis as { isSecureContext?: unknown }).isSecureContext === 'boolean') {
    return (globalThis as { isSecureContext: boolean }).isSecureContext
  }
  return undefined
}

function readGetRandomValues(env?: SignerCryptoEnvOverrides) {
  if (env?.getRandomValues !== undefined) return env.getRandomValues
  const runtime = globalCrypto()
  return runtime && typeof runtime.getRandomValues === 'function'
    ? runtime.getRandomValues.bind(runtime) as <T extends ArrayBufferView>(array: T) => T
    : null
}

function readSubtle(env?: SignerCryptoEnvOverrides): SubtleCrypto | null {
  if (env?.subtle !== undefined) return env.subtle
  const runtime = globalCrypto()
  return runtime?.subtle ?? null
}

/** Non-throwing capability probe used by UI to explain why it is disabled. */
export function detectSignerCryptoSupport(env?: SignerCryptoEnvOverrides): DetectedSignerCrypto {
  if (readSecureContext(env) === false) {
    return { supported: false, backend: null, reason: 'insecure-context' }
  }
  if (!readGetRandomValues(env)) {
    return { supported: false, backend: null, reason: 'no-csprng' }
  }
  const subtle = readSubtle(env)
  if (subtle && typeof subtle.digest === 'function' && typeof subtle.importKey === 'function') {
    return { supported: true, backend: 'webcrypto', reason: null }
  }
  // @noble is always present; only the CSPRNG requirement above can disable us.
  return { supported: true, backend: 'noble', reason: null }
}

function createWebCryptoBackend(getRandomValues: (<T extends ArrayBufferView>(array: T) => T), subtle: SubtleCrypto): SignerCrypto {
  return {
    backend: 'webcrypto',
    randomBytes(length: number): Uint8Array {
      if (!Number.isInteger(length) || length <= 0) throw new Error('invalid random length')
      const out = new Uint8Array(length)
      getRandomValues(out)
      return out
    },
    async sha256(data: Uint8Array): Promise<Uint8Array> {
      const digest = await subtle.digest(SHA256, toArrayBuffer(data))
      return new Uint8Array(digest)
    },
    async aesGcmSeal(key, nonce, plaintext, aad): Promise<Uint8Array> {
      const handle = await subtle.importKey('raw', toArrayBuffer(key), { name: AES_GCM }, false, ['encrypt'])
      const sealed = await subtle.encrypt(
        { name: AES_GCM, iv: toArrayBuffer(nonce), additionalData: toArrayBuffer(aad), tagLength: 128 },
        handle,
        toArrayBuffer(plaintext)
      )
      return new Uint8Array(sealed)
    },
    async aesGcmOpen(key, nonce, ciphertext, aad): Promise<Uint8Array> {
      const handle = await subtle.importKey('raw', toArrayBuffer(key), { name: AES_GCM }, false, ['decrypt'])
      const opened = await subtle.decrypt(
        { name: AES_GCM, iv: toArrayBuffer(nonce), additionalData: toArrayBuffer(aad), tagLength: 128 },
        handle,
        toArrayBuffer(ciphertext)
      )
      return new Uint8Array(opened)
    }
  }
}

function createNobleBackend(getRandomValues: (<T extends ArrayBufferView>(array: T) => T)): SignerCrypto {
  return {
    backend: 'noble',
    randomBytes(length: number): Uint8Array {
      if (!Number.isInteger(length) || length <= 0) throw new Error('invalid random length')
      const out = new Uint8Array(length)
      getRandomValues(out)
      return out
    },
    async sha256(data: Uint8Array): Promise<Uint8Array> {
      return nobleSha256(data)
    },
    async aesGcmSeal(key, nonce, plaintext, aad): Promise<Uint8Array> {
      return gcm(key, nonce, aad).encrypt(plaintext)
    },
    async aesGcmOpen(key, nonce, ciphertext, aad): Promise<Uint8Array> {
      return gcm(key, nonce, aad).decrypt(ciphertext)
    }
  }
}

/**
 * Resolve the crypto implementation or throw a typed unavailable error.
 * Callers must surface the reason and disable the flow; there is no weak path.
 */
export function detectSignerCrypto(options: DetectOptions = {}): SignerCrypto {
  const env = options.env
  if (readSecureContext(env) === false) throw new SignerCryptoUnavailableError('insecure-context')
  const getRandomValues = readGetRandomValues(env)
  if (!getRandomValues) throw new SignerCryptoUnavailableError('no-csprng')
  let backend = options.forceBackend
  if (!backend) {
    const support = detectSignerCryptoSupport(env)
    if (!support.supported || !support.backend) throw new SignerCryptoUnavailableError(support.reason ?? 'no-aes-gcm')
    backend = support.backend
  }
  if (backend === 'noble') return createNobleBackend(getRandomValues)
  const subtle = readSubtle(env)
  if (!subtle) throw new SignerCryptoUnavailableError('no-aes-gcm')
  return createWebCryptoBackend(getRandomValues, subtle)
}

let cached: SignerCrypto | null = null

/** Shared instance for UI code; tests call resetSignerCryptoCache(). */
export function getSignerCrypto(): SignerCrypto {
  if (!cached) cached = detectSignerCrypto()
  return cached
}

export function resetSignerCryptoCache(): void {
  cached = null
}

/** Clear a key from memory after use (best effort; arrays only). */
export function wipeKey(key: Uint8Array | null | undefined): void {
  zeroBytes(key)
}

/** Domain-separated SHA-256 commitment for the wallet data key. */
export const DK_COMMITMENT_DOMAIN = 'sub2api/crypto-wallet/dk-commitment/v1:'

export async function dataKeyCommitment(crypto: SignerCrypto, dataKey: Uint8Array): Promise<Uint8Array> {
  return crypto.sha256(new Uint8Array([...utf8Bytes(DK_COMMITMENT_DOMAIN), ...dataKey]))
}
