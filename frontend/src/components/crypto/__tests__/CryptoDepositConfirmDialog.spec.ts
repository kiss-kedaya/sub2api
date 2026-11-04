import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { DepositConfirmOutcome, DepositRecord } from '@/api/admin/crypto'
import zh from '@/i18n/locales/zh/admin/crypto'
import en from '@/i18n/locales/en/admin/crypto'
import CryptoDepositConfirmDialog from '../CryptoDepositConfirmDialog.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))
enableAutoUnmount(afterEach)

const row = (id: number, chain: string, amount: string): DepositRecord => ({
  id, chain, tx_hash: `0xhash${id}`, log_index: 0, from_address: '0xfrom', to_address: '0xto',
  token: 'USDT', amount, status: 'pending', created_at: '2026-11-05T00:00:00Z'
})
const rows = [row(302, 'bsc', '10.5'), row(321, 'ethereum', '4'), row(330, 'bsc', '1')]
const outcome: DepositConfirmOutcome = {
  credited: 1,
  failed: 2,
  results: [
    { id: 302, status: 'credited', order_id: 123 },
    { id: 321, status: 'rejected', message: 'receipt not found' },
    { id: 330, status: 'error', message: 'rpc timeout' },
    { id: 999, status: 'already_credited', order_id: 77 },
    { id: 1000, status: 'something_new' }
  ]
}

// BaseDialog renders its panel from a post-mount watcher, so wait one tick before asserting.
async function render(props: Partial<InstanceType<typeof CryptoDepositConfirmDialog>['$props']> = {}) {
  const wrapper = mount(CryptoDepositConfirmDialog, {
    props: { show: true, rows, total: '15.50', outcome: null, ...props },
    global: { stubs: { teleport: true } }
  })
  await flushPromises()
  return wrapper
}

describe('Deposit manual-confirm dialog', () => {
  it('lists the count, the total and each involved chain once', async () => {
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.depositConfirmTitle')
    expect(wrapper.get('[data-testid="deposit-confirm-count"]').text()).toBe('3')
    expect(wrapper.get('[data-testid="deposit-confirm-total"]').text()).toBe('15.50')
    expect(wrapper.get('[data-testid="deposit-confirm-chains"]').text()).toBe('bsc, ethereum')
    expect(wrapper.get('[data-testid="deposit-confirm-warning"]').text()).toContain('admin.crypto.depositConfirmWarning')
    expect(wrapper.find('[data-testid="deposit-result-summary"]').exists()).toBe(false)
  })

  it('states in both languages what is skipped and that crediting cannot be undone', async () => {
    expect(zh.crypto.depositConfirmWarning).toContain('跳过区块确认数和出账安全扫描')
    expect(zh.crypto.depositConfirmWarning).toContain('只核对链上收据')
    expect(zh.crypto.depositConfirmWarning).toContain('立即给用户加余额')
    expect(zh.crypto.depositConfirmWarning).toContain('不可撤销')
    expect(en.crypto.depositConfirmWarning).toMatch(/skipped/i)
    expect(en.crypto.depositConfirmWarning).toMatch(/on-chain receipt/i)
    expect(en.crypto.depositConfirmWarning).toMatch(/immediately/i)
    expect(en.crypto.depositConfirmWarning).toMatch(/cannot be undone/i)
    // The verification prompt repeats the same facts next to the code box.
    expect(zh.crypto.depositConfirmVerify).toContain('不可撤销')
    expect(en.crypto.depositConfirmVerify).toMatch(/cannot be undone/i)
  })

  it('emits the trimmed note on confirm and a close on cancel', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="deposit-confirm-note"]').setValue('  ticket 88  ')
    await wrapper.get('[data-testid="deposit-confirm-submit"]').trigger('click')
    expect(wrapper.emitted('confirm')).toEqual([['ticket 88']])
    await wrapper.get('[data-testid="deposit-confirm-cancel"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('emits an empty note when the operator typed nothing', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="deposit-confirm-submit"]').trigger('click')
    expect(wrapper.emitted('confirm')).toEqual([['']])
  })

  it('cannot confirm an empty snapshot', async () => {
    const wrapper = await render({ rows: [], total: '0.00' })
    expect(wrapper.get('[data-testid="deposit-confirm-submit"]').attributes('disabled')).toBeDefined()
  })

  it('clears a previous note each time the dialog opens', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="deposit-confirm-note"]').setValue('old note')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect((wrapper.get('[data-testid="deposit-confirm-note"]').element as HTMLInputElement).value).toBe('')
  })

  it('shows every deposit outcome with its reason and order, and only offers close', async () => {
    const wrapper = await render({ outcome })
    expect(wrapper.text()).toContain('admin.crypto.depositResultTitle')
    expect(wrapper.get('[data-testid="deposit-result-summary"]').text()).toBe('admin.crypto.depositResultSummary{"credited":1,"failed":2}')
    const items = wrapper.findAll('[data-testid="deposit-result-item"]')
    expect(items).toHaveLength(5)
    expect(items[0].text()).toContain('#302 · bsc · 10.5')
    expect(items[0].text()).toContain('admin.crypto.depositResultCredited')
    expect(items[0].text()).toContain('admin.crypto.depositResultOrder{"id":123}')
    expect(items[1].text()).toContain('admin.crypto.depositResultRejected')
    expect(items[1].text()).toContain('receipt not found')
    expect(items[2].text()).toContain('admin.crypto.depositResultError')
    expect(items[2].text()).toContain('rpc timeout')
    expect(items[3].text()).toContain('admin.crypto.depositResultAlready')
    expect(items[3].text()).toContain('admin.crypto.depositResultOrder{"id":77}')
    // A row missing from the snapshot still reports its id; an unknown status stays readable.
    expect(items[3].text()).toContain('#999')
    expect(items[3].text()).not.toContain('·')
    expect(items[4].text()).toContain('something_new')
    expect(wrapper.find('[data-testid="deposit-confirm-submit"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="deposit-confirm-cancel"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="deposit-confirm-note"]').exists()).toBe(false)
  })

  it('closes from the result view', async () => {
    const wrapper = await render({ outcome })
    await wrapper.get('[data-testid="deposit-result-close"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })
})
