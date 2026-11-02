import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import USelect from '@nuxt/ui/components/Select.vue'
import CryptoGasWalletPanel from '../CryptoGasWalletPanel.vue'

const mocks = vi.hoisted(() => ({ getAddress: vi.fn(), getStatus: vi.fn(), qr: vi.fn(), copy: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getHotWalletAddress: mocks.getAddress, getHotWalletStatus: mocks.getStatus }))
vi.mock('qrcode', () => ({ default: { toDataURL: mocks.qr } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))

enableAutoUnmount(afterEach)
const gasAddress = '0x1234567890123456789012345678901234567890'
const otherAddress = '0x2345678901234567890123456789012345678901'
const gasStatus = (chain = 'bsc', balance = '0.0123', address = gasAddress) => ({ address, chain, balance, symbol: chain === 'bsc' ? 'BNB' : 'ETH', enough: true })

beforeEach(() => {
  vi.resetAllMocks()
  mocks.getAddress.mockResolvedValue({ index: 0, evm: gasAddress, solana: '' })
  mocks.getStatus.mockResolvedValue([gasStatus()])
  mocks.qr.mockResolvedValue('data:image/png;base64,gas-address-qr')
  mocks.copy.mockResolvedValue(true)
})

async function render(props = { available: true, chains: ['bsc'], revision: 0 }) {
  const wrapper = mount(CryptoGasWalletPanel, { props })
  await flushPromises()
  return wrapper
}

describe('Gas wallet funding (read-only address and local QR)', () => {
  it('shows and copies the full actual Gas address, with a locally generated QR and native balance', async () => {
    const wrapper = await render()
    expect(wrapper.get('[data-testid="crypto-gas-address"]').text()).toBe(gasAddress)
    expect(wrapper.text()).toContain('0.0123 BNB')
    expect(mocks.qr).toHaveBeenCalledWith(gasAddress, { type: 'image/png', width: 184, margin: 2 })
    expect(wrapper.get('[data-testid="crypto-gas-qr"]').attributes('src')).toBe('data:image/png;base64,gas-address-qr')
    await wrapper.get('[data-testid="crypto-gas-copy"]').trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith(gasAddress)
    expect(mocks.getAddress).toHaveBeenCalledWith()
    expect(mocks.getStatus).toHaveBeenCalledWith()
  })

  it('keeps the funding address and QR available while a balance RPC is slow or fails', async () => {
    let rejectBalance!: (error: unknown) => void
    mocks.getStatus.mockReturnValue(new Promise((_, reject) => { rejectBalance = reject }))
    const wrapper = await render()
    expect(wrapper.get('[data-testid="crypto-gas-address"]').text()).toBe(gasAddress)
    expect(wrapper.find('[data-testid="crypto-gas-qr"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('common.loading')
    rejectBalance(new Error('private RPC URL must not appear'))
    await flushPromises()
    expect(wrapper.get('[data-testid="crypto-gas-address"]').text()).toBe(gasAddress)
    expect(wrapper.text()).toContain('admin.crypto.hotWalletUnreachable')
    expect(wrapper.text()).not.toContain('private RPC URL')
  })

  it('distinguishes chains and native currencies, ignoring a balance for a different Gas seed', async () => {
    mocks.getStatus.mockResolvedValue([gasStatus(), gasStatus('base', '0.0234'), gasStatus('ethereum', '999', otherAddress)])
    const wrapper = await render({ available: true, chains: ['bsc', 'base', 'ethereum'], revision: 0 })
    expect(wrapper.find('[data-testid="crypto-gas-network"]').exists()).toBe(true)
    wrapper.findComponent(USelect).vm.$emit('update:modelValue', 'base')
    await flushPromises()
    expect(wrapper.text()).toContain('0.0234 ETH')
    expect(wrapper.get('.gas-wallet__warning').text()).toContain('"network":"Base","symbol":"ETH"')
    wrapper.findComponent(USelect).vm.$emit('update:modelValue', 'ethereum')
    await flushPromises()
    expect(wrapper.text()).not.toContain('999')
    expect(wrapper.text()).toContain('admin.crypto.hotWalletUnreachable')
  })

  it('does not derive or display a wallet before initialization or for unsupported-only chains', async () => {
    const wrapper = await render({ available: false, chains: ['bsc'], revision: 0 })
    expect(mocks.getAddress).not.toHaveBeenCalled()
    expect(mocks.getStatus).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="crypto-gas-qr"]').exists()).toBe(false)
    await wrapper.setProps({ available: true, chains: ['solana'] })
    await flushPromises()
    expect(mocks.getAddress).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.crypto.gasFundingNoNetworks')
  })

  it('rejects invalid or zero addresses instead of generating an unsafe funding QR', async () => {
    for (const evm of ['0x0', `0x${'0'.repeat(40)}`, 'https://untrusted.example/qr']) {
      mocks.getAddress.mockResolvedValueOnce({ evm })
      const wrapper = await render()
      expect(wrapper.find('[data-testid="crypto-gas-address"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('admin.crypto.gasFundingLoadFailed')
      wrapper.unmount()
    }
    expect(mocks.qr).not.toHaveBeenCalled()
  })

  it('discards stale address and QR work when the Gas mnemonic has just been replaced', async () => {
    let resolveOldQR!: (image: string) => void
    mocks.qr.mockReturnValueOnce(new Promise(resolve => { resolveOldQR = resolve }))
    const wrapper = await render()
    mocks.getAddress.mockResolvedValueOnce({ evm: otherAddress })
    mocks.getStatus.mockResolvedValueOnce([gasStatus('bsc', '0.1', otherAddress)])
    mocks.qr.mockResolvedValueOnce('data:image/png;base64,new-wallet-qr')
    await wrapper.setProps({ revision: 1 })
    await flushPromises()
    expect(wrapper.get('[data-testid="crypto-gas-address"]').text()).toBe(otherAddress)
    expect(wrapper.get('[data-testid="crypto-gas-qr"]').attributes('src')).toBe('data:image/png;base64,new-wallet-qr')
    resolveOldQR('data:image/png;base64,old-wallet-qr')
    await flushPromises()
    expect(wrapper.get('[data-testid="crypto-gas-qr"]').attributes('src')).toBe('data:image/png;base64,new-wallet-qr')
  })

  it('retains a copyable address when QR generation fails', async () => {
    mocks.qr.mockRejectedValue(new Error('Canvas unavailable'))
    const wrapper = await render()
    expect(wrapper.get('[data-testid="crypto-gas-address"]').text()).toBe(gasAddress)
    expect(wrapper.text()).toContain('admin.crypto.gasFundingQrFailed')
    await wrapper.get('[data-testid="crypto-gas-copy"]').trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith(gasAddress)
  })
})
