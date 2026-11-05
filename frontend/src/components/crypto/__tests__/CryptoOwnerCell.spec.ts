import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import CryptoOwnerCell from '../CryptoOwnerCell.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

// Fixture identities only.
const owner = (over: Record<string, unknown> = {}) => ({
  user_id: 42, username: 'fixture-name', email: 'fixture-user@example.test', deleted: false, ...over
})
const render = (value: unknown) => mount(CryptoOwnerCell, { props: { owner: value as never } })
const text = (wrapper: ReturnType<typeof render>, id: string) => wrapper.find(`[data-testid="${id}"]`)

describe('CryptoOwnerCell fallback chain', () => {
  it.each([[undefined], [null]])('shows a dash when the owner is unknown (%s)', value => {
    const wrapper = render(value)
    expect(wrapper.text()).toBe('—')
    expect(text(wrapper, 'crypto-owner').exists()).toBe(false)
  })

  it('shows the username as the main text, with the email and #id beneath', () => {
    const wrapper = render(owner())
    expect(text(wrapper, 'crypto-owner-primary').text()).toBe('fixture-name')
    expect(text(wrapper, 'crypto-owner-email').text()).toBe('fixture-user@example.test')
    expect(text(wrapper, 'crypto-owner-id').text()).toBe('#42')
    expect(text(wrapper, 'crypto-owner-deleted').exists()).toBe(false)
  })

  it('falls back to the email when the username is empty, and keeps #id on the small line', () => {
    const wrapper = render(owner({ username: '' }))
    expect(text(wrapper, 'crypto-owner-primary').text()).toBe('fixture-user@example.test')
    expect(text(wrapper, 'crypto-owner-email').exists()).toBe(false) // not shown twice
    expect(text(wrapper, 'crypto-owner-id').text()).toBe('#42')
  })

  it('treats a blank username as empty', () => {
    const wrapper = render(owner({ username: '   ' }))
    expect(text(wrapper, 'crypto-owner-primary').text()).toBe('fixture-user@example.test')
  })

  it('falls back to #id when there is neither username nor email, without a second copy', () => {
    const wrapper = render(owner({ username: '', email: '' }))
    expect(text(wrapper, 'crypto-owner-primary').text()).toBe('#42')
    expect(text(wrapper, 'crypto-owner-secondary').exists()).toBe(false)
    expect(wrapper.text().match(/#42/g)).toHaveLength(1)
  })

  it('marks a deleted user and still names them', () => {
    const named = render(owner({ deleted: true }))
    expect(text(named, 'crypto-owner-primary').text()).toBe('fixture-name')
    expect(text(named, 'crypto-owner-deleted').text()).toBe('admin.crypto.sweepView.ownerDeleted')

    const bare = render(owner({ deleted: true, username: '', email: '' }))
    expect(text(bare, 'crypto-owner-primary').text()).toBe('#42')
    expect(text(bare, 'crypto-owner-deleted').exists()).toBe(true)
  })

  it('truncates a long email instead of widening the table, and keeps the full text on hover', () => {
    const long = 'a-very-long-fixture-mailbox-name.with.many.parts.and.more@subdomain.example-long-domain.test'
    const wrapper = render(owner({ email: long }))
    const email = text(wrapper, 'crypto-owner-email')
    expect(email.text()).toBe(long)
    expect(email.attributes('title')).toBe(long)
    expect(email.classes()).toContain('truncate')
    expect(text(wrapper, 'crypto-owner').classes()).toEqual(expect.arrayContaining(['max-w-40', 'min-w-0']))
    // The id sits beside it and is never the part that gets cut.
    expect(text(wrapper, 'crypto-owner-id').classes()).toContain('shrink-0')
  })

  it('truncates a long username and a long email-as-name the same way', () => {
    const long = 'x'.repeat(120) + '@example.test'
    const wrapper = render(owner({ username: '', email: long }))
    const primary = text(wrapper, 'crypto-owner-primary')
    expect(primary.classes()).toContain('truncate')
    expect(primary.attributes('title')).toBe(long)
  })

  it('is display only: no links, buttons or inputs', () => {
    const wrapper = render(owner())
    expect(wrapper.find('a, button, input').exists()).toBe(false)
  })
})
