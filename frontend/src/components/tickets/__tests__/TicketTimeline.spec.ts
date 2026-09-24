import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TicketTimeline from '../TicketTimeline.vue'
import type { TicketMessage } from '@/api/tickets'

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
})
