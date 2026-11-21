import { describe, expect, it } from 'vitest'

import { SignerCryptoUnavailableError } from '../cryptoProvider'
import { QrCameraError } from '../qr'
import { ShareFormatError } from '../shardFormat'
import { isRateLimitedError, isShareRequiredError, signerErrorI18nKey, signerErrorKey } from '../errors'

describe('signer error mapping', () => {
  it('maps every stable backend error code', () => {
    const cases: Array<[string, string]> = [
      ['CRYPTO_SIGNER_SHARE_REQUIRED', 'shareRequired'],
      ['CRYPTO_SIGNER_SHARE_INVALID', 'shareInvalid'],
      ['CRYPTO_SIGNER_RATE_LIMITED', 'rateLimited'],
      ['CRYPTO_SIGNER_SHARDS_NOT_CONFIGURED', 'notConfigured'],
      ['CRYPTO_SIGNER_SHARDS_EXIST', 'alreadyConfigured'],
      ['CRYPTO_SIGNER_STATE_INVALID', 'stateInvalid'],
      ['CRYPTO_SIGNER_RISK_ACK_REQUIRED', 'riskAckRequired'],
      ['CRYPTO_SIGNER_TOTP_REQUIRED', 'totpRequired'],
      ['CRYPTO_HTTPS_REQUIRED', 'httpsRequired'],
      ['CRYPTO_TEST_MODE_READ_ONLY', 'readOnly']
    ]
    for (const [reason, key] of cases) {
      expect(signerErrorKey({ reason })).toBe(key)
      expect(signerErrorI18nKey({ reason })).toBe(`admin.crypto.signer.errors.${key}`)
    }
  })

  it('detects the share-required and rate-limited conditions used for UX routing', () => {
    expect(isShareRequiredError({ reason: 'CRYPTO_SIGNER_SHARE_REQUIRED' })).toBe(true)
    expect(isShareRequiredError({ reason: 'CRYPTO_SIGNER_SHARE_INVALID' })).toBe(false)
    expect(isRateLimitedError({ reason: 'CRYPTO_SIGNER_RATE_LIMITED' })).toBe(true)
    expect(isRateLimitedError({ reason: 'CRYPTO_SIGNER_SHARE_INVALID' })).toBe(false)
  })

  it('maps share parse errors, including an invalid character', () => {
    expect(signerErrorKey(new ShareFormatError('checksum'))).toBe('shareChecksum')
    expect(signerErrorKey(new ShareFormatError('format'))).toBe('shareFormat')
    expect(signerErrorKey(new ShareFormatError('format', 'invalid-character:＃'))).toBe('shareInvalidCharacter')
    expect(signerErrorKey(new ShareFormatError('threshold'))).toBe('shareThreshold')
    expect(signerErrorKey(new ShareFormatError('duplicate'))).toBe('shareDuplicate')
    expect(signerErrorKey(new ShareFormatError('version'))).toBe('shareVersion')
    expect(signerErrorKey(new ShareFormatError('parameters'))).toBe('shareParameters')
  })

  it('maps crypto-environment and camera failures', () => {
    expect(signerErrorKey(new SignerCryptoUnavailableError('insecure-context'))).toBe('insecureContext')
    expect(signerErrorKey(new SignerCryptoUnavailableError('no-csprng'))).toBe('noCsprng')
    expect(signerErrorKey(new SignerCryptoUnavailableError('no-aes-gcm'))).toBe('cryptoUnavailable')
    expect(signerErrorKey(new QrCameraError('denied'))).toBe('cameraDenied')
    expect(signerErrorKey(new QrCameraError('insecure'))).toBe('cameraInsecure')
    expect(signerErrorKey(new QrCameraError('unsupported'))).toBe('cameraUnsupported')
    expect(signerErrorKey(new QrCameraError('unavailable'))).toBe('cameraUnavailable')
  })

  it('returns undefined for unrelated failures so callers can show a generic message', () => {
    expect(signerErrorKey(new Error('boom'))).toBeUndefined()
    expect(signerErrorI18nKey({ reason: 'SOMETHING_ELSE' })).toBeUndefined()
  })
})
