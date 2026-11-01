import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupSelector from '../GroupSelector.vue'
import { bodyFindAll } from './overlay-helpers'

const authState = { isSimpleMode: false }

vi.mock('@/stores', () => ({ useAuthStore: () => authState }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

afterEach(() => {
  document.body.innerHTML = ''
})
enableAutoUnmount(afterEach)

const groups = [
  { id: 1, name: 'Basic', platform: 'anthropic', status: 'active' },
  { id: 2, name: 'Composite', platform: 'composite', status: 'active' }
] as any

const mountSelector = (modelValue: number[] = []) => mount(GroupSelector, {
  attachTo: document.body,
  props: { modelValue, groups, 'onUpdate:modelValue': (value: number[]) => wrapper?.setProps({ modelValue: value }) },
  global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
})
let wrapper: ReturnType<typeof mountSelector> | undefined

// The options live in a Nuxt UI menu teleported to <body>.
async function openOptions(target: ReturnType<typeof mountSelector>) {
  await target.get('[data-test="group-selector"]').trigger('click')
  await flushPromises()
  return bodyFindAll('[role="option"]')
}

describe('GroupSelector simple-mode binding policy', () => {
  beforeEach(() => { authState.isSimpleMode = false; wrapper = undefined })

  it('hides composite groups in simple mode and preserves basic groups', async () => {
    authState.isSimpleMode = true
    wrapper = mountSelector()
    const options = (await openOptions(wrapper)).map((option) => option.text())
    expect(options.join(' ')).toContain('Basic')
    expect(options.join(' ')).not.toContain('Composite')
  })

  it('keeps composite groups available in advanced mode', async () => {
    wrapper = mountSelector()
    const options = (await openOptions(wrapper)).map((option) => option.text())
    expect(options.join(' ')).toContain('Composite')
  })

  it('cleans hidden historical composite IDs while preserving visible selections', () => {
    authState.isSimpleMode = true
    const selector = mountSelector([1, 2])
    expect(selector.emitted('update:modelValue')).toEqual([[[1]]])
  })

  it('toggles a group from the menu and shows selected groups on the trigger', async () => {
    wrapper = mountSelector([1])
    expect(wrapper.get('[data-test="group-selector"]').text()).toContain('Basic')
    const composite = (await openOptions(wrapper)).find((option) => option.text().includes('Composite'))!
    await composite.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[1, 2]])
    expect(wrapper.get('[data-test="group-selector"]').text()).toContain('Composite')
  })
})
