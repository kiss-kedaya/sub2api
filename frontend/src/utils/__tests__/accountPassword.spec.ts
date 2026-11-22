import { describe, expect, it } from 'vitest'

import { accountLacksLoginPassword, hasSyntheticOAuthEmail } from '../accountPassword'

describe('passwordless OAuth account detection', () => {
  it.each([
    'user@linuxdo-connect.invalid',
    'user@oidc-connect.invalid',
    'user@wechat-connect.invalid',
    'user@dingtalk-connect.invalid',
  ])('treats synthetic provider sign-in email %s as passwordless', (email) => {
    expect(hasSyntheticOAuthEmail(email)).toBe(true)
    expect(accountLacksLoginPassword({ email })).toBe(true)
  })

  it('keeps normal email accounts eligible for password encryption', () => {
    expect(accountLacksLoginPassword({ email: 'user@example.com' })).toBe(false)
    expect(accountLacksLoginPassword({ email: '  USER@EXAMPLE.COM ' })).toBe(false)
  })

  it('defaults to eligible for an unknown profile instead of hiding the feature', () => {
    expect(accountLacksLoginPassword(null)).toBe(false)
    expect(accountLacksLoginPassword(undefined)).toBe(false)
    expect(accountLacksLoginPassword({ email: '' })).toBe(false)
  })
})