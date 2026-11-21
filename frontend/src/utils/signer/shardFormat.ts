/**
 * S2V1 share wire format + GF(256) 2-of-3 Shamir split/combine.
 *
 * Wire format (must stay byte-identical to the Go server implementation in
 * `backend/internal/cryptopay/wallet/shards`):
 *
 *   S2V1-XXXX-XXXX-...   RFC 4648 base32 (no padding), grouped in fours
 *   payload = version(1) | threshold(1) | total(1) | index(1) | dataLen(1) | data(n) | sha256(payload-before-checksum)[0..4]
 *
 * The server only ever sees share A; the browser keeps B and C. Any two shares
 * reconstruct the 32-byte wallet data key (DK); a single share is useless.
 */

import { dataKeyCommitment, type SignerCrypto } from './cryptoProvider'
import { base64ToBytes, bytesToBase64, equalBytes } from './encoding'
import { gfDiv, gfEvaluate, gfMul } from './gf256'

export const SHARE_PREFIX = 'S2V1'
export const SHARD_SCHEME = 'shamir-gf256'
export const SHARD_VERSION = 1
export const SHARD_THRESHOLD = 2
export const SHARD_TOTAL = 3
export const SHARD_DATA_LENGTH = 32
export const SHARE_CHECKSUM_BYTES = 4
export const SHARE_HEADER_BYTES = 5
export const MAX_SHARE_DATA = 255

const BASE32_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
const BASE32_LOOKUP = new Map<string, number>(Array.from(BASE32_ALPHABET, (char, index) => [char, index]))

export type ShareErrorCode = 'format' | 'version' | 'checksum' | 'parameters' | 'threshold' | 'duplicate'

export class ShareFormatError extends Error {
  readonly code: ShareErrorCode
  readonly detail?: string

  constructor(code: ShareErrorCode, detail?: string) {
    super(detail ? `share ${code}: ${detail}` : `share ${code}`)
    this.name = 'ShareFormatError'
    this.code = code
    this.detail = detail
  }
}

export interface ShardShare {
  version: number
  threshold: number
  total: number
  index: number
  data: Uint8Array
}

export interface SplitOptions {
  threshold?: number
  total?: number
  /** Deterministic randomness hook; defaults to the CSPRNG. */
  random?: (length: number) => Uint8Array
}

function base32Encode(bytes: Uint8Array): string {
  let out = ''
  let buffer = 0
  let bits = 0
  for (const byte of bytes) {
    buffer = (buffer << 8) | byte
    bits += 8
    while (bits >= 5) {
      bits -= 5
      out += BASE32_ALPHABET[(buffer >> bits) & 0x1f]
    }
    buffer &= (1 << bits) - 1
  }
  if (bits > 0) out += BASE32_ALPHABET[(buffer << (5 - bits)) & 0x1f]
  return out
}

function base32Decode(text: string): Uint8Array {
  if (!text) throw new ShareFormatError('format', 'empty')
  // RFC 4648: lengths 1, 3 and 6 mod 8 cannot encode whole bytes.
  const remainder = text.length % 8
  if (remainder === 1 || remainder === 3 || remainder === 6) throw new ShareFormatError('format', 'length')
  const out = new Uint8Array(Math.floor((text.length * 5) / 8))
  let buffer = 0
  let bits = 0
  let offset = 0
  for (const char of text) {
    const value = BASE32_LOOKUP.get(char)
    if (value === undefined) throw new ShareFormatError('format', `invalid-character:${char}`)
    buffer = (buffer << 5) | value
    bits += 5
    if (bits >= 8) {
      bits -= 8
      out[offset] = (buffer >> bits) & 0xff
      offset += 1
    }
    buffer &= (1 << bits) - 1
  }
  return out
}

const INVISIBLE = /[\u200B-\u200D\u2060\uFEFF\u00AD\u180E]/g
const DASHES = /[-\u2010-\u2015\u2212\uFE58\uFE63\uFF0D]/g
const WHITESPACE = /\s+/g

/**
 * Normalize operator input before parsing: full-width to half-width (NFKC),
 * drop invisible characters, whitespace and dashes, then uppercase.
 *
 * An invalid character is reported explicitly instead of being silently
 * dropped, so a typo never becomes an unrecoverable one-time share.
 */
export function normalizeShareText(raw: string): string {
  const normalized = raw
    .normalize('NFKC')
    .replace(INVISIBLE, '')
    .replace(WHITESPACE, '')
    .replace(DASHES, '')
    .toUpperCase()
  if (!normalized) throw new ShareFormatError('format', 'empty')
  const invalid = normalized.match(/[^A-Z0-9]/)
  if (invalid) throw new ShareFormatError('format', `invalid-character:${invalid[0]}`)
  return normalized
}

/** True when the text looks like it intends to be an S2V1 share. */
export function looksLikeShareText(raw: string): boolean {
  try {
    return normalizeShareText(raw).startsWith(SHARE_PREFIX)
  } catch {
    return false
  }
}

export async function parseShare(raw: string, crypto: SignerCrypto): Promise<ShardShare> {
  const normalized = normalizeShareText(raw)
  if (!normalized.startsWith(SHARE_PREFIX)) throw new ShareFormatError('format', 'prefix')
  const payload = base32Decode(normalized.slice(SHARE_PREFIX.length))
  if (payload.length < SHARE_HEADER_BYTES + 1 + SHARE_CHECKSUM_BYTES) throw new ShareFormatError('format', 'short')
  const bodyLength = payload.length - SHARE_CHECKSUM_BYTES
  const body = payload.slice(0, bodyLength)
  const checksum = payload.slice(bodyLength)
  const expected = (await crypto.sha256(body)).slice(0, SHARE_CHECKSUM_BYTES)
  if (!equalBytes(checksum, expected)) throw new ShareFormatError('checksum')
  if (body[0] !== SHARD_VERSION) throw new ShareFormatError('version')
  const threshold = body[1]
  const total = body[2]
  const index = body[3]
  const dataLength = body[4]
  const parametersValid = threshold >= 2 && total >= threshold && index >= 1 && index <= total &&
    dataLength >= 1 && bodyLength === SHARE_HEADER_BYTES + dataLength
  if (!parametersValid) throw new ShareFormatError('parameters')
  return {
    version: body[0],
    threshold,
    total,
    index,
    data: body.slice(SHARE_HEADER_BYTES)
  }
}

export async function formatShare(share: ShardShare, crypto: SignerCrypto): Promise<string> {
  const payload = new Uint8Array(SHARE_HEADER_BYTES + share.data.length + SHARE_CHECKSUM_BYTES)
  payload[0] = share.version
  payload[1] = share.threshold
  payload[2] = share.total
  payload[3] = share.index
  payload[4] = share.data.length
  payload.set(share.data, SHARE_HEADER_BYTES)
  const checksum = (await crypto.sha256(payload.slice(0, SHARE_HEADER_BYTES + share.data.length))).slice(0, SHARE_CHECKSUM_BYTES)
  payload.set(checksum, SHARE_HEADER_BYTES + share.data.length)
  const encoded = base32Encode(payload)
  const groups: string[] = []
  for (let i = 0; i < encoded.length; i += 4) groups.push(encoded.slice(i, i + 4))
  return `${SHARE_PREFIX}-${groups.join('-')}`
}

/**
 * Split a 32-byte data key into `total` shares with `threshold` points needed.
 * Consumption order matches Go: for each secret byte, `threshold - 1` random
 * coefficient bytes, in field order.
 */
export async function splitSecret(
  secret: Uint8Array,
  options: SplitOptions,
  crypto: SignerCrypto
): Promise<ShardShare[]> {
  const threshold = options.threshold ?? SHARD_THRESHOLD
  const total = options.total ?? SHARD_TOTAL
  if (secret.length < 1 || secret.length > MAX_SHARE_DATA) throw new ShareFormatError('parameters')
  if (threshold < 2 || total < threshold || total > 255 || threshold > 255) throw new ShareFormatError('parameters')
  const random = options.random ?? ((length: number) => crypto.randomBytes(length))
  const shares: ShardShare[] = Array.from({ length: total }, (_, offset) => ({
    version: SHARD_VERSION,
    threshold,
    total,
    index: offset + 1,
    data: new Uint8Array(secret.length)
  }))
  const coefficients = new Uint8Array(threshold)
  for (let byteIndex = 0; byteIndex < secret.length; byteIndex += 1) {
    coefficients[0] = secret[byteIndex]
    if (threshold > 1) coefficients.set(random(threshold - 1), 1)
    for (const share of shares) share.data[byteIndex] = gfEvaluate(coefficients, share.index)
  }
  coefficients.fill(0)
  return shares
}

/**
 * Lagrange interpolation at x=0 over any threshold shares. Order independent;
 * duplicate indexes and shares from different splits are rejected.
 */
export function combineShares(shares: ShardShare[]): Uint8Array {
  if (shares.length < SHARD_THRESHOLD) throw new ShareFormatError('threshold')
  const { version, threshold, total } = shares[0]
  const length = shares[0].data.length
  if (version !== SHARD_VERSION || threshold < 2 || total < threshold || length === 0) throw new ShareFormatError('parameters')
  const seen = new Set<number>()
  for (const share of shares) {
    if (share.version !== version || share.threshold !== threshold || share.total !== total || share.data.length !== length) {
      throw new ShareFormatError('parameters')
    }
    if (share.index < 1 || share.index > total) throw new ShareFormatError('parameters')
    if (seen.has(share.index)) throw new ShareFormatError('duplicate')
    seen.add(share.index)
  }
  const ordered = [...shares].sort((a, b) => a.index - b.index)
  const secret = new Uint8Array(length)
  for (let byteIndex = 0; byteIndex < length; byteIndex += 1) {
    let value = 0
    for (let i = 0; i < ordered.length; i += 1) {
      let basis = 1
      for (let j = 0; j < ordered.length; j += 1) {
        if (i === j) continue
        const numerator = ordered[j].index
        const denominator = ordered[j].index ^ ordered[i].index
        if (denominator === 0) throw new ShareFormatError('duplicate')
        basis = gfMul(basis, gfDiv(numerator, denominator))
      }
      value ^= gfMul(ordered[i].data[byteIndex], basis)
    }
    secret[byteIndex] = value
  }
  return secret
}

export async function parseAndCombine(texts: string[], crypto: SignerCrypto): Promise<Uint8Array> {
  if (texts.length < SHARD_THRESHOLD) throw new ShareFormatError('threshold')
  const shares: ShardShare[] = []
  for (const text of texts) shares.push(await parseShare(text, crypto))
  return combineShares(shares)
}

/** Recompute the public commitment for a data key (base64 for the API). */
export async function commitmentBase64(crypto: SignerCrypto, dataKey: Uint8Array): Promise<string> {
  const commitment = await dataKeyCommitment(crypto, dataKey)
  return bytesToBase64(commitment)
}

export async function verifySharesOpenWallet(
  texts: string[],
  commitment: string,
  crypto: SignerCrypto
): Promise<{ ok: true; dataKey: Uint8Array } | { ok: false; reason: 'shares' | 'commitment' }> {
  let dataKey: Uint8Array
  try {
    dataKey = await parseAndCombine(texts, crypto)
  } catch {
    return { ok: false, reason: 'shares' }
  }
  const expected = await dataKeyCommitment(crypto, dataKey)
  let published: Uint8Array
  try {
    published = base64ToBytes(commitment)
  } catch {
    return { ok: false, reason: 'commitment' }
  }
  if (!equalBytes(expected, published)) return { ok: false, reason: 'commitment' }
  return { ok: true, dataKey }
}
