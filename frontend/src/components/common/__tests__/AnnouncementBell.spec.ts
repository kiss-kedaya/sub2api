import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AnnouncementBell from '../AnnouncementBell.vue'
import { useAnnouncementStore } from '@/stores/announcements'
import { useAppStore } from '@/stores/app'
import { bodyFindAll } from './overlay-helpers'

const api = vi.hoisted(() => ({
  list: vi.fn(),
  summary: vi.fn(),
  popups: vi.fn(),
  getById: vi.fn(),
  markRead: vi.fn(),
}))
const listResponse = vi.hoisted(() => ({ value: [] as any[] }))

vi.mock('@/api', () => ({ announcementsAPI: api }))

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
    vi.resetAllMocks()
    listResponse.value = items()
    api.list.mockImplementation(() => Promise.resolve(listResponse.value))
    api.summary.mockResolvedValue({ total: 2, unread: 1 })
    api.popups.mockResolvedValue([])
    api.markRead.mockResolvedValue({ message: 'ok' })
  })

  async function open() {
    const store = useAnnouncementStore()
    store.unreadCount = 1
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    await wrapper.get('button[aria-label="announcements.title"]').trigger('click')
    await flushPromises()
    return { wrapper, store }
  }

  it('shows an unread dot on the bell only while something is unread', async () => {
    const store = useAnnouncementStore()
    store.unreadCount = 2
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    expect(wrapper.find('span[data-slot="base"]').exists()).toBe(true)
    store.unreadCount = 0
    await flushPromises()
    expect(wrapper.find('span[data-slot="base"]').exists()).toBe(false)
  })

  it('only fetches the timeline when the panel is opened', async () => {
    const store = useAnnouncementStore()
    store.unreadCount = 1
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    await flushPromises()
    expect(api.list).not.toHaveBeenCalled()

    await wrapper.get('button[aria-label="announcements.title"]').trigger('click')
    await flushPromises()
    expect(api.list).toHaveBeenCalledTimes(1)
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

  it('fetches the body when a timeline row arrives without one', async () => {
    listResponse.value = [
      { id: 9, title: 'Heavy notice', excerpt: 'Summary only', notify_mode: 'silent', read_at: '2026-10-31T12:00:00Z', created_at: '2026-10-31T12:00:00Z', updated_at: '2026-10-31T12:00:00Z' },
    ]
    api.getById.mockResolvedValue({
      id: 9, title: 'Heavy notice', content: '**Full** body', notify_mode: 'silent',
      read_at: '2026-10-31T12:00:00Z', created_at: '2026-10-31T12:00:00Z', updated_at: '2026-10-31T12:00:00Z',
    })
    await open()

    const rows = bodyFindAll('[data-test="announcement-row"]')
    expect(rows[0].text()).toContain('Summary only')
    await rows[0].trigger('click')
    await flushPromises()

    expect(api.getById).toHaveBeenCalledWith(9)
    expect(document.body.querySelector('.markdown-body strong')?.textContent).toBe('Full')
  })

  it('keeps a failed detail visible instead of showing an empty body', async () => {
    listResponse.value = [
      { id: 10, title: 'Broken notice', excerpt: 'Summary only', notify_mode: 'silent', read_at: null, created_at: '2026-10-31T12:00:00Z', updated_at: '2026-10-31T12:00:00Z' },
    ]
    api.getById.mockRejectedValue(new Error('gone'))
    await open()

    await bodyFindAll('[data-test="announcement-row"]')[0].trigger('click')
    await flushPromises()

    expect(document.body.textContent).toContain('announcements.loadFailed')
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
    listResponse.value = []
    const store = useAnnouncementStore()
    store.unreadCount = 0
    const wrapper = mount(AnnouncementBell, { attachTo: document.body })
    await wrapper.get('button[aria-label="announcements.title"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('announcements.empty')
  })
})
