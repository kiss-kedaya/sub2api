import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PlazaFilterBar from '../PlazaFilterBar.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const groups = [
  { id: 1, name: 'Claude', platform: 'anthropic', rate: 1 },
  { id: 2, name: 'GPT', platform: 'openai', rate: 0.07 },
  { id: 3, name: 'Grok', platform: 'grok', rate: 1 },
]

function mountBar(props: Record<string, unknown> = {}) {
  return mount(PlazaFilterBar, {
    props: {
      platforms: ['anthropic', 'openai', 'grok'],
      groups,
      rates: [0.07, 1],
      platform: 'all',
      groupId: 'all',
      rate: 'all',
      search: '',
      ...props,
    },
    global: {
      stubs: {
        Icon: true,
        PlatformIcon: true,
        Select: {
          props: ['modelValue', 'options', 'ariaLabel'],
          emits: ['update:modelValue'],
          template: `
            <div :aria-label="ariaLabel">
              <button
                v-for="option in options"
                :key="String(option.value)"
                type="button"
                :disabled="option.disabled"
                :data-option="String(option.value)"
                @click="$emit('update:modelValue', option.value)"
              >{{ option.label }}</button>
            </div>
          `,
        },
      },
    },
  })
}

describe('PlazaFilterBar faceted filters', () => {
  it('keeps unmatched platforms, groups and rates disabled instead of hiding them', () => {
    const byGroup = mountBar({ groupId: 2 })
    const platforms = byGroup.findAll('.plaza-seg-item')
    expect(platforms.map((button) => button.text().replace(/\s+/g, ' ').trim())).toEqual([
      'modelPlaza.filters.all',
      'anthropic',
      'openai',
      'grok',
    ])
    expect(platforms[1].attributes('disabled')).toBeDefined()
    expect(platforms[2].attributes('disabled')).toBeUndefined()
    expect(platforms[3].attributes('disabled')).toBeDefined()

    const byPlatform = mountBar({ platform: 'openai' })
    const groupButtons = byPlatform.get('[aria-label="modelPlaza.filters.groupLabel"]').findAll('button')
    expect(groupButtons.map((button) => button.text())).toEqual(['modelPlaza.filters.groupLabel', 'Claude', 'GPT', 'Grok'])
    expect(groupButtons[1].attributes('disabled')).toBeDefined()
    expect(groupButtons[2].attributes('disabled')).toBeUndefined()

    const rateButtons = byPlatform.get('[aria-label="modelPlaza.filters.rateLabel"]').findAll('button')
    expect(rateButtons.map((button) => button.text())).toEqual(['modelPlaza.filters.rateLabel', '0.07x', '1x'])
    expect(rateButtons[1].attributes('disabled')).toBeUndefined()
    expect(rateButtons[2].attributes('disabled')).toBeDefined()
  })

  it('emits compact toolbar changes without dropping the all option', async () => {
    const wrapper = mountBar()
    await wrapper.get('.plaza-search-input').setValue('gpt-5')
    expect(wrapper.emitted('update:search')?.at(-1)).toEqual(['gpt-5'])
    await wrapper.get('[aria-label="modelPlaza.filters.groupLabel"]').get('[data-option="2"]').trigger('click')
    expect(wrapper.emitted('update:groupId')?.at(-1)).toEqual([2])
    await wrapper.get('[aria-label="modelPlaza.filters.rateLabel"]').get('[data-option="0.07"]').trigger('click')
    expect(wrapper.emitted('update:rate')?.at(-1)).toEqual([0.07])
    await wrapper.get('.plaza-seg-item[aria-checked="true"]').trigger('click')
    expect(wrapper.emitted('update:platform')?.at(-1)).toEqual(['all'])
  })
})
