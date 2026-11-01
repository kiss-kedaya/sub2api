import { afterEach, describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import HelpTooltip from '@/components/common/HelpTooltip.vue'

// The tooltip renders through a Nuxt UI popover: it is mounted only while open (closed = absent).
function findTooltip(): HTMLDivElement | null {
  const tooltip = document.body.querySelector('[role="tooltip"]')
  return tooltip instanceof HTMLDivElement ? tooltip : null
}

function getTooltipElement(): HTMLDivElement {
  const tooltip = findTooltip()
  if (!tooltip) {
    throw new Error('tooltip element not found')
  }
  return tooltip
}

const isOpen = () => findTooltip() !== null

describe('HelpTooltip', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('keeps the existing hover interaction by default', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'hover details',
      },
    })

    const trigger = wrapper.get('.group')

    expect(isOpen()).toBe(false)

    await trigger.trigger('mouseenter')
    await flushPromises()
    expect(isOpen()).toBe(true)
    expect(getTooltipElement().textContent).toContain('hover details')

    await trigger.trigger('mouseleave')
    await flushPromises()
    expect(isOpen()).toBe(false)

    wrapper.unmount()
  })

  it('keeps a hover tooltip open while the pointer moves between the trigger and the tooltip', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'copyable details',
      },
    })

    const trigger = wrapper.get('.group')

    await trigger.trigger('mouseenter')
    await flushPromises()
    const tooltip = getTooltipElement()
    expect(isOpen()).toBe(true)

    await trigger.trigger('mouseleave', { relatedTarget: tooltip })
    await flushPromises()
    expect(isOpen()).toBe(true)

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: trigger.element }))
    await flushPromises()
    expect(isOpen()).toBe(true)

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: null }))
    await flushPromises()
    expect(isOpen()).toBe(false)

    wrapper.unmount()
  })

  it('supports click-to-toggle details and closes on outside click', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'click details',
        trigger: 'click',
      },
    })

    const trigger = wrapper.get('.group')

    expect(isOpen()).toBe(false)

    await trigger.trigger('click')
    await flushPromises()
    expect(isOpen()).toBe(true)
    expect(getTooltipElement().textContent).toContain('click details')

    const closeButton = getTooltipElement().querySelector('button[aria-label="Close"]')
    if (!(closeButton instanceof HTMLButtonElement)) {
      throw new Error('close button not found')
    }
    closeButton.click()
    await flushPromises()
    expect(isOpen()).toBe(false)

    await trigger.trigger('click')
    await flushPromises()
    expect(isOpen()).toBe(true)

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(isOpen()).toBe(false)

    wrapper.unmount()
  })

  it('closes a click tooltip on Escape and when the trigger is clicked again', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: { content: 'esc details', trigger: 'click' },
    })
    const trigger = wrapper.get('.group')

    await trigger.trigger('click')
    await flushPromises()
    expect(isOpen()).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(isOpen()).toBe(false)

    await trigger.trigger('click')
    await flushPromises()
    expect(isOpen()).toBe(true)
    await trigger.trigger('click')
    await flushPromises()
    expect(isOpen()).toBe(false)

    wrapper.unmount()
  })
})
