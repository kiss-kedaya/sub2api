import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { useTicketWorkspace } from '../useTicketWorkspace'
import type { TicketDetail } from '@/api/tickets'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), stats: vi.fn(), detail: vi.fn(), reply: vi.fn(), update: vi.fn(), create: vi.fn(), read: vi.fn(),
  showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/tickets', () => ({
  ticketsAPI: () => mocks,
  newClientID: () => '11111111-1111-4111-8111-111111111111'
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function fixture(id: number, messageID = id, hasMore = false): TicketDetail {
  return {
    ticket: { id, user_id: 9, subject: `Ticket ${id}`, contact: 'example.test', category: 'api', priority: 'normal', status: 'open', assignee_id: null, assignee_name: '', user_name: 'User', created_at: '', updated_at: '', last_message_at: '', last_message_preview: '', unread_count: 1, last_message_id: messageID },
    messages: [{ id: messageID, ticket_id: id, author_id: 9, author_role: 'user', author_name: 'User', content: 'Message', kind: 'reply', created_at: '' }],
    has_more: hasMore
  }
}
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done }); return { promise, resolve } }
let wrapper: VueWrapper | undefined
let workspace: ReturnType<typeof useTicketWorkspace>
async function setup() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/tickets/:id?', component: { template: '<div />' } }] })
  await router.push('/tickets/1')
  await router.isReady()
  wrapper = mount(defineComponent({ setup() { workspace = useTicketWorkspace(ref(false)); return () => null } }), {
    global: { plugins: [router] }
  })
  await flushPromises()
  return router
}
beforeEach(() => {
  vi.resetAllMocks()
  mocks.list.mockResolvedValue({ items: [fixture(1).ticket, fixture(2).ticket], total: 2, page: 1, page_size: 20, pages: 1 })
  mocks.stats.mockResolvedValue({ total: 2, open: 2, in_progress: 0, waiting_user: 0, resolved: 0, closed: 0, unread: 1, can_create: true })
  mocks.detail.mockImplementation((id: number) => Promise.resolve(fixture(id)))
  mocks.read.mockResolvedValue(undefined)
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('ticket workspace', () => {
  it('ignores a late detail response after selecting another ticket', async () => {
    const router = await setup()
    const late = deferred<TicketDetail>()
    mocks.detail.mockReturnValueOnce(late.promise)
    const request = workspace.refresh(true)
    await router.push('/tickets/2')
    await flushPromises()
    late.resolve(fixture(1, 99))
    await request
    expect(workspace.detail.value?.ticket.id).toBe(2)
  })
  it('keeps per-ticket drafts and never clears the new ticket draft when a send completes', async () => {
    const router = await setup()
    workspace.draft.value = 'Reply for one'
    const pending = deferred<TicketDetail>()
    mocks.reply.mockReturnValueOnce(pending.promise)
    const sending = workspace.send()
    await router.push('/tickets/2')
    await flushPromises()
    workspace.draft.value = 'Reply for two'
    pending.resolve(fixture(1, 3))
    await sending
    expect(mocks.reply).toHaveBeenCalledWith(1, expect.objectContaining({ content: 'Reply for one' }))
    expect(workspace.draft.value).toBe('Reply for two')
    expect(workspace.detail.value?.ticket.id).toBe(2)
    await router.push('/tickets/1')
    expect(workspace.draft.value).toBe('')
  })
  it('retries the same reply idempotently and retains the draft after an error', async () => {
    await setup()
    workspace.draft.value = 'Reply'
    mocks.reply.mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValueOnce(fixture(1, 3))
    expect(await workspace.send()).toBe(false)
    expect(workspace.draft.value).toBe('Reply')
    expect(await workspace.send()).toBe(true)
    expect(mocks.reply.mock.calls[0][1].client_id).toBe(mocks.reply.mock.calls[1][1].client_id)
    expect(workspace.draft.value).toBe('')
  })
  it('retains a failed reply idempotency key after replying in another ticket', async () => {
    const router = await setup()
    workspace.draft.value = 'Reply for one'
    mocks.reply.mockRejectedValueOnce(new Error('Response lost'))
    await workspace.send()
    const firstKey = mocks.reply.mock.calls[0][1].client_id
    await router.push('/tickets/2')
    await flushPromises()
    workspace.draft.value = 'Reply for two'
    mocks.reply.mockResolvedValueOnce(fixture(2, 3))
    await workspace.send()
    await router.push('/tickets/1')
    await flushPromises()
    mocks.reply.mockResolvedValueOnce(fixture(1, 4))
    await workspace.send()
    expect(mocks.reply.mock.calls[2][1].client_id).toBe(firstKey)
  })
  it('ignores an earlier-page response if a refresh replaced its timeline window', async () => {
    const router = await setup()
    const current = fixture(1, 100, true)
    current.messages = Array.from({ length: 50 }, (_, i) => ({ ...current.messages[0], id: i + 51 }))
    mocks.detail.mockResolvedValueOnce(current)
    await workspace.refresh(true)
    const pendingOlder = deferred<TicketDetail>()
    mocks.detail.mockReturnValueOnce(pendingOlder.promise)
    const request = workspace.older()
    const latest = fixture(1, 200, true)
    latest.messages = Array.from({ length: 50 }, (_, i) => ({ ...latest.messages[0], id: i + 151 }))
    mocks.detail.mockResolvedValueOnce(latest)
    await workspace.refresh(true)
    const old = fixture(1, 50, false)
    old.messages = Array.from({ length: 50 }, (_, i) => ({ ...old.messages[0], id: i + 1 }))
    pendingOlder.resolve(old)
    await request
    expect(workspace.detail.value?.messages).toHaveLength(50)
    expect(workspace.detail.value?.messages[0].id).toBe(151)
    expect(workspace.detail.value?.has_more).toBe(true)
    expect(router.currentRoute.value.path).toBe('/tickets/1')
  })
  it('does not implicitly mark unseen messages as read and only acknowledges the displayed cursor', async () => {
    await setup()
    expect(mocks.read).not.toHaveBeenCalled()
    await workspace.markRead()
    expect(mocks.read).toHaveBeenCalledWith(1, 1)
    await workspace.markRead()
    expect(mocks.read).toHaveBeenCalledTimes(1)
  })
  it('does not stitch a gap into the timeline when more than a page arrived', async () => {
    await setup()
    mocks.detail.mockResolvedValueOnce(fixture(1, 100, true))
    await workspace.refresh(true)
    expect(workspace.detail.value?.messages.map(m => m.id)).toEqual([100])
    expect(workspace.detail.value?.has_more).toBe(true)
  })
  it('preserves older history and requester metadata when refreshing overlapping messages', async () => {
    await setup()
    const next = fixture(1)
    next.requester = { id: 9, username: 'Test', email: 'test@example.test', balance: 12.5, status: 'active', created_at: '' }
    mocks.detail.mockResolvedValueOnce(next)
    await workspace.refresh(true)
    expect(workspace.detail.value?.requester?.balance).toBe(12.5)
    expect(workspace.detail.value?.messages).toHaveLength(1)
  })
})
