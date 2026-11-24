import { describe, expect, it } from 'vitest'
import type { ApiKeyVerificationFactors } from '@/types'
import { needsKeyVerification } from '../apiKeyVerification'

const base: ApiKeyVerificationFactors = {
  password_available: false,
  totp_enabled: false,
  passkey_count: 0,
  preferred: '',
  available_methods: [],
  seal_available: false,
}

describe('needsKeyVerification', () => {
  it('is false while the factors are unknown', () => {
    expect(needsKeyVerification(null)).toBe(false)
    expect(needsKeyVerification(undefined)).toBe(false)
  })

  it('follows the server: a login password alone does not force a verification step', () => {
    const passwordOnly: ApiKeyVerificationFactors = {
      ...base,
      password_available: true,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
      create_verification_required: false,
    }
    expect(needsKeyVerification(passwordOnly)).toBe(false)
  })

  it('follows the server: an account with TOTP or a passkey must verify', () => {
    expect(
      needsKeyVerification({
        ...base,
        totp_enabled: true,
        preferred: 'totp',
        available_methods: ['totp', 'password'],
        create_verification_required: true,
      }),
    ).toBe(true)
    expect(
      needsKeyVerification({
        ...base,
        passkey_count: 1,
        preferred: 'passkey',
        available_methods: ['passkey'],
        create_verification_required: true,
      }),
    ).toBe(true)
  })

  it('the server flag wins over the list of methods, in both directions', () => {
    expect(needsKeyVerification({ ...base, available_methods: ['password'], create_verification_required: false })).toBe(false)
    expect(needsKeyVerification({ ...base, available_methods: [], create_verification_required: true })).toBe(true)
  })

  it('an older server without the flag keeps its old rule: any available factor forces it', () => {
    expect(needsKeyVerification({ ...base, available_methods: ['password'] })).toBe(true)
    expect(needsKeyVerification({ ...base, available_methods: ['totp', 'password'] })).toBe(true)
    expect(needsKeyVerification({ ...base, available_methods: [] })).toBe(false)
  })
})
