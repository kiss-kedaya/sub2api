import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('usage service tier locale keys', () => {
  it('contains zh labels for service tier tooltip', () => {
    expect(zh.usage.serviceTier).toBe('服务档位')
    expect(zh.usage.serviceTierPriority).toBe('Fast')
    expect(zh.usage.serviceTierUltrafast).toBe('Ultrafast')
    expect(zh.usage.serviceTierFlex).toBe('Flex')
    expect(zh.usage.serviceTierStandard).toBe('Standard')
  })

  it('contains zh labels for the tier multiplier rows', () => {
    expect(zh.usage.serviceTierMultiplier).toBe('档位倍率')
    expect(zh.usage.groupRate).toBe('分组倍率')
    expect(zh.usage.combinedRate).toBe('综合倍率')
    // 没有档位倍率的老数据仍显示原来的「倍率」
    expect(zh.usage.rate).toBe('倍率')
  })

  it('contains en labels for the tier multiplier rows', () => {
    expect(en.usage.serviceTierMultiplier).toBe('Tier multiplier')
    expect(en.usage.groupRate).toBe('Group rate')
    expect(en.usage.combinedRate).toBe('Combined rate')
    expect(en.usage.rate).toBe('Rate')
  })

  it('contains en labels for service tier tooltip', () => {
    expect(en.usage.serviceTier).toBe('Service tier')
    expect(en.usage.serviceTierPriority).toBe('Fast')
    expect(en.usage.serviceTierUltrafast).toBe('Ultrafast')
    expect(en.usage.serviceTierFlex).toBe('Flex')
    expect(en.usage.serviceTierStandard).toBe('Standard')
  })
})
