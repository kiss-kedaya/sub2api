import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoChainStatusPanel from '../CryptoChainStatusPanel.vue'
import { chain, deadChain, node, report } from './chainStatusFixtures'
import type { ChainStatusReport } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ getChainStatus: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getChainStatus: mocks.getChainStatus }))
vi.mock('vue-i18n', async () => (await import('./chainStatusFixtures')).i18nMock)

enableAutoUnmount(afterEach)
beforeEach(() => mocks.getChainStatus.mockReset())

async function render(data: ChainStatusReport) {
  mocks.getChainStatus.mockResolvedValue(data)
  const wrapper = mount(CryptoChainStatusPanel)
  await flushPromises()
  return wrapper
}
const rowOf = (w: Awaited<ReturnType<typeof render>>, name = 'bsc') => w.get(`[data-testid="chain-row-${name}"]`)

describe('chain row: failing and unconfigured chains', () => {
  it('error is a red bar with the reason, and the bad node is marked with its error category', async () => {
    const r = rowOf(await render(report([deadChain()])))
    expect(r.get('[data-testid="chain-state"]').text()).toBe('异常')
    expect(r.get('[data-testid="chain-bar"]').attributes('data-state')).toBe('error')
    expect(r.get('[data-testid="chain-bar"]').attributes('aria-valuenow')).toBeUndefined()
    expect(r.get('[data-testid="chain-pct"]').text()).toBe('—')
    expect(r.get('[data-testid="chain-reason"]').text()).toBe('所有节点探测失败 · 服务端错误 (5xx)')
    const n = r.get('[data-testid="node-ankr#0"]')
    expect(n.attributes('data-status')).toBe('error')
    expect(n.get('[data-testid="node-error"]').text()).toBe('服务端错误 (5xx)')
    expect(n.get('[data-testid="node-latency"]').text()).toBe('—')
    expect(n.get('.font-mono').classes()).toContain('text-error')
    expect(n.get('.rounded-full').classes()).toContain('bg-red-500')
    expect(r.find('[data-stat="scanSpeed"]').exists()).toBe(false)
    expect(r.find('[data-stat="remaining"]').exists()).toBe(false)
  })

  it('a cursor read failure is explained, and does not pretend the cursor is merely missing', async () => {
    const r = rowOf(await render(report([deadChain({
      head: 100, cursor: null, stalled_seconds: null, cursor_updated_at: null,
      last_error: { kind: 'db_error', stage: 'cursor' }, nodes: [node()]
    })])))
    expect(r.get('[data-testid="chain-reason"]').text()).toBe('无法读取扫描游标')
    expect(r.get('[data-testid="chain-cursor-age"]').text()).toBe('')
  })

  it('no RPC at all says so and lists no nodes', async () => {
    const r = rowOf(await render(report([chain({
      state: 'no_rpc', head: null, confirmed_head: null, cursor: null, unscanned: null, progress_pct: null,
      scan_speed_bpm: null, head_speed_bpm: null, eta_seconds: null, stalled_seconds: null, nodes: []
    })])))
    expect(r.get('[data-testid="chain-state"]').text()).toBe('无 RPC')
    expect(r.get('[data-testid="chain-reason"]').text()).toBe('没有可用的 RPC 节点')
    expect(r.findAll('li')).toHaveLength(0)
    expect(r.get('[data-testid="chain-bar"]').attributes('data-state')).toBe('no_rpc')
  })
})

describe('configured but not enabled (the BSC incident)', () => {
  const disabled = () => report([chain({
    enabled: false, state: 'disabled', progress_pct: 0, eta_seconds: null, unscanned: 999_997,
    stalled_seconds: 14 * 3600, pending_deposits: 17
  })])

  it('greys the row out and raises a conspicuous warning with a link to the settings tab', async () => {
    const w = await render(disabled())
    const r = rowOf(w)
    expect(r.attributes('data-state')).toBe('disabled')
    expect(r.get('[data-testid="chain-state"]').text()).toBe('未开启')
    const warning = r.get('[data-testid="chain-disabled-warning"]')
    expect(warning.text()).toContain('已配置 RPC 但未开启，不会被扫描')
    expect(warning.attributes('role')).toBe('alert')
    expect(r.findAll('.opacity-60')).toHaveLength(2)
    expect(warning.element.closest('.opacity-60')).toBeNull()
    expect(r.get('[data-testid="chain-bar"]').attributes('data-state')).toBe('disabled')
    expect(r.get('[data-stat="pending"]').text()).toBe('待入账 17 笔')
    expect(r.get('[data-stat="pending"]').classes()).toContain('text-warning')
    expect(r.get('[data-testid="chain-cursor-age"]').text()).toBe('游标 14 小时前更新')

    expect(w.emitted('open-config')).toBeUndefined()
    await r.get('[data-testid="chain-open-config"]').trigger('click')
    expect(w.emitted('open-config')).toHaveLength(1)
  })

  it('an enabled chain has no warning and no dimming', async () => {
    const r = rowOf(await render(report()))
    expect(r.find('[data-testid="chain-disabled-warning"]').exists()).toBe(false)
    expect(r.findAll('.opacity-60')).toHaveLength(0)
  })
})

describe('summary line', () => {
  it('counts enabled, lagging, stalled and failing chains and flags the ones that are not enabled', async () => {
    const data = report([
      chain(), chain({ chain: 'ethereum', state: 'catching_up' }), chain({ chain: 'polygon', state: 'stalled' }),
      deadChain({ chain: 'arbitrum' }), chain({ chain: 'xlayer', enabled: false, state: 'disabled' })
    ], { summary: { enabled: 4, lagging: 1, stalled: 1, error: 1 } })
    const w = await render(data)
    const text = w.get('[data-testid="chain-status-summary"]').text()
    for (const part of ['4 条已开启', '1 条落后', '1 条停滞', '1 条异常']) expect(text).toContain(part)
    expect(w.get('[data-testid="chain-status-disabled-count"]').text()).toBe('1 条未开启')
    const d = new Date(data.generated_at)
    const p = (n: number) => String(n).padStart(2, '0')
    expect(w.get('[data-testid="chain-status-updated"]').text()).toBe(`更新于 ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`)
    expect(w.find('[data-testid="chain-status-stale"]').exists()).toBe(false)
  })

  it('shows no "not enabled" count when every chain is enabled', async () => {
    const w = await render(report())
    expect(w.find('[data-testid="chain-status-disabled-count"]').exists()).toBe(false)
  })

  it('says so when nothing is configured', async () => {
    const w = await render(report([]))
    expect(w.text()).toContain('没有已配置的链')
  })
})

describe('node list', () => {
  it('grades each node: dot colour, latency, blocks behind and error category', async () => {
    const r = rowOf(await render(report([chain({ nodes: [
      node({ label: 'ankr#0', host: 'rpc.ankr.com' }),
      node({ label: 'drpc#1', host: 'bsc.drpc.org', status: 'slow', latency_ms: 2500 }),
      node({ label: 'blockpi#2', host: 'bsc.blockpi.network', status: 'behind', behind: 30 }),
      node({ label: 'llamarpc#3', host: 'eth.llamarpc.com', status: 'wrong_chain', head: null, behind: null, error_kind: 'wrong_chain' }),
      node({ label: 'x#4', host: '', status: 'error', latency_ms: null, head: null, behind: null, error_kind: 'timeout' })
    ] })])))
    const get = (label: string) => r.get(`[data-testid="node-${label}"]`)
    expect(get('ankr#0').get('.rounded-full').classes()).toContain('bg-green-500')
    expect(get('ankr#0').get('[data-testid="node-latency"]').text()).toBe('120 ms')
    expect(get('ankr#0').find('[data-testid="node-behind"]').exists()).toBe(false)
    expect(get('ankr#0').find('[data-testid="node-error"]').exists()).toBe(false)

    expect(get('drpc#1').get('.rounded-full').classes()).toContain('bg-amber-500')
    expect(get('drpc#1').get('[data-testid="node-latency"]').text()).toBe('2,500 ms')
    expect(get('drpc#1').get('[data-testid="node-latency"]').classes()).toContain('text-warning')

    expect(get('blockpi#2').get('.rounded-full').classes()).toContain('bg-amber-500')
    expect(get('blockpi#2').get('[data-testid="node-behind"]').text()).toBe('落后 30 块')

    expect(get('llamarpc#3').get('.rounded-full').classes()).toContain('bg-red-500')
    expect(get('llamarpc#3').get('[data-testid="node-error"]').text()).toBe('链 ID 不符')
    expect(get('llamarpc#3').get('.font-mono').classes()).toContain('text-error')

    expect(get('x#4').get('.font-mono').text()).toBe('x#4')
    expect(get('x#4').get('[data-testid="node-error"]').text()).toBe('超时')
    expect(get('x#4').get('.sr-only').text()).toBe('异常')
  })

  it('never renders a URL, path or key: only the host the server sent', async () => {
    const w = await render(report([chain({ nodes: [node({ host: 'rpc.ankr.com' })] })]))
    expect(w.text()).toContain('rpc.ankr.com')
    expect(w.html()).not.toMatch(/https?:\/\//)
  })
})
