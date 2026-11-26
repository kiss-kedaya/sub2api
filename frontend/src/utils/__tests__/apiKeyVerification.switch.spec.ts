import { describe, expect, it } from 'vitest'
import type { ApiKeyVerificationFactors } from '@/types'
import { needsKeyVerification } from '../apiKeyVerification'

// 用户自己改过「创建 API Key 时需要验证身份」之后，创建 / 轮换流程只看服务端算好的有效值：
// 2FA 账号设成 off 不再弹校验（也就不会发 verification 字段），仅有密码的账号设成 on 就要弹。
const factors = (overrides: Partial<ApiKeyVerificationFactors>): ApiKeyVerificationFactors => ({
  password_available: true,
  totp_enabled: false,
  passkey_count: 0,
  preferred: 'password',
  available_methods: ['password'],
  seal_available: true,
  create_verification_configurable: true,
  ...overrides,
})

describe('needsKeyVerification follows the user switch', () => {
  it('an account with 2FA that turned the check off is not asked', () => {
    const twoFactorOff = factors({
      totp_enabled: true,
      preferred: 'totp',
      available_methods: ['totp', 'password'],
      create_verification_required: false,
      create_verification_mode: 'off',
    })
    expect(needsKeyVerification(twoFactorOff)).toBe(false)
  })

  it('a password-only account that turned the check on is asked', () => {
    expect(needsKeyVerification(factors({ create_verification_required: true, create_verification_mode: 'on' }))).toBe(true)
  })

  it('an account with nothing to verify with is never asked, whatever it stored', () => {
    expect(
      needsKeyVerification(
        factors({
          password_available: false,
          preferred: '',
          available_methods: [],
          seal_available: false,
          create_verification_required: false,
          create_verification_mode: 'on',
          create_verification_configurable: false,
        }),
      ),
    ).toBe(false)
  })

  it('an older server (no switch fields) keeps the old fallback: any available factor is asked', () => {
    expect(needsKeyVerification(factors({ create_verification_configurable: undefined }))).toBe(true)
  })
})
