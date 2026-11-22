/**
 * Local-only seal for API key plaintext.
 *
 * The server never receives the login password and never stores the API key in
 * plaintext. When the user opts in, the browser derives an AES-256-GCM key from
 * the login password with PBKDF2-SHA256 and uploads only
 * `{ version, kdf: { algo, iterations, salt }, sealed }`, where
 * `sealed = base64(iv || ciphertext || tag)` and the plaintext is the `sk-...`
 * key string. Reading it back is a purely local operation: fetch the opaque
 * ciphertext and decrypt it in the browser.
 *
 * Contract (fixed with the backend PR):
 *  - PBKDF2-SHA256, 310000 iterations, 16-byte random salt
 *  - AES-256-GCM, 12-byte random IV, 128-bit tag, no AAD
 *  - base64 for salt and for `iv || ciphertext || tag`
 *
 * Security rules:
 *  - WebCrypto (`crypto.subtle`) is mandatory. There is no pure-JS fallback and
 *    no weak randomness: without it we throw and the UI tells the user the
 *    browser cannot use the feature.
 *  - Randomness comes from `crypto.getRandomValues` only.
 *  - Nothing here touches localStorage / sessionStorage / IndexedDB; callers
 *    keep the plaintext in memory for the current session only.
 */

import type { ApiKeySecretSeal } from '@/types'
import { base64ToBytes, bytesToBase64, concatBytes, toArrayBuffer, utf8Bytes, utf8Text } from './signer/encoding'

export const API_KEY_SEAL_VERSION = 1
export const API_KEY_SEAL_KDF_ALGO = 'PBKDF2-SHA256'
export const API_KEY_SEAL_KDF_ITERATIONS = 310000
export const API_KEY_SEAL_SALT_BYTES = 16
export const API_KEY_SEAL_IV_BYTES = 12
/** AES-GCM appends a 16-byte tag to the ciphertext. */
export const API_KEY_SEAL_TAG_BYTES = 16

/** The runtime cannot do the required WebCrypto operations. Never degrade. */
export class ApiKeySealUnsupportedError extends Error {
  readonly code = 'apikey-seal-unsupported'

  constructor(message = 'WebCrypto is required to seal API keys') {
    super(message)
    this.name = 'ApiKeySealUnsupportedError'
  }
}

/** The stored seal is malformed, truncated or uses an unsupported parameter set. */
export class ApiKeySealFormatError extends Error {
  readonly code = 'apikey-seal-format'

  constructor(message = 'Stored API key seal is malformed') {
    super(message)
    this.name = 'ApiKeySealFormatError'
  }
}

/**
 * Wrong password or tampered ciphertext. AES-GCM cannot tell those apart and we
 * must not leak which one it was: both look exactly like this error.
 */
export class ApiKeySealDecryptError extends Error {
  readonly code = 'apikey-seal-decrypt'

  constructor(message = 'Unable to decrypt the sealed API key') {
    super(message)
    this.name = 'ApiKeySealDecryptError'
  }
}

export interface ApiKeySealCryptoEnv {
  subtle?: SubtleCrypto | null
  getRandomValues?: (<T extends ArrayBufferView>(array: T) => T) | null
}

interface ResolvedSealCrypto {
  subtle: SubtleCrypto
  getRandomValues: <T extends ArrayBufferView>(array: T) => T
}

function resolveSealCrypto(env?: ApiKeySealCryptoEnv): ResolvedSealCrypto {
  const runtime = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined
  const subtle = env?.subtle !== undefined ? env.subtle : runtime?.subtle ?? null
  const getRandomValues =
    env?.getRandomValues !== undefined
      ? env.getRandomValues
      : typeof runtime?.getRandomValues === 'function'
        ? runtime.getRandomValues.bind(runtime)
        : null
  if (!subtle || !getRandomValues) throw new ApiKeySealUnsupportedError()
  return { subtle, getRandomValues }
}

/** Pure capability probe for the UI, without throwing. */
export function isApiKeySealSupported(env?: ApiKeySealCryptoEnv): boolean {
  try {
    const { subtle } = resolveSealCrypto(env)
    return typeof subtle.importKey === 'function' && typeof subtle.deriveKey === 'function'
  } catch {
    return false
  }
}

function randomBytes(length: number, getRandomValues: ResolvedSealCrypto['getRandomValues']): Uint8Array {
  const out = new Uint8Array(length)
  getRandomValues(out)
  // A broken implementation that leaves zeroes must fail loudly, not weaken the seal.
  if (out.every((byte) => byte === 0)) throw new ApiKeySealUnsupportedError('CSPRNG returned all-zero bytes')
  return out
}

async function deriveAesKey(
  subtle: SubtleCrypto,
  password: string,
  salt: Uint8Array,
  usage: KeyUsage[],
): Promise<CryptoKey> {
  const baseKey = await subtle.importKey('raw', toArrayBuffer(utf8Bytes(password)), 'PBKDF2', false, ['deriveKey'])
  return subtle.deriveKey(
    {
      name: 'PBKDF2',
      hash: 'SHA-256',
      salt: toArrayBuffer(salt),
      iterations: API_KEY_SEAL_KDF_ITERATIONS,
    },
    baseKey,
    { name: 'AES-GCM', length: 256 },
    false,
    usage,
  )
}

function assertSealPayload(payload: ApiKeySecretSeal): void {
  if (!payload || typeof payload !== 'object') throw new ApiKeySealFormatError()
  if (payload.version !== API_KEY_SEAL_VERSION) throw new ApiKeySealFormatError()
  const kdf = payload.kdf
  if (!kdf || typeof kdf !== 'object') throw new ApiKeySealFormatError()
  if (kdf.algo !== API_KEY_SEAL_KDF_ALGO) throw new ApiKeySealFormatError()
  // Refuse a downgraded iteration count: the server must not be able to weaken the KDF.
  if (kdf.iterations !== API_KEY_SEAL_KDF_ITERATIONS) throw new ApiKeySealFormatError()
  if (typeof kdf.salt !== 'string' || typeof payload.sealed !== 'string') throw new ApiKeySealFormatError()
}

/**
 * Encrypt a plaintext API key with the login password.
 *
 * The returned payload is exactly what `PUT /api/v1/user/api-keys/:id/secret-seal`
 * expects. The password is used locally and is never part of the returned value.
 */
export async function sealApiKey(
  plaintext: string,
  password: string,
  env?: ApiKeySealCryptoEnv,
): Promise<ApiKeySecretSeal> {
  if (!plaintext) throw new ApiKeySealFormatError('empty API key')
  if (!password) throw new ApiKeySealFormatError('empty password')
  const { subtle, getRandomValues } = resolveSealCrypto(env)
  const salt = randomBytes(API_KEY_SEAL_SALT_BYTES, getRandomValues)
  const iv = randomBytes(API_KEY_SEAL_IV_BYTES, getRandomValues)
  const key = await deriveAesKey(subtle, password, salt, ['encrypt'])
  const sealedBytes = new Uint8Array(
    await subtle.encrypt(
      { name: 'AES-GCM', iv: toArrayBuffer(iv), tagLength: 128 },
      key,
      toArrayBuffer(utf8Bytes(plaintext)),
    ),
  )
  return {
    version: API_KEY_SEAL_VERSION,
    kdf: {
      algo: API_KEY_SEAL_KDF_ALGO,
      iterations: API_KEY_SEAL_KDF_ITERATIONS,
      salt: bytesToBase64(salt),
    },
    sealed: bytesToBase64(concatBytes(iv, sealedBytes)),
  }
}

/**
 * Decrypt a stored seal with the login password. Throws `ApiKeySealDecryptError`
 * for a wrong password or tampered payload, and `ApiKeySealFormatError` for a
 * structurally invalid seal.
 */
export async function openApiKeySeal(
  payload: ApiKeySecretSeal,
  password: string,
  env?: ApiKeySealCryptoEnv,
): Promise<string> {
  assertSealPayload(payload)
  if (!password) throw new ApiKeySealDecryptError()
  const { subtle } = resolveSealCrypto(env)
  let salt: Uint8Array
  let body: Uint8Array
  try {
    salt = base64ToBytes(payload.kdf.salt)
    body = base64ToBytes(payload.sealed)
  } catch {
    throw new ApiKeySealFormatError()
  }
  if (salt.length !== API_KEY_SEAL_SALT_BYTES) throw new ApiKeySealFormatError()
  if (body.length < API_KEY_SEAL_IV_BYTES + API_KEY_SEAL_TAG_BYTES) throw new ApiKeySealFormatError()
  const iv = body.slice(0, API_KEY_SEAL_IV_BYTES)
  const ciphertext = body.slice(API_KEY_SEAL_IV_BYTES)
  const key = await deriveAesKey(subtle, password, salt, ['decrypt'])
  try {
    const opened = await subtle.decrypt(
      { name: 'AES-GCM', iv: toArrayBuffer(iv), tagLength: 128 },
      key,
      toArrayBuffer(ciphertext),
    )
    return utf8Text(new Uint8Array(opened))
  } catch {
    throw new ApiKeySealDecryptError()
  }
}