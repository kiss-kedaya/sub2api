import { afterEach, describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import HintFormField from '@/components/common/HintFormField.vue'

// HintFormField wraps UFormField and moves the explanatory copy into a HelpTooltip bubble beside the label.
// The bubble teleports to <body>, so open it through the trigger and read it from there.
function mountField(props: Record<string, unknown> = {}) {
  return mount(HintFormField, {
    attachTo: document.body,
    props: { label: 'admin.label', hint: 'admin.hint', ...props },
    slots: { default: '<input data-test="field" />' },
  })
}

const trigger = (wrapper: ReturnType<typeof mountField>) => wrapper.get('[data-hint-trigger]')
// mouseenter / mouseleave do not bubble: the handlers sit on the wrapping .group element.
const hoverTarget = (wrapper: ReturnType<typeof mountField>) => wrapper.get('.group')
const bubble = () => document.body.querySelector<HTMLElement>('[role="tooltip"]')
const bubbleText = () => bubble()?.textContent ?? ''

afterEach(() => {
  document.body.innerHTML = ''
})

describe('HintFormField', () => {
  it('renders the label but keeps the hint out of the permanent page copy', () => {
    const wrapper = mountField()

    expect(wrapper.text()).toContain('admin.label')
    expect(wrapper.text()).not.toContain('admin.hint')
    expect(bubble()).toBeNull()

    wrapper.unmount()
  })

  it('opens on keyboard focus and closes on Escape', async () => {
    const wrapper = mountField()

    await trigger(wrapper).trigger('focusin')
    await flushPromises()
    expect(bubbleText()).toContain('admin.hint')

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(bubble()).toBeNull()

    wrapper.unmount()
  })

  it('opens on click for touch and pointer users', async () => {
    const wrapper = mountField()

    await trigger(wrapper).trigger('click')
    await flushPromises()
    expect(bubbleText()).toContain('admin.hint')
    expect(trigger(wrapper).attributes('aria-expanded')).toBe('true')

    // A second click closes it again.
    await trigger(wrapper).trigger('click')
    await flushPromises()
    expect(bubble()).toBeNull()

    wrapper.unmount()
  })

  it('closes when the pointer leaves a hover-opened bubble that was never pinned', async () => {
    const wrapper = mountField()

    await hoverTarget(wrapper).trigger('mouseenter')
    await flushPromises()
    expect(bubbleText()).toContain('admin.hint')

    await hoverTarget(wrapper).trigger('mouseleave', { relatedTarget: null })
    await flushPromises()
    expect(bubble()).toBeNull()

    wrapper.unmount()
  })

  it('keeps a hover-opened bubble open once it has been clicked', async () => {
    const wrapper = mountField()

    await hoverTarget(wrapper).trigger('mouseenter')
    await flushPromises()
    await trigger(wrapper).trigger('click')
    await flushPromises()

    await hoverTarget(wrapper).trigger('mouseleave', { relatedTarget: null })
    await flushPromises()
    expect(bubbleText()).toContain('admin.hint')

    wrapper.unmount()
  })

  it('renders a short hint inline when keep-hint-visible is set', () => {
    const wrapper = mountField({ keepHintVisible: true })

    expect(wrapper.text()).toContain('admin.hint')
    expect(wrapper.find('[data-hint-trigger]').exists()).toBe(false)

    wrapper.unmount()
  })

  it('lets a caller replace the label slot entirely', () => {
    const wrapper = mount(HintFormField, {
      attachTo: document.body,
      props: { label: 'ignored', hint: 'admin.hint' },
      slots: { label: '<span data-test="custom-label">custom</span>' },
    })

    // A caller-owned label slot wins outright: the built-in label + bubble markup is not rendered.
    expect(wrapper.text()).toContain('custom')
    expect(wrapper.text()).not.toContain('ignored')

    wrapper.unmount()
  })
})
