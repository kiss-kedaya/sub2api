import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('usage claude refusal request type locale keys', () => {
  it('labels the Claude policy refusal request type in zh and en', () => {
    expect(zh.usage.claudeRefusal).toBe('Claude违规拒答')
    expect(en.usage.claudeRefusal).toBe('Claude policy refusal')
  })
})
