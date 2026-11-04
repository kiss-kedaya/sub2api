import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoChainStatusPanel from '../CryptoChainStatusPanel.vue'
import { chain, report } from './chainStatusFixtures'
import type { ChainStatusReport } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ getChainStatus: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getChainStatus: mocks.getChainStatus }))
vi.mock('vue-i18n', async () => (await import('./chainStatusFixtures')).i18nMock)

enableAutoUnmount(afterEach)
beforeEach(() => mocks.getChainStatus.mockReset())

async function row(data: ChainStatusReport, name = 'bsc') {
  mocks.getChainStatus.mockResolvedValue(data)
  const wrapper = mount(CryptoChainStatusPanel)
  await flushPromises()
  return wrapper.get(`[data-testid="chain-row-${name}"]`)
}
const stat = (r: Awaited<ReturnType<typeof row>>, key: string) => r.find(`[data-stat="${key}"]`)

describe('chain row: progress, speed and ETA', () => {
  it('synced is a full green bar with the scan position and speeds', async () => {
    const r = await row(report())
    expect(r.get('h3').text()).toBe('BSC')
    expect(r.attributes('data-state')).toBe('synced')
    expect(r.get('[data-testid="chain-state"]').text()).toBe('已同步')
    const bar = r.get('[data-testid="chain-bar"]')
    expect(bar.attributes('role')).toBe('progressbar')
    expect(bar.attributes('data-state')).toBe('synced')
    expect(bar.attributes('aria-valuenow')).toBe('100')
    expect(r.get('[data-testid="chain-bar-fill"]').attributes('style')).toContain('width: 100%')
    expect(r.get('[data-testid="chain-pct"]').text()).toBe('100%')
    expect(stat(r, 'scanned').text()).toBe('已扫到 39,999,990 / 40,000,000')
    expect(stat(r, 'remaining').text()).toBe('还有 7 块没扫')
    expect(stat(r, 'scanSpeed').text()).toBe('扫描速度 4,000 块/分')
    expect(stat(r, 'headSpeed').text()).toBe('链增长速度 80 块/分')
    expect(stat(r, 'pending').text()).toBe('待入账 0 笔')
    expect(stat(r, 'eta').exists()).toBe(false)
    expect(r.get('[data-testid="chain-cursor-age"]').text()).toBe('游标 5 秒前更新')
  })

  it('catching up shows the blue flowing bar, the percentage and the ETA', async () => {
    const r = await row(report([chain({ state: 'catching_up', progress_pct: 78.4, eta_seconds: 1800, unscanned: 2160 })]))
    expect(r.get('[data-testid="chain-state"]').text()).toBe('追赶中')
    expect(r.get('[data-testid="chain-bar"]').attributes('data-state')).toBe('catching_up')
    expect(r.get('[data-testid="chain-bar-fill"]').attributes('style')).toContain('width: 78.4%')
    expect(r.get('[data-testid="chain-bar"]').attributes('aria-valuenow')).toBe('78.4')
    expect(r.get('[data-testid="chain-pct"]').text()).toBe('78.4%')
    expect(stat(r, 'remaining').text()).toBe('还有 2,160 块没扫')
    expect(stat(r, 'eta').text()).toBe('预计 30 分钟 追平')
  })

  it.each([
    [34, '不到 1 分钟'], [90, '2 分钟'], [3600, '1 小时'], [3 * 3600, '3 小时'], [50 * 3600, '2 天']
  ])('formats an ETA of %i seconds', async (seconds, expected) => {
    const r = await row(report([chain({ state: 'catching_up', progress_pct: 10, eta_seconds: seconds })]))
    expect(stat(r, 'eta').text()).toBe(`预计 ${expected} 追平`)
  })

  it('right after a restart shows 0% and calculating, never an invented figure', async () => {
    const r = await row(report([chain({
      state: 'catching_up', progress_pct: 0, eta_seconds: null, scan_speed_bpm: null, head_speed_bpm: null, unscanned: 7000
    })]))
    expect(r.get('[data-testid="chain-pct"]').text()).toBe('0%')
    expect(stat(r, 'eta').text()).toBe('预计追平：计算中')
    expect(stat(r, 'scanSpeed').text()).toBe('扫描速度 计算中')
    expect(stat(r, 'headSpeed').text()).toBe('链增长速度 计算中')
  })

  it('an unknown percentage reads calculating and leaves the bar empty', async () => {
    const r = await row(report([chain({ state: 'catching_up', progress_pct: null, eta_seconds: null })]))
    expect(r.get('[data-testid="chain-pct"]').text()).toBe('计算中')
    expect(r.get('[data-testid="chain-bar-fill"]').attributes('style')).toContain('width: 0%')
    expect(r.get('[data-testid="chain-bar"]').attributes('aria-valuenow')).toBeUndefined()
  })

  it('a scanner that cannot outrun the chain says so instead of a time', async () => {
    const r = await row(report([chain({
      state: 'catching_up', progress_pct: 5, eta_seconds: null, scan_speed_bpm: 100, head_speed_bpm: 100
    })]))
    expect(stat(r, 'eta').text()).toBe('暂时追不上')
  })

  it('stalled is the orange bar, shows how long the cursor has been idle and flags pending deposits', async () => {
    const r = await row(report([chain({
      state: 'stalled', progress_pct: 0, eta_seconds: null, unscanned: 999_997, stalled_seconds: 14 * 3600,
      pending_deposits: 17, scan_speed_bpm: 0
    })]))
    expect(r.get('[data-testid="chain-state"]').text()).toBe('停滞')
    expect(r.get('[data-testid="chain-bar"]').attributes('data-state')).toBe('stalled')
    expect(r.get('[data-testid="chain-cursor-age"]').text()).toBe('游标 14 小时前更新')
    expect(r.get('[data-testid="chain-cursor-age"]').classes()).toContain('text-warning')
    expect(stat(r, 'eta').exists()).toBe(false)
    expect(stat(r, 'scanSpeed').text()).toBe('扫描速度 0 块/分')
    expect(stat(r, 'pending').text()).toBe('待入账 17 笔')
    expect(stat(r, 'pending').classes()).toContain('text-warning')
  })

  it('formats how long ago the cursor moved', async () => {
    for (const [seconds, text] of [[59, '游标 59 秒前更新'], [60, '游标 1 分钟前更新'], [3599, '游标 59 分钟前更新'],
      [3600, '游标 1 小时前更新'], [86400 * 3, '游标 3 天前更新']] as const) {
      const r = await row(report([chain({ stalled_seconds: seconds })]))
      expect(r.get('[data-testid="chain-cursor-age"]').text()).toBe(text)
    }
    const none = await row(report([chain({ stalled_seconds: null, cursor: null, cursor_updated_at: null })]))
    expect(none.get('[data-testid="chain-cursor-age"]').text()).toBe('尚无游标记录')
  })
})
