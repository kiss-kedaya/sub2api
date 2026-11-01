import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ConfirmHost from '@/components/common/ConfirmHost.vue'
import { cancelConfirmation, confirm, settleConfirmation, useConfirmation } from '@/composables/useConfirmation'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const ConfirmDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false },
    title: { type: String, default: '' },
    message: { type: String, default: '' },
    confirmText: { type: String, default: '' },
    cancelText: { type: String, default: '' },
    danger: { type: Boolean, default: false }
  },
  emits: ['confirm', 'cancel'],
  template: `<div v-if="show" data-testid="confirm-dialog">
    <p data-testid="confirm-message">{{ message }}</p>
    <button data-testid="confirm-ok" @click="$emit('confirm')">ok</button>
    <button data-testid="confirm-cancel" @click="$emit('cancel')">cancel</button>
  </div>`
})

const mounted: Array<ReturnType<typeof mount>> = []

function mountHost(router?: Router) {
  const wrapper = mount(ConfirmHost, {
    global: {
      plugins: router ? [router] : [],
      stubs: { ConfirmDialog: ConfirmDialogStub }
    }
  })
  mounted.push(wrapper)
  return wrapper
}

function makeRouter(): Router {
  const page = { template: '<div />' }
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/a', component: page },
      { path: '/b', component: page }
    ]
  })
}

afterEach(() => {
  for (const wrapper of mounted.splice(0)) wrapper.unmount()
  cancelConfirmation()
})

describe('useConfirmation', () => {
  it('resolves false when no host is mounted', async () => {
    await expect(confirm('are you sure?')).resolves.toBe(false)
  })

  it('opens the dialog and resolves true when confirmed', async () => {
    const wrapper = mountHost()
    await flushPromises()

    const answer = confirm({ title: 'Delete group', message: 'delete it?' })
    await flushPromises()

    expect(wrapper.get('[data-testid="confirm-message"]').text()).toBe('delete it?')
    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')

    await expect(answer).resolves.toBe(true)
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(false)
  })

  it('resolves false when the dialog is cancelled', async () => {
    const wrapper = mountHost()
    await flushPromises()

    const answer = confirm('delete it?')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click')

    await expect(answer).resolves.toBe(false)
  })

  it('shows queued requests one at a time', async () => {
    const wrapper = mountHost()
    await flushPromises()

    const first = confirm('first?')
    const second = confirm('second?')
    await flushPromises()

    expect(wrapper.get('[data-testid="confirm-message"]').text()).toBe('first?')
    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="confirm-message"]').text()).toBe('second?')
    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click')

    await expect(first).resolves.toBe(true)
    await expect(second).resolves.toBe(false)
  })

  it('cancels the pending request when the route changes', async () => {
    const router = makeRouter()
    await router.push('/a')
    const wrapper = mountHost(router)
    await flushPromises()

    const answer = confirm('delete it?')
    await flushPromises()
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(true)

    await router.push('/b')
    await flushPromises()

    await expect(answer).resolves.toBe(false)
    expect(wrapper.find('[data-testid="confirm-dialog"]').exists()).toBe(false)
  })

  it('cancels everything that is still waiting when the host unmounts', async () => {
    const wrapper = mountHost()
    await flushPromises()

    const active = confirm('first?')
    const queued = confirm('second?')
    await flushPromises()

    wrapper.unmount()
    mounted.splice(mounted.indexOf(wrapper), 1)

    await expect(active).resolves.toBe(false)
    await expect(queued).resolves.toBe(false)
  })

  it('exposes a read-only state and ignores settles without an active request', () => {
    const { state } = useConfirmation()
    expect(state.open).toBe(false)
    settleConfirmation(true)
    expect(state.open).toBe(false)
  })
})
