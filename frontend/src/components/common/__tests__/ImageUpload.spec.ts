import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ImageUpload from '../ImageUpload.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) })
}))

enableAutoUnmount(afterEach)

async function pick(wrapper: ReturnType<typeof mount>, file: File) {
  const input = wrapper.get('input[type="file"]').element as HTMLInputElement
  Object.defineProperty(input, 'files', { configurable: true, value: [file] })
  input.dispatchEvent(new Event('change'))
  // FileReader is async in jsdom.
  await new Promise((resolve) => setTimeout(resolve, 20))
  await flushPromises()
}

describe('ImageUpload', () => {
  it('reads a picked image into a data URL and accepts images only', async () => {
    const wrapper = mount(ImageUpload, { props: { modelValue: '' } })
    expect(wrapper.get('input[type="file"]').attributes('accept')).toBe('image/*')
    await pick(wrapper, new File(['png'], 'logo.png', { type: 'image/png' }))
    const value = wrapper.emitted('update:modelValue')?.[0]?.[0] as string
    expect(value.startsWith('data:image/png;base64,')).toBe(true)
  })

  it('rejects a file over maxSize with a message and no value', async () => {
    const wrapper = mount(ImageUpload, { props: { modelValue: '', maxSize: 2 } })
    await pick(wrapper, new File(['too big'], 'logo.png', { type: 'image/png' }))
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.text()).toContain('common.fileTooLargeKb')
  })

  it('reads SVG text in svg mode and removes the current value', async () => {
    const wrapper = mount(ImageUpload, { props: { modelValue: '<svg></svg>', mode: 'svg', removeLabel: 'Remove' } })
    expect(wrapper.get('input[type="file"]').attributes('accept')).toBe('.svg')
    await pick(wrapper, new File(['  <svg viewBox="0 0 1 1"></svg>  '], 'icon.svg', { type: 'image/svg+xml' }))
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['<svg viewBox="0 0 1 1"></svg>'])

    const remove = wrapper.findAll('button').find((button) => button.text() === 'Remove')!
    await remove.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual([''])
  })
})
