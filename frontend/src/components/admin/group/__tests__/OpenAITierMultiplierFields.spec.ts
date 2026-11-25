import { defineComponent, h, ref } from 'vue'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import OpenAITierMultiplierFields from '../OpenAITierMultiplierFields.vue'

// 用真实的中文文案，这样断言的就是老板看到的字（含预览句式和错误文案）。
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const { default: zh } = await import('@/i18n/locales/zh')
  const lookup = (key: string) =>
    key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], zh)
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params: Record<string, unknown> = {}) =>
        String(lookup(key) ?? key).replace(/\{(\w+)\}/g, (_match, name) => String(params[name] ?? `{${name}}`)),
    }),
  }
})

enableAutoUnmount(afterEach)

type FieldValue = string | number

// 像 GroupsView 一样用 v-model 绑两个值，才能验证「点芯片/输入 → 回写表单」。
const Harness = defineComponent({
  props: {
    fast: { type: [String, Number], default: '' },
    ultrafast: { type: [String, Number], default: '' },
    groupRate: { type: Number, default: 0.16 },
    freeFast: { type: Boolean, default: false },
  },
  setup(props) {
    const fastModel = ref<FieldValue>(props.fast)
    const ultrafastModel = ref<FieldValue>(props.ultrafast)
    return { fastModel, ultrafastModel, props }
  },
  render() {
    return h(OpenAITierMultiplierFields, {
      idPrefix: 'create',
      groupRate: this.props.groupRate,
      freeFast: this.props.freeFast,
      fast: this.fastModel,
      'onUpdate:fast': (value: FieldValue) => { this.fastModel = value },
      ultrafast: this.ultrafastModel,
      'onUpdate:ultrafast': (value: FieldValue) => { this.ultrafastModel = value },
    })
  },
})

// UButton 渲染成 ULink，需要有 router 可注入，否则每个按钮都刷一条 Vue warn。
async function render(props: Record<string, unknown> = {}) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { render: () => null } }] })
  await router.push('/')
  return mount(Harness, { props, global: { plugins: [router] } })
}
type Wrapper = Awaited<ReturnType<typeof render>>

const fastInput = (wrapper: Wrapper) => wrapper.get('input[data-testid="create-openai-fast-multiplier"]')
const ultrafastInput = (wrapper: Wrapper) => wrapper.get('input[data-testid="create-openai-ultrafast-multiplier"]')
const preview = (wrapper: Wrapper, tier: 'fast' | 'ultrafast') =>
  wrapper.find(`[data-testid="create-openai-${tier}-multiplier-preview"]`)
const inputValue = (input: ReturnType<typeof fastInput>) => (input.element as HTMLInputElement).value

describe('OpenAITierMultiplierFields', () => {
  it('renders both inputs with the official default as placeholder and a one-sentence hint', async () => {
    const wrapper = await render()

    expect(wrapper.text()).toContain('Fast 档位倍率')
    expect(wrapper.text()).toContain('UltraFast 档位倍率')
    expect(wrapper.text()).toContain('叠在分组倍率上；留空 = 官方默认（通常 2×，GPT-5.5 为 2.5×）；填 1 = 不加价。')
    expect(wrapper.text()).toContain('叠在分组倍率上；留空 = 官方默认（6×）；填 1 = 不加价。')
    expect(fastInput(wrapper).attributes('type')).toBe('number')
    expect(fastInput(wrapper).attributes('placeholder')).toBe('默认 2')
    expect(ultrafastInput(wrapper).attributes('placeholder')).toBe('默认 6')
    // 数字用等宽数字，预览和输入框都一样
    expect(fastInput(wrapper).classes()).toContain('tabular-nums')
    // 没填 + 没错误：不应该出现红色错误
    expect(fastInput(wrapper).attributes('aria-invalid')).toBe('false')
    expect(wrapper.find('[data-slot="error"]').exists()).toBe(false)
  })

  it('previews group rate x tier multiplier live, and says "official" when blank', async () => {
    const wrapper = await render()
    expect(preview(wrapper, 'fast').text()).toBe('Fast 请求按 分组倍率 0.16 × 官方档位倍率 计费')
    expect(preview(wrapper, 'ultrafast').text()).toBe('UltraFast 请求按 分组倍率 0.16 × 官方档位倍率 计费')

    await fastInput(wrapper).setValue('2')
    expect(preview(wrapper, 'fast').text()).toBe('Fast 请求按 分组倍率 0.16 × 档位倍率 2 = 0.32× 计费')
    expect(preview(wrapper, 'fast').classes()).toContain('tabular-nums')

    await ultrafastInput(wrapper).setValue('6')
    expect(preview(wrapper, 'ultrafast').text()).toBe('UltraFast 请求按 分组倍率 0.16 × 档位倍率 6 = 0.96× 计费')

    await fastInput(wrapper).setValue('')
    expect(preview(wrapper, 'fast').text()).toBe('Fast 请求按 分组倍率 0.16 × 官方档位倍率 计费')
  })

  it('shows float-safe products (0.1 x 3 = 0.3, not 0.30000000000000004)', async () => {
    const wrapper = await render({ groupRate: 0.1, fast: 3 })
    expect(preview(wrapper, 'fast').text()).toContain('= 0.3× 计费')
  })

  it.each([
    ['0', '必须大于 0；想免费请填 1'],
    ['-1', '必须大于 0；想免费请填 1'],
    ['101', '不能超过 100'],
    ['1.23456', '最多 4 位小数'],
  ])('marks %s red with an error message and hides the preview', async (value, message) => {
    const wrapper = await render()
    await fastInput(wrapper).setValue(value)

    expect(fastInput(wrapper).attributes('aria-invalid')).toBe('true')
    expect(fastInput(wrapper).attributes('aria-describedby')).toContain('-error')
    expect(wrapper.get('[data-slot="error"]').text()).toBe(message)
    expect(preview(wrapper, 'fast').exists()).toBe(false)
    // 另一个输入框不受影响
    expect(ultrafastInput(wrapper).attributes('aria-invalid')).toBe('false')
    expect(preview(wrapper, 'ultrafast').exists()).toBe(true)

    await fastInput(wrapper).setValue('2')
    expect(fastInput(wrapper).attributes('aria-invalid')).toBe('false')
    expect(wrapper.find('[data-slot="error"]').exists()).toBe(false)
  })

  it('treats an empty field as clear, not as an error', async () => {
    const wrapper = await render({ fast: 3 })
    await fastInput(wrapper).setValue('')
    expect(fastInput(wrapper).attributes('aria-invalid')).toBe('false')
    expect(wrapper.find('[data-slot="error"]').exists()).toBe(false)
  })

  it('locks Fast at 1x while free Fast is on, and leaves UltraFast alone', async () => {
    // 锁定时被藏起来的旧值不参与校验，也不能让它挡住保存
    const wrapper = await render({ freeFast: true, fast: 101 })

    const fast = fastInput(wrapper)
    expect(fast.attributes('disabled')).toBeDefined()
    expect(inputValue(fast)).toBe('1×（免费 Fast 已开启）')
    expect(fast.attributes('aria-invalid')).toBe('false')
    expect(preview(wrapper, 'fast').text()).toBe('Fast 请求按 分组倍率 0.16 × 档位倍率 1 = 0.16× 计费')
    for (const chip of ['default', 'free']) {
      expect(wrapper.get(`[data-testid="create-openai-fast-multiplier-${chip}"]`).attributes('disabled')).toBeDefined()
    }

    expect(ultrafastInput(wrapper).attributes('disabled')).toBeUndefined()
    expect(ultrafastInput(wrapper).attributes('type')).toBe('number')
    expect(wrapper.get('[data-testid="create-openai-ultrafast-multiplier-free"]').attributes('disabled')).toBeUndefined()
  })

  it('unlocks Fast again when free Fast is switched off, with the previous value back', async () => {
    const wrapper = await render({ freeFast: true, fast: 3 })
    expect(inputValue(fastInput(wrapper))).toBe('1×（免费 Fast 已开启）')

    await wrapper.setProps({ freeFast: false })
    expect(fastInput(wrapper).attributes('disabled')).toBeUndefined()
    expect(fastInput(wrapper).attributes('type')).toBe('number')
    expect(inputValue(fastInput(wrapper))).toBe('3')
  })

  it('has "official default" and "free 1x" chips that fill the field on click', async () => {
    const wrapper = await render({ fast: 3, ultrafast: 6 })
    const fastDefault = wrapper.get('[data-testid="create-openai-fast-multiplier-default"]')
    const fastFree = wrapper.get('[data-testid="create-openai-fast-multiplier-free"]')

    // 真按钮、不抢表单提交；读屏能听到是哪个字段、做什么、现在选没选中
    for (const chip of [fastDefault, fastFree]) {
      expect(chip.element.tagName).toBe('BUTTON')
      expect(chip.attributes('type')).toBe('button')
    }
    expect(fastDefault.text()).toBe('官方默认')
    expect(fastFree.text()).toBe('免费 1×')
    expect(fastDefault.attributes('aria-label')).toBe('将 Fast 档位倍率 清空，使用官方默认')
    expect(fastFree.attributes('aria-label')).toBe('将 Fast 档位倍率 设为 1×，不加价')
    expect(fastDefault.attributes('aria-pressed')).toBe('false')
    expect(fastFree.attributes('aria-pressed')).toBe('false')

    await fastFree.trigger('click')
    expect(inputValue(fastInput(wrapper))).toBe('1')
    expect(fastFree.attributes('aria-pressed')).toBe('true')
    expect(fastDefault.attributes('aria-pressed')).toBe('false')
    expect(preview(wrapper, 'fast').text()).toBe('Fast 请求按 分组倍率 0.16 × 档位倍率 1 = 0.16× 计费')
    // 只动 Fast，不碰 UltraFast
    expect(inputValue(ultrafastInput(wrapper))).toBe('6')

    await fastDefault.trigger('click')
    expect(inputValue(fastInput(wrapper))).toBe('')
    expect(fastDefault.attributes('aria-pressed')).toBe('true')
    expect(fastFree.attributes('aria-pressed')).toBe('false')

    await wrapper.get('[data-testid="create-openai-ultrafast-multiplier-free"]').trigger('click')
    expect(inputValue(ultrafastInput(wrapper))).toBe('1')
  })

  it('a chip also repairs an invalid value', async () => {
    const wrapper = await render({ ultrafast: 500 })
    expect(ultrafastInput(wrapper).attributes('aria-invalid')).toBe('true')

    await wrapper.get('[data-testid="create-openai-ultrafast-multiplier-default"]').trigger('click')
    expect(ultrafastInput(wrapper).attributes('aria-invalid')).toBe('false')
    expect(preview(wrapper, 'ultrafast').exists()).toBe(true)
  })

  it('only fades colors (150ms ease-out) and respects reduced motion', async () => {
    const wrapper = await render()
    const chip = wrapper.get('[data-testid="create-openai-fast-multiplier-default"]')
    expect(chip.classes()).toEqual(expect.arrayContaining([
      'transition-colors', 'duration-150', 'ease-out', 'motion-reduce:transition-none',
    ]))
  })
})
