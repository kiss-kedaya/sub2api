import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import UDropdownMenu from '@nuxt/ui/components/DropdownMenu.vue'
import AccountActionMenu from '../AccountActionMenu.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const account = {
  id: 1,
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  rate_limit_reset_at: '2099-01-01T00:00:00Z'
} as Account

// The menu is a UDropdownMenu teleported to <body>; floating-ui places it against the
// trigger rect (virtual reference), flips it above near the viewport bottom and keeps it
// inside an 8px margin. Specs assert that configuration plus the rendered menu.
const getMenu = () => document.body.querySelector<HTMLElement>('.action-menu-content')
const menuItems = () => Array.from(document.body.querySelectorAll<HTMLElement>('.action-menu-content [role="menuitem"]'))

async function mountMenu(anchorRect = new DOMRect(950, 720, 32, 24)) {
  const wrapper = mount(AccountActionMenu, {
    attachTo: document.body,
    props: { show: true, account, anchorRect }
  })
  await flushPromises()
  return wrapper
}

const contentOf = (wrapper: Awaited<ReturnType<typeof mountMenu>>) =>
  wrapper.getComponent(UDropdownMenu).props('content') as {
    side: string
    align: string
    sideOffset: number
    collisionPadding: number
    avoidCollisions: boolean
    reference?: { getBoundingClientRect: () => DOMRect }
  }

enableAutoUnmount(afterEach)

describe('AccountActionMenu viewport positioning', () => {
  it('anchors a long OAuth menu to the trigger of the last row', async () => {
    const wrapper = await mountMenu()

    const content = contentOf(wrapper)
    expect(content.reference?.getBoundingClientRect()).toEqual(new DOMRect(950, 720, 32, 24))
    expect(getMenu()).not.toBeNull()
    expect(getMenu()!.textContent).toContain('admin.accounts.recoverState')
  })

  it('opens below the trigger with right edges aligned', async () => {
    const wrapper = await mountMenu(new DOMRect(500, 100, 32, 24))

    expect(contentOf(wrapper)).toMatchObject({ side: 'bottom', align: 'end', sideOffset: 4 })
  })

  it('flips above and stays inside an 8px viewport margin near the edges', async () => {
    const wrapper = await mountMenu(new DOMRect(179, 550, 32, 24))

    expect(contentOf(wrapper)).toMatchObject({ avoidCollisions: true, collisionPadding: 8 })
  })

  it.each([0, 370])('keeps the same collision margin for a trigger at x=%i', async (x) => {
    const wrapper = await mountMenu(new DOMRect(x, 100, 24, 24))

    expect(contentOf(wrapper).collisionPadding).toBe(8)
    expect(contentOf(wrapper).reference?.getBoundingClientRect().x).toBe(x)
  })

  it('limits an oversized menu to the available height, scrolls it and preserves the recover-state action', async () => {
    const wrapper = await mountMenu(new DOMRect(150, 180, 24, 24))

    const menu = getMenu()!
    expect(menu.className).toContain('max-h-(--reka-dropdown-menu-content-available-height)')
    expect(menu.querySelector('[class*="overflow-y-auto"]')).not.toBeNull()

    const recover = menuItems().find(item => item.textContent?.includes('admin.accounts.recoverState'))!
    recover.click()
    await flushPromises()
    expect(wrapper.emitted('recover-state')).toEqual([[account]])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('follows the trigger when it reopens on another row', async () => {
    const wrapper = await mountMenu()
    await wrapper.setProps({ show: false })
    await flushPromises()
    expect(getMenu()).toBeNull()

    await wrapper.setProps({ show: true, anchorRect: new DOMRect(400, 100, 32, 24) })
    await flushPromises()

    expect(getMenu()).not.toBeNull()
    expect(contentOf(wrapper).reference?.getBoundingClientRect()).toEqual(new DOMRect(400, 100, 32, 24))
  })

  it('still closes on Escape', async () => {
    const wrapper = await mountMenu()
    getMenu()!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()

    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('does not render without an anchor or an account', async () => {
    const wrapper = mount(AccountActionMenu, {
      attachTo: document.body,
      props: { show: true, account: null, anchorRect: new DOMRect(10, 10, 24, 24) }
    })
    await flushPromises()
    expect(getMenu()).toBeNull()

    await wrapper.setProps({ account, anchorRect: null })
    await flushPromises()
    expect(getMenu()).toBeNull()
  })
})
