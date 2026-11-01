import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { useToast } from '@nuxt/ui/composables/useToast'
import Toast from '../Toast.vue'
import { useAppStore } from '@/stores/app'

enableAutoUnmount(afterEach)

const nuxtToasts = () => useToast().toasts.value

async function settle() {
  await nextTick()
  await flushPromises()
  await nextTick()
}

describe('Toast bridge', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    useToast().clear()
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows store toasts through Nuxt UI with the matching color and icon', async () => {
    const store = useAppStore()
    mount(Toast)
    const okId = store.showSuccess('Saved')
    const errId = store.showError('Upstream 502')
    await settle()

    const ok = nuxtToasts().find((t) => t.id === okId)
    const err = nuxtToasts().find((t) => t.id === errId)
    expect(ok).toMatchObject({ title: 'Saved', color: 'success', icon: 'i-lucide-circle-check', duration: 3000 })
    expect(err).toMatchObject({ title: 'Upstream 502', color: 'error', icon: 'i-lucide-circle-x', duration: 5000 })
  })

  it('keeps a toast without duration open until it is closed', async () => {
    const store = useAppStore()
    mount(Toast)
    const id = store.showToast('warning', 'Pinned')
    await settle()
    expect(nuxtToasts().find((t) => t.id === id)).toMatchObject({ color: 'warning', duration: Number.POSITIVE_INFINITY })
  })

  it('closes the Nuxt UI toast when the store removes it', async () => {
    const store = useAppStore()
    mount(Toast)
    const id = store.showInfo('Bye', 1000)
    await settle()
    expect(nuxtToasts().some((t) => t.id === id && t.open !== false)).toBe(true)

    vi.advanceTimersByTime(1000)
    await settle()
    expect(store.toasts.some((t) => t.id === id)).toBe(false)
    expect(nuxtToasts().some((t) => t.id === id && t.open !== false)).toBe(false)
  })

  it('removes the store toast when Nuxt UI closes it', async () => {
    const store = useAppStore()
    mount(Toast)
    const keep = store.showToast('info', 'Keep')
    const close = store.showToast('error', 'Close me')
    await settle()

    useToast().remove(close)
    await settle()
    expect(store.toasts.map((t) => t.id)).toEqual([keep])
  })
})
