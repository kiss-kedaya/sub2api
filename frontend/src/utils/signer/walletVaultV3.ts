/**
 * `sub2api-wallet:v3:` envelope, byte-compatible with the Go server.
 *
 * Go uses `cipher.NewGCMWithRandomNonce`, which renders the wire value as
 *
 *   "sub2api-wallet:v3:" || base64(nonce[12] || AES-256-GCM(plaintext, AAD=prefix))
 *
 * so the nonce is prefixed and the whole prefix is authenticated as AAD. A v2
 * ciphertext therefore cannot be replayed as v3. The browser needs this codec
 * for offline recovery verification and for the documented strict rotation
 * path, and its output must decrypt on the server unchanged.
 */

import { base64ToBytes, bytesToBase64, equalBytes, utf8Bytes, utf8Text } from './encoding'
import type { SignerCrypto } from './cryptoProvider'

export const VAULT_V3_PREFIX = 'sub2api-wallet:v3:'
export const VAULT_NONCE_BYTES = 12
export const VAULT_TAG_BYTES = 16
export const VAULT_DATA_KEY_BYTES = 32

export const VAULT_AAD = VAULT_V3_PREFIX
const VAULT_AAD_BYTES = utf8Bytes(VAULT_AAD)

export interface WalletVaultContents {
  deposit: string
  hot: string
  security_version?: number
  [key: string]: unknown
}

export function isWalletVaultV3(raw: string): boolean {
  return typeof raw === 'string' && raw.startsWith(VAULT_V3_PREFIX)
}

export async function sealWalletVaultV3(
  crypto: SignerCrypto,
  dataKey: Uint8Array,
  contents: WalletVaultContents | string,
  nonce?: Uint8Array
): Promise<string> {
  assertDataKey(dataKey)
  const plaintext = typeof contents === 'string' ? utf8Bytes(contents) : utf8Bytes(JSON.stringify(contents))
  const iv = nonce ? nonce.slice() : crypto.randomBytes(VAULT_NONCE_BYTES)
  if (iv.length !== VAULT_NONCE_BYTES) throw new Error('vault v3 nonce must be 12 bytes')
  const sealed = await crypto.aesGcmSeal(dataKey, iv, plaintext, VAULT_AAD_BYTES)
  const payload = new Uint8Array(iv.length + sealed.length)
  payload.set(iv, 0)
  payload.set(sealed, iv.length)
  plaintext.fill(0)
  return VAULT_V3_PREFIX + bytesToBase64(payload)
}

export async function openWalletVaultV3(
  crypto: SignerCrypto,
  dataKey: Uint8Array,
  raw: string
): Promise<WalletVaultContents> {
  assertDataKey(dataKey)
  if (!isWalletVaultV3(raw)) throw new Error('not a sub2api-wallet:v3 ciphertext')
  const payload = base64ToBytes(raw.slice(VAULT_V3_PREFIX.length))
  if (payload.length <= VAULT_NONCE_BYTES + VAULT_TAG_BYTES) throw new Error('vault v3 ciphertext is truncated')
  const nonce = payload.slice(0, VAULT_NONCE_BYTES)
  const sealed = payload.slice(VAULT_NONCE_BYTES)
  const plaintext = await crypto.aesGcmOpen(dataKey, nonce, sealed, VAULT_AAD_BYTES)
  try {
    const parsed = JSON.parse(utf8Text(plaintext)) as WalletVaultContents
    if (!parsed || typeof parsed.deposit !== 'string' || typeof parsed.hot !== 'string') {
      throw new Error('vault contents are missing deposit/hot')
    }
    return parsed
  } finally {
    plaintext.fill(0)
  }
}

/** Same check the server performs before it accepts a reconstructed key. */
export function isVaultDataKeyFormat(dataKey: Uint8Array): boolean {
  return dataKey.length === VAULT_DATA_KEY_BYTES
}

export function vaultNonceFromCiphertext(raw: string): Uint8Array | null {
  if (!isWalletVaultV3(raw)) return null
  try {
    const payload = base64ToBytes(raw.slice(VAULT_V3_PREFIX.length))
    return payload.slice(0, VAULT_NONCE_BYTES)
  } catch {
    return null
  }
}

export function vaultCiphertextEquals(a: string, b: string): boolean {
  if (!isWalletVaultV3(a) || !isWalletVaultV3(b)) return false
  try {
    return equalBytes(base64ToBytes(a.slice(VAULT_V3_PREFIX.length)), base64ToBytes(b.slice(VAULT_V3_PREFIX.length)))
  } catch {
    return false
  }
}

function assertDataKey(dataKey: Uint8Array): void {
  if (!isVaultDataKeyFormat(dataKey)) throw new Error('wallet data key must be 32 bytes')
}
