import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AnnouncementBell from '../AnnouncementBell.vue'
import { useAnnouncementStore } from '@/stores/announcements'
import { useAppStore } from '@/stores/app'
import { bodyFindAll } from './overlay-helpers'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

// Slideover and modal are teleported to <body>: unmount first (registered last), then clear.
afterEach(() => {
  document.body.innerHTML = ''
  document.body.style.overflow = ''
})
enableAutoUnmount(afterEach)

const items = () => [
  { id: 1, title: 'Maintenance window', content: '**Gateway** restarts at 02:00', notify_mode: 'silent', read_at: null, created_at: '2026-10-31T12:00:00Z', updated_at: '2026-10-31T12:00:00Z' },
  { id: 2, title: 'New models', content: 'Read already', notify_mode: 'silent', read_at: '2026-10-30T12:00:00Z', created_at: '2026-10-30T12:00:00Z', updated_at: '2026-10-30T12:00:00Z' },
] as any

describe('AnnouncementBell', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  async function open() {
    const store = useAnnouncementStore()
    store.announcements = items()
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    await wrapper.get('button[aria-label="announcements.title"]').trigger('click')
    await flushPromises()
    return { wrapper, store }
  }

  it('shows an unread dot on the bell only while something is unread', async () => {
    const store = useAnnouncementStore()
    store.announcements = items()
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    expect(wrapper.find('span[data-slot="base"]').exists()).toBe(true)
    store.announcements = items().map((item: any) => ({ ...item, read_at: '2026-11-01T00:00:00Z' }))
    await flushPromises()
    expect(wrapper.find('span[data-slot="base"]').exists()).toBe(false)
  })

  it('lists announcements with a plain-text excerpt and marks one read when opened', async () => {
    const { store } = await open()
    const markAsRead = vi.spyOn(store, 'markAsRead').mockResolvedValue()
    const rows = bodyFindAll('[data-test="announcement-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('Maintenance window')
    expect(rows[0].text()).toContain('Gateway restarts at 02:00')

    await rows[0].trigger('click')
    await flushPromises()
    expect(markAsRead).toHaveBeenCalledWith(1)
    expect(document.body.querySelector('.markdown-body strong')?.textContent).toBe('Gateway')

    // A read announcement opens without another request.
    markAsRead.mockClear()
    await rows[1].trigger('click')
    await flushPromises()
    expect(markAsRead).not.toHaveBeenCalled()
  })

  it('marks everything read from the panel header', async () => {
    const { store } = await open()
    const markAllAsRead = vi.spyOn(store, 'markAllAsRead').mockResolvedValue()
    const showSuccess = vi.spyOn(useAppStore(), 'showSuccess')
    const button = bodyFindAll('button').find((node) => node.text() === 'announcements.markAllRead')!
    await button.trigger('click')
    await flushPromises()
    expect(markAllAsRead).toHaveBeenCalledTimes(1)
    expect(showSuccess).toHaveBeenCalledWith('announcements.allMarkedAsRead')
  })

  it('shows the empty state when there are no announcements', async () => {
    const store = useAnnouncementStore()
    store.announcements = []
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    await wrapper.get('button[aria-label="announcements.title"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('announcements.empty')
  })
})
