import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import TicketsView from '../TicketsView.vue'
import type { TicketAttachment, TicketDetail } from '@/api/tickets'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  stats: vi.fn(),
  detail: vi.fn(),
  read: vi.fn(),
  reply: vi.fn(),
  update: vi.fn(),
  create: vi.fn(),
  proactiveCreate: vi.fn(),
  uploadAttachment: vi.fn(),
  deletePendingAttachment: vi.fn(),
  fetchAttachmentBlob: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/tickets', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/tickets')>(),
  ticketsAPI: () => mocks,
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: ref('zh') }),
}))

let wrapper: VueWrapper | undefined

function detailFixture(id = 1): TicketDetail {
  const ts = '2026-09-24T10:00:00Z'
  return {
    ticket: {
      id, user_id: 9, subject: 'Image ticket', contact: '',
      category: 'api', priority: 'normal', status: 'open', assignee_id: null,
      assignee_name: '', user_name: 'Tester', created_at: ts, updated_at: ts,
      last_message_at: ts, last_message_preview: '[图片]', last_message_id: 10, unread_count: 2,
    },
    messages: [{
      id: 10, ticket_id: id, author_id: 1, author_role: 'admin', author_name: 'Support',
      content: 'Hello', kind: 'reply', created_at: ts,
    }],
    has_more: false,
  }
}

async function mountTickets(admin: boolean, path = admin ? '/admin/tickets/1' : '/tickets/1') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:scope(admin/)?tickets/:id?', component: { template: '<div />' } }],
  })
  await router.push(path)
  await router.isReady()
  wrapper = mount(TicketsView, {
    props: { admin },
    global: {
      plugins: [router],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Select: { props: ['options', 'modelValue', 'size', 'disabled'], template: '<div class="stub-select" />' },
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show" class="stub-dialog"><slot /><slot name="footer" /></div>',
        },
        ConfirmDialog: { template: '<div />' },
        TicketTimeline: {
          template: '<div class="stub-timeline" />',
          methods: { scrollToBottom: vi.fn(), capturePosition: vi.fn(), restorePosition: vi.fn() },
        },
      },
    },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.resetAllMocks()
  const detail = detailFixture()
  mocks.list.mockResolvedValue({ items: [detail.ticket], total: 1, page: 1, page_size: 20, pages: 1 })
  mocks.stats.mockResolvedValue({ total: 5, open: 3, in_progress: 0, waiting_user: 0, resolved: 0, closed: 2, unread: 2, can_create: true })
  mocks.detail.mockResolvedValue(detail)
  mocks.read.mockResolvedValue(undefined)
  mocks.reply.mockResolvedValue(detail)
  mocks.create.mockResolvedValue(detail)
  mocks.uploadAttachment.mockResolvedValue({
    id: 77, file_name: 'shot.png', content_type: 'image/png', size_bytes: 128, width: 100, height: 80, created_at: '2026-09-24T10:00:00Z'
  } satisfies TicketAttachment)
  mocks.deletePendingAttachment.mockResolvedValue(undefined)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('TicketsView unread tab and image sending', () => {
  it.each([false, true])('renders the Unread folder tab with count and filters list by status=unread (admin=%s)', async (admin) => {
    const view = await mountTickets(admin, admin ? '/admin/tickets' : '/tickets')
    const tabs = view.findAll('.tickets-status-tabs button')
    expect(tabs.map((tab) => tab.text())).toEqual([
      'tickets.all5',
      'tickets.unreadTab2',
      'tickets.notClosed3',
      'tickets.statuses.closed2',
    ])
    expect(tabs[1].find('span').classes()).toContain('tab-badge-unread')

    mocks.list.mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    await tabs[1].trigger('click')
    await flushPromises()

    expect(mocks.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ status: 'unread', page: 1 }),
      expect.any(AbortSignal),
    )
    expect(view.get('.tickets-empty').text()).toContain('tickets.emptyUnread')
  })

  it('uploads an image via reply composer and submits an image-only reply', async () => {
    const view = await mountTickets(false)
    const file = new File(['pngdata'], 'shot.png', { type: 'image/png' })
    const input = view.get('.ticket-composer input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    await flushPromises()

    expect(mocks.uploadAttachment).toHaveBeenCalledWith(file, expect.any(AbortSignal))
    expect(view.findAll('.ticket-pending-image')).toHaveLength(1)

    const sendBtn = view.get('.ticket-composer button[type="submit"]')
    expect(sendBtn.attributes('disabled')).toBeUndefined()
    await view.get('.ticket-composer').trigger('submit')
    await flushPromises()

    expect(mocks.reply).toHaveBeenCalledWith(1, expect.objectContaining({
      content: '',
      attachment_ids: [77],
    }))
    expect(view.findAll('.ticket-pending-image')).toHaveLength(0)
  })

  it('allows creating a new ticket with subject and attached image only', async () => {
    const view = await mountTickets(false, '/tickets')
    await view.get('.tickets-heading-actions .ticket-button-primary').trigger('click')
    await flushPromises()

    await view.get('#ticket-subject').setValue('Error screenshot')
    const file = new File(['pngdata'], 'bug.png', { type: 'image/png' })
    const input = view.get('#new-ticket-form input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    await flushPromises()

    await view.get('#new-ticket-form').trigger('submit')
    await flushPromises()

    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      subject: 'Error screenshot',
      content: '',
      attachment_ids: [77],
    }))
  })

  it('supports drag-and-drop image upload and pending removal in the New Ticket modal, and does not hijack plain text drags in the composer', async () => {
    const view = await mountTickets(false)

    // Plain-text dragover in composer must not be prevented.
    const textDrag = new Event('dragover', { cancelable: true })
    Object.defineProperty(textDrag, 'dataTransfer', { value: { types: ['text/plain'] } })
    view.get('.ticket-composer').element.dispatchEvent(textDrag)
    expect(textDrag.defaultPrevented).toBe(false)

    // File dragover in composer must be prevented so dropping works.
    const fileDrag = new Event('dragover', { cancelable: true })
    Object.defineProperty(fileDrag, 'dataTransfer', { value: { types: ['Files'] } })
    view.get('.ticket-composer').element.dispatchEvent(fileDrag)
    expect(fileDrag.defaultPrevented).toBe(true)

    // Drag-and-drop onto New Ticket modal uploads the dropped image, and removing it cleans up server pending row.
    await view.get('.tickets-heading-actions .ticket-button-primary').trigger('click')
    await flushPromises()
    const file = new File(['pngdata'], 'dropped.png', { type: 'image/png' })
    const dropEvent = new Event('drop', { cancelable: true })
    Object.defineProperty(dropEvent, 'dataTransfer', { value: { types: ['Files'], files: [file] } })
    view.get('#new-ticket-form').element.dispatchEvent(dropEvent)
    await flushPromises()
    expect(dropEvent.defaultPrevented).toBe(true)
    expect(mocks.uploadAttachment).toHaveBeenCalledWith(file, expect.any(AbortSignal))
    expect(view.findAll('#new-ticket-form .ticket-pending-image')).toHaveLength(1)

    await view.get('#new-ticket-form .ticket-pending-remove').trigger('click')
    expect(mocks.deletePendingAttachment).toHaveBeenCalledWith(77)
    expect(view.findAll('#new-ticket-form .ticket-pending-image')).toHaveLength(0)
  })
})
