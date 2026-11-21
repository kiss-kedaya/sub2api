import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import SignerRiskDialog from '../SignerRiskDialog.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const PHRASE = '我已离线保存分片，丢失无法找回'

async function render(show = true) {
  const wrapper = mount(SignerRiskDialog, {
    props: { show, phrase: PHRASE, points: ['point one', 'point two', 'point three'] },
    global: { stubs: { teleport: true } }
  })
  await flushPromises()
  await new Promise(resolve => setTimeout(resolve, 0))
  return wrapper
}

describe('blocking signer risk acknowledgement', () => {
  it('renders the three backend risk points and the exact phrase', async () => {
    const wrapper = await render()
    const points = wrapper.findAll('.signer-risk__points li')
    expect(points).toHaveLength(3)
    const rendered = points.map(point => point.text())
    for (const expected of ['point one', 'point two', 'point three']) {
      expect(rendered.some(text => text.includes(expected))).toBe(true)
    }
    expect(wrapper.get('[data-testid="signer-risk-phrase"]').text()).toBe(PHRASE)
  })

  it('keeps the confirm button disabled until the phrase is typed exactly', async () => {
    const wrapper = await render()
    const confirm = wrapper.get('[data-testid="signer-risk-confirm"]')
    expect(confirm.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="signer-risk-input"]').setValue('我已离线保存分片')
    expect(confirm.attributes('disabled')).toBeDefined()

    // Whitespace inside the phrase is ignored, matching the Go comparison.
    await wrapper.get('[data-testid="signer-risk-input"]').setValue(' 我已离线 保存分片，丢失无法找回 ')
    expect(confirm.attributes('disabled')).toBeUndefined()

    await confirm.trigger('click')
    expect(wrapper.emitted('confirm')?.[0]).toEqual([' 我已离线 保存分片，丢失无法找回 '])
  })

  it('does not emit when the phrase is wrong (the server validates it too)', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="signer-risk-input"]').setValue('我已离线保存分片')
    expect(wrapper.get('[data-testid="signer-risk-confirm"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="signer-risk-confirm"]').trigger('click')
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('can be cancelled without acknowledging', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="signer-risk-cancel"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('falls back to the built-in three points when the backend notice failed to load', async () => {
    const wrapper = mount(SignerRiskDialog, {
      props: { show: true, phrase: PHRASE },
      global: { stubs: { teleport: true } }
    })
    await flushPromises()
    expect(wrapper.findAll('.signer-risk__points li')).toHaveLength(3)
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.riskPointFallback1')
  })
})
