import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

// 个人资料页「创建 API Key 时需要验证身份」开关的文案：中英两份都要有、键一致、不能是空串。
// （localeKeyCompleteness.spec.ts 会扫源码里静态的 t('...') 调用；这里再把整块文案钉住，
// 防止只改一边、或者把默认值说明改丢。）

const EXPECTED_KEYS = [
  'title',
  'description',
  'switchLabel',
  'switchHint',
  'stateOn',
  'stateOff',
  'unavailable',
  'loadFailed',
  'verifyTitle',
  'verifyHint',
  'confirmOff',
  'savedOn',
  'savedOff',
  'conflict',
  'saveFailed',
].sort()

describe('profile.keyVerification locale block', () => {
  it('has exactly the same keys in zh and en, all non-empty', () => {
    const zhBlock = zh.profile.keyVerification as Record<string, unknown>
    const enBlock = en.profile.keyVerification as Record<string, unknown>
    expect(Object.keys(zhBlock).sort()).toEqual(EXPECTED_KEYS)
    expect(Object.keys(enBlock).sort()).toEqual(EXPECTED_KEYS)
    for (const key of EXPECTED_KEYS) {
      expect(typeof zhBlock[key]).toBe('string')
      expect((zhBlock[key] as string).trim()).not.toBe('')
      expect(typeof enBlock[key]).toBe('string')
      expect((enBlock[key] as string).trim()).not.toBe('')
    }
  })

  it('states the default and the consequence of turning it off', () => {
    // 默认：开了两步验证 / 通行密钥的账号为开；仅有密码的账号为关。
    expect(zh.profile.keyVerification.switchHint).toContain('两步验证')
    expect(zh.profile.keyVerification.switchHint).toContain('通行密钥')
    expect(zh.profile.keyVerification.switchHint).toContain('登录密码')
    expect(en.profile.keyVerification.switchHint).toMatch(/two-factor/i)
    expect(en.profile.keyVerification.switchHint).toMatch(/passkey/i)
    expect(en.profile.keyVerification.switchHint).toMatch(/login password/i)
    // 关掉之后任何拿到登录会话的人都能直接建 Key，要明说。
    expect(zh.profile.keyVerification.stateOff).toContain('登录会话')
    expect(en.profile.keyVerification.stateOff).toMatch(/signed-in session/i)
    // 置灰时要说清原因：没有任何可用的验证方式。
    expect(zh.profile.keyVerification.unavailable).toContain('验证方式')
    expect(en.profile.keyVerification.unavailable).toMatch(/no way to verify/i)
  })
})
