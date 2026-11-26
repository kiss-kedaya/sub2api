import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TicketTimeline from '../TicketTimeline.vue'
import type { TicketMessage } from '@/api/tickets'

const fetchAttachmentBlob = vi.fn()
vi.mock('@/api/tickets', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/tickets')>(),
  ticketsAPI: () => ({ fetchAttachmentBlob })
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ locale: { value: 'zh' }, t: (key: string) => key === 'tickets.team' ? '客服团队' : key })
}))

const conversation: TicketMessage[] = [
  { id: 1, ticket_id: 1, author_id: 10, author_role: 'user', author_name: 'Test user', kind: 'reply', content: '<img src=x onerror=alert(1)>', created_at: '2026-09-24T01:00:00Z' },
  { id: 2, ticket_id: 1, author_id: 20, author_role: 'admin', author_name: 'Test admin', kind: 'reply', content: 'Reply', created_at: '2026-09-24T01:01:00Z' },
  { id: 3, ticket_id: 1, author_id: 20, author_role: 'admin', author_name: 'Test admin', kind: 'event', content: 'Closed', created_at: '2026-09-24T01:02:00Z' }
]
function render(admin: boolean) {
  return mount(TicketTimeline, {
    props: { messages: conversation, admin, hasMore: false, loadingOlder: false }
  })
}

describe('TicketTimeline', () => {
  it('shows the user on the right and support on the left for users', () => {
    const wrapper = render(false)
    expect(wrapper.get('[data-message-id="1"]').classes()).toContain('timeline-self')
    expect(wrapper.get('[data-message-id="2"]').classes()).toContain('timeline-other')
    expect(wrapper.get('[data-message-id="2"]').text()).toContain('客服团队')
    expect(wrapper.text()).not.toContain('Test admin')
  })
  it('mirrors the conversation for administrators while keeping events on the axis', () => {
    const wrapper = render(true)
    expect(wrapper.get('[data-message-id="1"]').classes()).toContain('timeline-other')
    expect(wrapper.get('[data-message-id="2"]').classes()).toContain('timeline-self')
    expect(wrapper.get('[data-message-id="3"]').classes()).toContain('timeline-event')
    expect(wrapper.findAll('[data-message-id]').map(el => el.attributes('data-message-id'))).toEqual(['1', '2', '3'])
  })
  it('renders user content as plain text', () => {
    const wrapper = render(false)
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.get('[data-message-id="1"]').text()).toContain('<img src=x onerror=alert(1)>')
  })
  it('keeps replies and closure history without workflow noise', () => {
    const events: TicketMessage[] = [
      ['assignment_changed', null, 20],
      ['priority_changed', 'normal', 'urgent'],
      ['status_changed', 'open', 'waiting_user'],
      ['status_changed', 'waiting_user', 'open'],
      ['status_changed', 'open', 'closed'],
      ['status_changed', 'closed', 'open']
    ].map(([event_type, from, to], index) => ({
      ...conversation[2], id: index + 4, event_type: String(event_type), event_data: { from, to }
    }))
    const wrapper = mount(TicketTimeline, {
      props: { messages: [...conversation, ...events], admin: true, hasMore: true, loadingOlder: false }
    })
    expect(wrapper.findAll('[data-message-id]').map(el => el.attributes('data-message-id'))).toEqual(['1', '2', '3', '8', '9'])
    expect(wrapper.get('[data-message-id="8"]').text()).toContain('tickets.closed')
    expect(wrapper.get('[data-message-id="9"]').text()).toContain('tickets.reopened')
    expect(wrapper.find('.timeline-older').exists()).toBe(true)
  })
  afterEach(() => {
    vi.restoreAllMocks()
    fetchAttachmentBlob.mockReset()
  })
  it('renders authenticated image attachments, hides image-only placeholder text, and supports lightbox preview', async () => {
    const originalCreate = URL.createObjectURL
    const originalRevoke = URL.revokeObjectURL
    const createURL = vi.fn().mockReturnValue('blob:ticket-img-1')
    const revokeURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { value: createURL, configurable: true, writable: true })
    Object.defineProperty(URL, 'revokeObjectURL', { value: revokeURL, configurable: true, writable: true })
    try {
      fetchAttachmentBlob.mockResolvedValue(new Blob(['img'], { type: 'image/png' }))
      const messages: TicketMessage[] = [
        {
          id: 10, ticket_id: 1, author_id: 10, author_role: 'user', author_name: 'Test user',
          kind: 'reply', content: '[图片]',
          event_data: { has_attachments: true, image_only: true },
          attachments: [{ id: 55, ticket_id: 1, message_id: 10, file_name: 'screen.png', content_type: 'image/png', size_bytes: 120, width: 640, height: 480, created_at: '2026-09-24T01:05:00Z' }],
          created_at: '2026-09-24T01:05:00Z'
        },
        {
          id: 11, ticket_id: 1, author_id: 20, author_role: 'admin', author_name: 'Support',
          kind: 'reply', content: 'Look at this screenshot',
          event_data: { has_attachments: true },
          attachments: [{ id: 56, ticket_id: 1, message_id: 11, file_name: 'reply.png', content_type: 'image/png', size_bytes: 200, width: 300, height: 200, created_at: '2026-09-24T01:06:00Z' }],
          created_at: '2026-09-24T01:06:00Z'
        }
      ]
      const wrapper = mount(TicketTimeline, {
        props: { messages, admin: false, hasMore: false, loadingOlder: false },
        global: { stubs: { Teleport: true } }
      })
      await flushPromises()
      expect(fetchAttachmentBlob).toHaveBeenCalledWith(1, 55, expect.any(AbortSignal))
      expect(fetchAttachmentBlob).toHaveBeenCalledWith(1, 56, expect.any(AbortSignal))
      expect(createURL).toHaveBeenCalledTimes(2)
      expect(wrapper.get('[data-message-id="10"]').find('p').exists()).toBe(false)
      expect(wrapper.get('[data-message-id="11"]').find('p').text()).toBe('Look at this screenshot')
      expect(wrapper.get('[data-message-id="10"] img').attributes('src')).toBe('blob:ticket-img-1')

      await wrapper.get('[data-message-id="10"] .timeline-image-thumb').trigger('click')
      expect(wrapper.find('.ticket-lightbox').exists()).toBe(true)
      expect(wrapper.get('.ticket-lightbox-caption').text()).toContain('screen.png')
      await wrapper.get('.ticket-lightbox button').trigger('click')
      expect(wrapper.find('.ticket-lightbox').exists()).toBe(false)

      wrapper.unmount()
      expect(revokeURL).toHaveBeenCalledWith('blob:ticket-img-1')
    } finally {
      Object.defineProperty(URL, 'createObjectURL', { value: originalCreate, configurable: true, writable: true })
      Object.defineProperty(URL, 'revokeObjectURL', { value: originalRevoke, configurable: true, writable: true })
    }
  })
  it('shows error state and retries when an attachment fails to load', async () => {
    const originalCreate = URL.createObjectURL
    const originalRevoke = URL.revokeObjectURL
    Object.defineProperty(URL, 'createObjectURL', { value: vi.fn().mockReturnValue('blob:retry-ok'), configurable: true, writable: true })
    Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true, writable: true })
    try {
      fetchAttachmentBlob
        .mockRejectedValueOnce(new Error('network error'))
        .mockResolvedValueOnce(new Blob(['ok'], { type: 'image/png' }))
      const messages: TicketMessage[] = [{
        id: 12, ticket_id: 1, author_id: 10, author_role: 'user', author_name: 'Test user',
        kind: 'reply', content: '[图片]',
        event_data: { has_attachments: true, image_only: true },
        attachments: [{ id: 88, ticket_id: 1, message_id: 12, file_name: 'err.png', content_type: 'image/png', size_bytes: 50, width: 100, height: 100, created_at: '2026-09-24T01:05:00Z' }],
        created_at: '2026-09-24T01:05:00Z'
      }]
      const wrapper = mount(TicketTimeline, {
        props: { messages, admin: false, hasMore: false, loadingOlder: false },
        global: { stubs: { Teleport: true } }
      })
      await flushPromises()
      expect(wrapper.find('.timeline-image-error').exists()).toBe(true)
      await wrapper.get('.timeline-image-error button').trigger('click')
      await flushPromises()
      expect(wrapper.find('.timeline-image-error').exists()).toBe(false)
      expect(wrapper.get('img').attributes('src')).toBe('blob:retry-ok')
      wrapper.unmount()
    } finally {
      Object.defineProperty(URL, 'createObjectURL', { value: originalCreate, configurable: true, writable: true })
      Object.defineProperty(URL, 'revokeObjectURL', { value: originalRevoke, configurable: true, writable: true })
    }
  })
  it('does not re-fetch or revoke blob URLs when polling passes a fresh attachment object with the same ID, and closes lightbox if the attachment disappears', async () => {
    const originalCreate = URL.createObjectURL
    const originalRevoke = URL.revokeObjectURL
    const createURL = vi.fn().mockReturnValue('blob:stable-img')
    const revokeURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { value: createURL, configurable: true, writable: true })
    Object.defineProperty(URL, 'revokeObjectURL', { value: revokeURL, configurable: true, writable: true })
    try {
      fetchAttachmentBlob.mockResolvedValue(new Blob(['img'], { type: 'image/png' }))
      const makeMessages = (): TicketMessage[] => [{
        id: 20, ticket_id: 1, author_id: 10, author_role: 'user', author_name: 'Test user',
        kind: 'reply', content: '[图片]',
        event_data: { has_attachments: true, image_only: true },
        attachments: [{ id: 99, ticket_id: 1, message_id: 20, file_name: 'stable.png', content_type: 'image/png', size_bytes: 50, width: 100, height: 100, created_at: '2026-09-24T01:05:00Z' }],
        created_at: '2026-09-24T01:05:00Z'
      }]
      const wrapper = mount(TicketTimeline, {
        props: { messages: makeMessages(), admin: false, hasMore: false, loadingOlder: false },
        global: { stubs: { Teleport: true } }
      })
      await flushPromises()
      expect(fetchAttachmentBlob).toHaveBeenCalledTimes(1)
      await wrapper.get('.timeline-image-thumb').trigger('click')
      expect(wrapper.find('.ticket-lightbox').exists()).toBe(true)

      // Simulate a 15-second poll returning fresh JSON objects for the exact same attachment.
      await wrapper.setProps({ messages: makeMessages() })
      await flushPromises()
      expect(fetchAttachmentBlob).toHaveBeenCalledTimes(1)
      expect(revokeURL).not.toHaveBeenCalled()
      expect(wrapper.find('.ticket-lightbox').exists()).toBe(true)

      // Removing the message closes the open lightbox cleanly.
      await wrapper.setProps({ messages: [] })
      await flushPromises()
      expect(wrapper.find('.ticket-lightbox').exists()).toBe(false)
      wrapper.unmount()
    } finally {
      Object.defineProperty(URL, 'createObjectURL', { value: originalCreate, configurable: true, writable: true })
      Object.defineProperty(URL, 'revokeObjectURL', { value: originalRevoke, configurable: true, writable: true })
    }
  })
  it('treats non-image blobs and img decode errors as load failures with retry', async () => {
    const originalCreate = URL.createObjectURL
    const originalRevoke = URL.revokeObjectURL
    const createURL = vi.fn().mockReturnValue('blob:broken-img')
    const revokeURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { value: createURL, configurable: true, writable: true })
    Object.defineProperty(URL, 'revokeObjectURL', { value: revokeURL, configurable: true, writable: true })
    try {
      fetchAttachmentBlob
        .mockResolvedValueOnce(new Blob(['<html>not an image</html>'], { type: 'text/html' }))
        .mockResolvedValueOnce(new Blob(['corrupt'], { type: 'image/png' }))
      const messages: TicketMessage[] = [{
        id: 21, ticket_id: 1, author_id: 10, author_role: 'user', author_name: 'Test user',
        kind: 'reply', content: '[图片]',
        event_data: { has_attachments: true, image_only: true },
        attachments: [{ id: 101, ticket_id: 1, message_id: 21, file_name: 'bad.png', content_type: 'image/png', size_bytes: 50, width: 100, height: 100, created_at: '2026-09-24T01:05:00Z' }],
        created_at: '2026-09-24T01:05:00Z'
      }]
      const wrapper = mount(TicketTimeline, {
        props: { messages, admin: false, hasMore: false, loadingOlder: false },
        global: { stubs: { Teleport: true } }
      })
      await flushPromises()
      expect(createURL).not.toHaveBeenCalled()
      expect(wrapper.find('.timeline-image-error').exists()).toBe(true)

      await wrapper.get('.timeline-image-error button').trigger('click')
      await flushPromises()
      expect(wrapper.find('img').exists()).toBe(true)
      await wrapper.get('img').trigger('error')
      expect(wrapper.find('.timeline-image-error').exists()).toBe(true)
      expect(revokeURL).toHaveBeenCalledWith('blob:broken-img')
      wrapper.unmount()
    } finally {
      Object.defineProperty(URL, 'createObjectURL', { value: originalCreate, configurable: true, writable: true })
      Object.defineProperty(URL, 'revokeObjectURL', { value: originalRevoke, configurable: true, writable: true })
    }
  })
})
