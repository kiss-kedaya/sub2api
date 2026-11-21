/**
 * High-level signer material: generate the browser-held data key, split it,
 * build the server payload (which only ever contains share A), and recover the
 * key from operator-held shares for verification.
 */

import { dataKeyCommitment, type SignerCrypto } from './cryptoProvider'
import { bytesToBase64, zeroBytes } from './encoding'
import {
  commitmentBase64,
  formatShare,
  parseAndCombine,
  parseShare,
  splitSecret,
  verifySharesOpenWallet,
  type ShardShare
} from './shardFormat'
import { VAULT_DATA_KEY_BYTES } from './walletVaultV3'

export type ShardSlot = 'A' | 'B' | 'C'

export interface SignerShardSet {
  /** 32-byte AES-256 key; never sent except once during setup. */
  dataKey: Uint8Array
  shares: ShardShare[]
  /** Canonical S2V1 strings, index order 1..3 (A, B, C). */
  shareTexts: string[]
  /** base64 SHA-256 commitment published to the server. */
  commitment: string
}

export interface GenerateOptions {
  /** Deterministic test hook. */
  dataKey?: Uint8Array
  random?: (length: number) => Uint8Array
}

export async function generateSignerShardSet(crypto: SignerCrypto, options: GenerateOptions = {}): Promise<SignerShardSet> {
  const dataKey = options.dataKey ? options.dataKey.slice() : crypto.randomBytes(VAULT_DATA_KEY_BYTES)
  if (dataKey.length !== VAULT_DATA_KEY_BYTES) throw new Error('data key must be 32 bytes')
  const shares = await splitSecret(dataKey, { random: options.random }, crypto)
  const shareTexts: string[] = []
  for (const share of shares) shareTexts.push(await formatShare(share, crypto))
  const commitment = await commitmentBase64(crypto, dataKey)
  return { dataKey, shares, shareTexts, commitment }
}

export function shareSlot(index: number): ShardSlot | null {
  return index === 1 ? 'A' : index === 2 ? 'B' : index === 3 ? 'C' : null
}

export function shareTextForSlot(set: SignerShardSet, slot: ShardSlot): string {
  const share = set.shares.find(candidate => shareSlot(candidate.index) === slot)
  const text = share ? set.shareTexts[share.index - 1] : ''
  if (!text) throw new Error(`share ${slot} is not part of this set`)
  return text
}

export interface SignerSetupPayload {
  totp_code: string
  risk_phrase: string
  data_key: string
  share_a: string
  confirm_share: string
  dk_commitment: string
}

/**
 * Build the POST body for `/admin/crypto/signer/setup` and `/resplit`.
 * Only share A, the one-shot data key and the public commitment leave the
 * browser; B and C exist only as `confirm_share` after the operator typed one
 * of them back.
 */
export async function buildSignerSetupPayload(
  crypto: SignerCrypto,
  input: {
    dataKey: Uint8Array
    shareA: string
    confirmShare: string
    commitment: string
    totpCode: string
    riskPhrase: string
  }
): Promise<SignerSetupPayload> {
  if (input.dataKey.length !== VAULT_DATA_KEY_BYTES) throw new Error('data key must be 32 bytes')
  for (const text of [input.shareA, input.confirmShare]) await parseShare(text, crypto)
  const expected = await commitmentBase64(crypto, input.dataKey)
  if (expected !== input.commitment) throw new Error('commitment does not match the data key')
  return {
    totp_code: input.totpCode,
    risk_phrase: input.riskPhrase,
    data_key: bytesToBase64(input.dataKey),
    share_a: input.shareA.trim(),
    confirm_share: input.confirmShare.trim(),
    dk_commitment: input.commitment
  }
}

/** Recover the data key from any two operator shares (B+C offline recovery). */
export async function recoverDataKeyFromShares(
  crypto: SignerCrypto,
  shareTexts: string[],
  commitment?: string
): Promise<Uint8Array> {
  if (commitment) {
    const result = await verifySharesOpenWallet(shareTexts, commitment, crypto)
    if (!result.ok) throw new Error(`shares do not open this wallet: ${result.reason}`)
    return result.dataKey
  }
  return parseAndCombine(shareTexts, crypto)
}

/** Local check that a typed-back share is exactly one of the shown shares. */
export async function matchesShownShare(
  crypto: SignerCrypto,
  typed: string,
  shown: string[]
): Promise<number | null> {
  let parsed: ShardShare
  try {
    parsed = await parseShare(typed, crypto)
  } catch {
    return null
  }
  for (let i = 0; i < shown.length; i += 1) {
    try {
      const candidate = await parseShare(shown[i], crypto)
      if (candidate.index === parsed.index && candidate.version === parsed.version &&
        candidate.threshold === parsed.threshold && candidate.total === parsed.total &&
        candidate.data.length === parsed.data.length &&
        candidate.data.every((byte, offset) => byte === parsed.data[offset])) {
        return i
      }
    } catch {
      /* ignore unparsable shown share */
    }
  }
  return null
}

/** Best-effort scrub of the in-memory set once the wizard completed. */
export function wipeSignerShardSet(set: SignerShardSet | null): void {
  if (!set) return
  zeroBytes(set.dataKey, ...set.shares.map(share => share.data))
  set.shareTexts.fill('')
}

export async function commitmentForDataKey(crypto: SignerCrypto, dataKey: Uint8Array): Promise<string> {
  return bytesToBase64(await dataKeyCommitment(crypto, dataKey))
}
