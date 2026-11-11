import { describe, expect, it } from 'vitest'
import { h } from 'vue'
import { mount } from '@vue/test-utils'
import SettingGroup from '@/components/common/SettingGroup.vue'

describe('SettingGroup', () => {
  it('renders title, description and rows', () => {
    const wrapper = mount(SettingGroup, {
      props: { title: 'Limits', description: 'Spend caps' },
      slots: { default: () => h('div', { class: 'setting-row' }, 'row') },
    })
    expect(wrapper.find('.setting-group__title').text()).toBe('Limits')
    expect(wrapper.find('.setting-group__description').text()).toBe('Spend caps')
    expect(wrapper.find('.setting-group__rows .setting-row').exists()).toBe(true)
  })

  it('is not flush by default, so existing groups keep their closing line', () => {
    const wrapper = mount(SettingGroup)
    expect(wrapper.attributes('data-flush')).toBeUndefined()
    expect(wrapper.find('.setting-group__title').exists()).toBe(false)
  })

  it('marks itself flush when asked, which drops the outer row lines', () => {
    const wrapper = mount(SettingGroup, { props: { flush: true } })
    expect(wrapper.attributes('data-flush')).toBe('true')
  })
})
