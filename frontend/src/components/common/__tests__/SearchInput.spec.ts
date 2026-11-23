import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'

import SearchInput from '../SearchInput.vue'

vi.mock('@vueuse/core', async () => {
  const actual = await vi.importActual<typeof import('@vueuse/core')>('@vueuse/core')
  return {
    ...actual,
    useDebounceFn: (fn: (value: string) => void) => fn,
  }
})

describe('SearchInput autofill contract', () => {
  // Key 列表页搜索框：浏览器曾把登录账号自动填进来（密码管理器把页面里的
  // password 输入框当成登录表单，把账号塞进了最近的文本输入框）。
  it('renders a search input that password managers must leave alone', () => {
    const wrapper = mount(SearchInput, { props: { modelValue: '' } })
    const input = wrapper.get('input')
    expect(input.attributes('type')).toBe('search')
    expect(input.attributes('name')).toBe('list-search')
    expect(input.attributes('autocomplete')).toBe('off')
    expect(input.attributes('data-lpignore')).toBe('true')
    expect(input.attributes('data-1p-ignore')).toBe('true')
    expect(input.attributes('spellcheck')).toBe('false')
    wrapper.unmount()
  })
})
