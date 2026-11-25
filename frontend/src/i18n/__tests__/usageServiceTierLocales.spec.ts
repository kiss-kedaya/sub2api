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
    // 没有档位倍率的老数据仍显示原来的「倍率」和「原始」
    expect(zh.usage.rate).toBe('倍率')
    expect(zh.usage.original).toBe('原始')
    // 有档位倍率的行：「原始」显示标准价；管理端多一行上游成本（只用于对账）
    expect(zh.usage.originalStandard).toBe('原始（标准价）')
    expect(zh.usage.upstreamCost).toBe('上游成本')
    expect(zh.usage.upstreamCostHint).toContain('对账')
  })

  it('contains en labels for the tier multiplier rows', () => {
    expect(en.usage.serviceTierMultiplier).toBe('Tier multiplier')
    expect(en.usage.groupRate).toBe('Group rate')
    expect(en.usage.combinedRate).toBe('Combined rate')
    expect(en.usage.rate).toBe('Rate')
    expect(en.usage.original).toBe('Original')
    expect(en.usage.originalStandard).toBe('Original (Standard price)')
    expect(en.usage.upstreamCost).toBe('Upstream cost')
    expect(en.usage.upstreamCostHint).toContain('reconciliation')
  })

  it('contains en labels for service tier tooltip', () => {
    expect(en.usage.serviceTier).toBe('Service tier')
    expect(en.usage.serviceTierPriority).toBe('Fast')
    expect(en.usage.serviceTierUltrafast).toBe('Ultrafast')
    expect(en.usage.serviceTierFlex).toBe('Flex')
    expect(en.usage.serviceTierStandard).toBe('Standard')
  })
})
