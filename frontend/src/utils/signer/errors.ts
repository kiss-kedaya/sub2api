/**
 * Map signer failures to i18n keys under `admin.crypto.signer.errors.*`.
 * Kept away from components so the same wording is used everywhere.
 */

import { extractApiErrorCode } from '@/utils/apiError'
import { SignerCryptoUnavailableError } from './cryptoProvider'
import { ShareFormatError } from './shardFormat'
import { QrCameraError } from './qr'

const API_CODES: Record<string, string> = {
  CRYPTO_SIGNER_SHARE_REQUIRED: 'shareRequired',
  CRYPTO_SIGNER_SHARE_INVALID: 'shareInvalid',
  CRYPTO_SIGNER_RATE_LIMITED: 'rateLimited',
  CRYPTO_SIGNER_SHARDS_NOT_CONFIGURED: 'notConfigured',
  CRYPTO_SIGNER_SHARDS_EXIST: 'alreadyConfigured',
  CRYPTO_SIGNER_STATE_INVALID: 'stateInvalid',
  CRYPTO_SIGNER_RISK_ACK_REQUIRED: 'riskAckRequired',
  CRYPTO_SIGNER_TOTP_REQUIRED: 'totpRequired',
  CRYPTO_HTTPS_REQUIRED: 'httpsRequired',
  CRYPTO_DISABLED: 'cryptoDisabled',
  CRYPTO_TEST_MODE_READ_ONLY: 'readOnly',
  CRYPTO_GRANT_REQUIRED: 'totpRequired',
  CRYPTO_TOTP_REQUIRED: 'totpRequired',
  CRYPTO_TOTP_INVALID: 'totpInvalid',
  CRYPTO_RATE_LIMITED: 'totpRateLimited'
}

const SHARE_ERRORS: Record<string, string> = {
  format: 'shareFormat',
  version: 'shareVersion',
  checksum: 'shareChecksum',
  parameters: 'shareParameters',
  threshold: 'shareThreshold',
  duplicate: 'shareDuplicate'
}

const CRYPTO_ERRORS: Record<string, string> = {
  'insecure-context': 'insecureContext',
  'no-csprng': 'noCsprng',
  'no-aes-gcm': 'cryptoUnavailable'
}

const CAMERA_ERRORS: Record<string, string> = {
  insecure: 'cameraInsecure',
  unsupported: 'cameraUnsupported',
  denied: 'cameraDenied',
  unavailable: 'cameraUnavailable'
}

export function signerErrorCode(error: unknown): string | undefined {
  const code = extractApiErrorCode(error)
  return code ?? undefined
}

/** True when the backend wants a recovery share before the operation runs. */
export function isShareRequiredError(error: unknown): boolean {
  return signerErrorCode(error) === 'CRYPTO_SIGNER_SHARE_REQUIRED'
}

export function isRateLimitedError(error: unknown): boolean {
  const code = signerErrorCode(error)
  return code === 'CRYPTO_SIGNER_RATE_LIMITED' || code === 'CRYPTO_RATE_LIMITED'
}

/** Returns a key suffix (without the `admin.crypto.signer.errors.` prefix). */
export function signerErrorKey(error: unknown): string | undefined {
  if (error instanceof ShareFormatError) {
    if (error.code === 'format' && error.detail?.startsWith('invalid-character')) return 'shareInvalidCharacter'
    return SHARE_ERRORS[error.code]
  }
  if (error instanceof SignerCryptoUnavailableError) return CRYPTO_ERRORS[error.code]
  if (error instanceof QrCameraError) return CAMERA_ERRORS[error.code]
  const code = extractApiErrorCode(error)
  if (code && API_CODES[code]) return API_CODES[code]
  return undefined
}

/** Full i18n key for a signer failure, or undefined when unmapped. */
export function signerErrorI18nKey(error: unknown): string | undefined {
  const key = signerErrorKey(error)
  return key ? `admin.crypto.signer.errors.${key}` : undefined
}
