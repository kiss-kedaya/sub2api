import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PlazaModelPricingTable from '../PlazaModelPricingTable.vue'
import type { PlazaModel } from '@/api/modelPlaza'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

function tokenModel(overrides: Partial<PlazaModel> = {}): PlazaModel {
  return {
    name: 'claude-sonnet',
    platform: 'anthropic',
    pricing: {
      billing_mode: 'token',
      input_price: 3e-6,
      output_price: 1.5e-5,
      cache_write_price: 3.75e-6,
      cache_read_price: 3e-7,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: []
    },
    official_pricing: {
      input_price: 3e-6,
      output_price: 1.5e-5,
      cache_write_price: 3.75e-6,
      cache_write_1h_price: 6e-6,
      cache_read_price: 3e-7
    },
    ...overrides
  }
}

function mountTable(
  models: PlazaModel[],
  rateMultiplier: number,
  userRateMultiplier?: number | null,
  extraProps?: {
    imageRateIndependent?: boolean
    imageRateMultiplier?: number | null
    peakWindow?: string
    peakRateMultiplier?: number | null
  }
) {
  return mount(PlazaModelPricingTable, {
    props: { models, rateMultiplier, userRateMultiplier: userRateMultiplier ?? null, ...extraProps }
  })
}

describe('PlazaModelPricingTable', () => {
  it('倍率为 1 时展示渠道单价原值($/1M),价格保底 2 位小数', () => {
    const wrapper = mountTable([tokenModel()], 1)
    const text = wrapper.text()
    expect(text).toContain('$3.00')
    expect(text).toContain('$15.00')
    // 缓存写 / 读(超过 2 位小数原样保留)
    expect(text).toContain('$3.75')
    expect(text).toContain('$0.30')
    // 倍率列
    expect(text).toContain('1x')
  })

  it('展示上下文/最大输出与能力芯片', () => {
    const model = tokenModel({
      context_window: 1048576,
      max_output_tokens: 393216,
      input_modalities: ['text', 'image'],
      reasoning: true,
      tool_call: true
    })
    const wrapper = mountTable([model], 1)
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.table.contextLabel')
    expect(text).toContain('1M')
    expect(text).toContain('modelPlaza.table.maxOutputLabel')
    expect(text).toContain('384K')
    expect(text).toContain('modelPlaza.table.reasoning')
    expect(text).toContain('modelPlaza.table.imageInput')
    expect(text).toContain('modelPlaza.table.toolCall')
    expect(text).not.toContain('modelPlaza.table.videoInput')
  })

  it('简介默认收起，点击展开再收起', async () => {
    const wrapper = mountTable([tokenModel({ description: 'A long-context model.' })], 1)
    expect(wrapper.text()).not.toContain('A long-context model.')
    const toggle = wrapper.find('.plaza-meta-toggle')
    expect(toggle.exists()).toBe(true)
    expect(toggle.attributes('aria-expanded')).toBe('false')

    await toggle.trigger('click')
    expect(wrapper.text()).toContain('A long-context model.')
    expect(toggle.attributes('aria-expanded')).toBe('true')

    await toggle.trigger('click')
    expect(wrapper.text()).not.toContain('A long-context model.')
    expect(toggle.attributes('aria-expanded')).toBe('false')
  })

  it('没有元数据时不渲染元数据行（旧后端兼容）', () => {
    const wrapper = mountTable([tokenModel()], 1)
    expect(wrapper.find('.plaza-meta-toggle').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('modelPlaza.table.contextLabel')
    expect(wrapper.text()).not.toContain('modelPlaza.table.reasoning')
  })

  it('shows the Max reasoning billing multiplier', () => {
    const model = tokenModel()
    model.pricing!.max_reasoning_effort_multiplier = 3
    const wrapper = mountTable([model], 1)

    expect(wrapper.text()).toContain('modelPlaza.table.maxReasoningMultiplierBadge')
    expect(wrapper.find('[title="modelPlaza.table.maxReasoningMultiplierHint"]').exists()).toBe(true)
  })

  it('倍率 ≠ 1 时价格列为折后实付价,官方价列保持原价', () => {
    const wrapper = mountTable([tokenModel()], 0.5)
    const text = wrapper.text()
    // 实付 = 3 × 0.5 / 15 × 0.5
    expect(text).toContain('$1.50')
    expect(text).toContain('$7.50')
    // 官方价原值仍在(官方列不乘倍率)
    expect(text).toContain('$3.00')
    expect(text).toContain('$15.00')
    expect(text).toContain('0.5x')
  })

  it('分组自定义价卡生效时参考价列显示自定义价并带标记', () => {
    const model = tokenModel({
      official_pricing: {
        input_price: 1e-6,
        output_price: 5e-6,
        cache_write_price: 1.25e-6,
        cache_write_1h_price: null,
        cache_read_price: 1e-7,
        custom: true
      }
    })
    const wrapper = mountTable([model], 1)
    const text = wrapper.text()
    // 参考价列使用后端返回的自定义起步价,而不是目录官方价
    expect(text).toContain('$1.00')
    expect(text).toContain('$5.00')
    expect(text).toContain('modelPlaza.table.customReference')
    expect(wrapper.find('[title="modelPlaza.table.customReferenceHint"]').exists()).toBe(true)
  })

  it('用户专属倍率覆盖分组倍率,并划线展示原倍率', () => {
    const wrapper = mountTable([tokenModel()], 1, 0.8)
    const text = wrapper.text()
    // 实付按 0.8:3 × 0.8 = 2.4
    expect(text).toContain('$2.40')
    expect(text).toContain('$12.00')
    // 倍率列:原倍率划线 + 专属倍率
    const struck = wrapper.find('td .line-through')
    expect(struck.exists()).toBe(true)
    expect(struck.text()).toBe('1x')
    expect(text).toContain('0.8x')
  })

  it('模型按官方输出价从高到低排序,无官方价的排最后', () => {
    const expensive = tokenModel({
      name: 'model-expensive',
      official_pricing: {
        input_price: 1e-5,
        output_price: 7.5e-5,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: null
      }
    })
    const cheap = tokenModel({
      name: 'model-cheap',
      official_pricing: {
        input_price: 1e-6,
        output_price: 5e-6,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: null
      }
    })
    const noOfficial = tokenModel({ name: 'model-no-official', official_pricing: null })

    const wrapper = mountTable([cheap, noOfficial, expensive], 1)
    const names = wrapper.findAll('tbody tr').map((tr) => tr.find('td').text())
    expect(names).toEqual(['model-expensive', 'model-cheap', 'model-no-official'])
  })

  it('官方输出价相同时按模型名降序(新版本号在前)', () => {
    const older = tokenModel({ name: 'gpt-5.5' })
    const newer = tokenModel({ name: 'gpt-5.6-sol' })

    const wrapper = mountTable([older, newer], 1)
    const names = wrapper.findAll('tbody tr').map((tr) => tr.find('td').text())
    expect(names).toEqual(['gpt-5.6-sol', 'gpt-5.5'])
  })

  it('按图片/按次计费的模型沉到末尾,不与 token 模型按官方价混排', () => {
    // 官方输出价 $10,介于下面两个 token 模型之间,但因计费模式不同应排最后
    const image = tokenModel({
      name: 'gpt-image-2',
      pricing: {
        billing_mode: 'image',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: 0.002,
        intervals: []
      },
      official_pricing: {
        input_price: 5e-6,
        output_price: 1e-5,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: 1.25e-6
      }
    })
    const pricier = tokenModel({
      name: 'gpt-5.6-terra',
      official_pricing: {
        input_price: 2.5e-6,
        output_price: 1.5e-5,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: null
      }
    })
    const cheaper = tokenModel({
      name: 'gpt-5.6-luna',
      official_pricing: {
        input_price: 1e-6,
        output_price: 6e-6,
        cache_write_price: null,
        cache_write_1h_price: null,
        cache_read_price: null
      }
    })

    const wrapper = mountTable([pricier, image, cheaper], 1)
    const names = wrapper.findAll('tbody tr').map((tr) => tr.find('td').text())
    expect(names[0]).toBe('gpt-5.6-terra')
    expect(names[1]).toBe('gpt-5.6-luna')
    // 首列含「按图片计费」徽章文本,只断言模型名
    expect(names[2]).toContain('gpt-image-2')
  })

  it('两级表头:实付区与官方区各拆输入/输出/缓存列', () => {
    const wrapper = mountTable([tokenModel()], 1)
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.table.paidPrice')
    expect(text).toContain('modelPlaza.table.officialPrice')
    // token 行:模型 + 实付 3 列 + 官方 3 列 + 倍率
    expect(wrapper.findAll('tbody td')).toHaveLength(8)
  })

  it('官方价包含 1h 缓存写入价;official_pricing 为 null 时官方三列显示 -', () => {
    const withOfficial = mountTable([tokenModel()], 1)
    expect(withOfficial.text()).toContain('$6.00')
    expect(withOfficial.text()).toContain('(1h')

    const withoutOfficial = mountTable([tokenModel({ official_pricing: null })], 1)
    const cells = withoutOfficial.findAll('tbody td')
    // 官方 输入/输出/缓存 三列均为 -
    expect(cells[4].text().trim()).toBe('-')
    expect(cells[5].text().trim()).toBe('-')
    expect(cells[6].text().trim()).toBe('-')
  })

  it('实付价分别展示自定义 5m 与 1h 缓存写入价', () => {
    const model = tokenModel()
    model.pricing!.cache_write_1h_price = 7e-6

    const wrapper = mountTable([model], 1)
    expect(wrapper.text()).toContain('$3.75')
    expect(wrapper.text()).toContain('$7.00')
    expect(wrapper.text()).toContain('(1h')
  })

  it('per_request 模型按单次价 × 倍率展示,官方价列显示 -', () => {
    const model = tokenModel({
      name: 'search-tool',
      pricing: {
        billing_mode: 'per_request',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: 0.04,
        intervals: []
      },
      official_pricing: null
    })
    const wrapper = mountTable([model], 0.5)
    const text = wrapper.text()
    // 0.04 × 0.5 = 0.02,scale=1
    expect(text).toContain('$0.02')
    expect(text).toContain('modelPlaza.table.perRequest')
    // 单位后缀跟在价格后(按次 → / 次)
    expect(text).toContain('modelPlaza.table.perUnitRequest')
  })

  it('token 模型阶梯定价内联进输入/输出列,按倍率折算', () => {
    const model = tokenModel({
      pricing: {
        billing_mode: 'token',
        input_price: 3e-6,
        output_price: 1.5e-5,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: null,
        intervals: [
          {
            min_tokens: 0,
            max_tokens: 200000,
            tier_label: '',
            input_price: 3e-6,
            output_price: 1.5e-5,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: null
          },
          {
            min_tokens: 200000,
            max_tokens: null,
            tier_label: '',
            input_price: 6e-6,
            output_price: 3e-5,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: null
          }
        ]
      }
    })
    const wrapper = mountTable([model], 0.5)
    const text = wrapper.text()
    // 区间标签按 token 数生成
    expect(text).toContain('≤200K')
    expect(text).toContain('>200K')
    // 折后:输入 1.5 / 3,输出 7.5 / 15
    expect(text).toContain('$1.50')
    expect(text).toContain('$7.50')
    expect(text).toContain('$15.00')
  })

  it('仅配置区间倍率时按基础价格展示 input/output/cache 各档价格', () => {
    const model = tokenModel({
      pricing: {
        billing_mode: 'token',
        input_price: 10e-6,
        output_price: 50e-6,
        cache_write_price: 12.5e-6,
        cache_write_1h_price: 12.5e-6,
        cache_read_price: 2e-6,
        image_input_price: null,
        image_output_price: null,
        per_request_price: null,
        intervals: [{
          min_tokens: 272000,
          max_tokens: null,
          tier_label: '>272K',
          input_price: null,
          output_price: null,
          cache_write_price: null,
          cache_write_1h_price: null,
          cache_read_price: null,
          input_multiplier: 2,
          output_multiplier: 1.5,
          cache_write_multiplier: 2,
          cache_read_multiplier: 2,
          per_request_price: null
        }]
      },
      official_pricing: null
    })

    const text = mountTable([model], 1).text()
    expect(text).toContain('$20.00')
    expect(text).toContain('$75.00')
    expect(text).toContain('$25.00')
    expect(text).toContain('$4.00')
    model.pricing!.intervals.unshift({
      ...model.pricing!.intervals[0], min_tokens: 0, max_tokens: 272000,
      tier_label: '<=272K', cache_write_multiplier: null, cache_read_multiplier: null
    })
    const cells = mountTable([model], 1).findAll('tbody td')
    expect(cells[3].text()).toContain('$12.50')
    expect(cells[3].text()).toContain('$2.00')
    expect(cells[3].text()).toContain('$25.00')
    expect(cells[3].text()).toContain('$4.00')
  })

  it('生图独立倍率开启时,按图价格 × 独立倍率,不乘分组倍率;倍率列展示独立倍率', () => {
    const model = tokenModel({
      name: 'gpt-image-2',
      pricing: {
        billing_mode: 'image',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: null,
        intervals: [
          {
            min_tokens: 0,
            max_tokens: null,
            tier_label: '1K',
            input_price: null,
            output_price: null,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: 0.02
          }
        ]
      },
      official_pricing: null
    })
    const wrapper = mountTable([model], 0.1, null, {
      imageRateIndependent: true,
      imageRateMultiplier: 1
    })
    const text = wrapper.text()
    // 0.02 × 1(独立倍率),而非 0.02 × 0.1
    expect(text).toContain('$0.02')
    expect(text).not.toContain('$0.002')
    // 倍率列展示独立倍率 1x,而非分组倍率 0.1x
    const rateCell = wrapper.findAll('tbody tr td').at(-1)!
    expect(rateCell.text()).toBe('1x')
  })

  it('生图独立倍率关闭时,按图价格仍乘分组/专属生效倍率', () => {
    const model = tokenModel({
      name: 'gpt-image-2',
      pricing: {
        billing_mode: 'image',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        image_output_price: null,
        per_request_price: 0.2,
        intervals: []
      },
      official_pricing: null
    })
    const wrapper = mountTable([model], 0.1, null, { imageRateIndependent: false })
    const text = wrapper.text()
    expect(text).toContain('$0.02')
    const rateCell = wrapper.findAll('tbody tr td').at(-1)!
    expect(rateCell.text()).toBe('0.1x')
  })

  it('按图模型主行展示阶梯芯片,不把 image_output_price(每 token)当按次价', () => {
    const model = tokenModel({
      name: 'gpt-image-2',
      pricing: {
        billing_mode: 'image',
        input_price: null,
        output_price: null,
        cache_write_price: null,
        cache_read_price: null,
        image_input_price: null,
        // 每 token 图片输出价:不应被当作按次单价展示
        image_output_price: 3e-5,
        per_request_price: null,
        intervals: [
          {
            min_tokens: 0,
            max_tokens: null,
            tier_label: '1K',
            input_price: null,
            output_price: null,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: 0.01
          },
          {
            min_tokens: 0,
            max_tokens: null,
            tier_label: '2K',
            input_price: null,
            output_price: null,
            cache_write_price: null,
            cache_read_price: null,
            per_request_price: 0.02
          }
        ]
      },
      official_pricing: null
    })
    const wrapper = mountTable([model], 0.1)
    const text = wrapper.text()
    expect(text).toContain('modelPlaza.table.perImage')
    // 芯片:1K $0.001 / 2K $0.002,单位后缀内嵌(按图 → / 张)
    expect(text).toContain('1K')
    expect(text).toContain('$0.001')
    expect(text).toContain('2K')
    expect(text).toContain('$0.002')
    expect(text).toContain('modelPlaza.table.perUnitImage')
    // 旧 bug:image_output_price × 0.1 = 0.000003 被当按次价
    expect(text).not.toContain('$0.000003')
  })

  it('Composite 分组中相同模型名按具体平台分别展示徽章', () => {
    const anthropic = tokenModel({ name: 'shared-model', platform: 'anthropic' })
    const openai = tokenModel({ name: 'shared-model', platform: 'openai' })
    const wrapper = mount(PlazaModelPricingTable, {
      props: {
        models: [anthropic, openai],
        platform: 'composite',
        rateMultiplier: 1
      }
    })

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows.map((row) => row.find('td').text())).toEqual([
      'shared-modelAnthropic',
      'shared-modelOpenAI'
    ])
    expect(wrapper.text()).toContain('Anthropic')
    expect(wrapper.text()).toContain('OpenAI')
  })
})

describe('PlazaModelPricingTable 长上下文阶梯', () => {
  function ladderIntervals() {
    return [
      {
        min_tokens: 0,
        max_tokens: 272000,
        tier_label: '≤272K',
        input_price: 5e-6,
        output_price: 3e-5,
        cache_write_price: 6.25e-6,
        cache_read_price: 5e-7,
        per_request_price: null
      },
      {
        min_tokens: 272000,
        max_tokens: null,
        tier_label: '>272K',
        input_price: 1e-5,
        output_price: 4.5e-5,
        cache_write_price: 1.25e-5,
        cache_read_price: 1e-6,
        per_request_price: null
      }
    ]
  }

  function ladderModel(overrides: Partial<PlazaModel> = {}): PlazaModel {
    return tokenModel({
      name: 'gpt-5.6-sol',
      platform: 'openai',
      pricing: {
        billing_mode: 'token',
        input_price: 5e-6,
        output_price: 3e-5,
        cache_write_price: 6.25e-6,
        cache_read_price: 5e-7,
        image_input_price: null,
        image_output_price: null,
        per_request_price: null,
        intervals: ladderIntervals()
      },
      official_pricing: {
        input_price: 5e-6,
        output_price: 3e-5,
        cache_write_price: 6.25e-6,
        cache_read_price: 5e-7,
        intervals: ladderIntervals()
      },
      long_context_basis: 'whole_request',
      ...overrides
    })
  }

  it('实付缓存列按档分行并乘倍率,每档一行与输入/输出列对齐;档位标签只在输入列', () => {
    const wrapper = mountTable([ladderModel()], 0.5)
    const cells = wrapper.findAll('tbody td')
    const cacheCell = cells[3]
    const rows = cacheCell.findAll('.leading-5')
    expect(rows).toHaveLength(2)
    // 写 6.25 × 0.5 / 读 0.5 × 0.5;高档 12.5 × 0.5 / 1 × 0.5
    expect(rows[0].text()).toContain('modelPlaza.table.cacheWriteShort')
    expect(rows[0].text()).toContain('$3.125')
    expect(rows[0].text()).toContain('$0.25')
    expect(rows[1].text()).toContain('$6.25')
    expect(rows[1].text()).toContain('$0.50')
    // 输入列带标签,输出/缓存列只按行对齐不重复标签
    expect(cells[1].text()).toContain('≤272K')
    expect(cells[1].text()).toContain('>272K')
    expect(cells[2].text()).not.toContain('272K')
    expect(cacheCell.text()).not.toContain('272K')
    expect(cells[1].findAll('.leading-5')).toHaveLength(2)
    expect(cells[2].findAll('.leading-5')).toHaveLength(2)
  })

  it('官方三列按 official_pricing.intervals 分档且不乘倍率,不内联 1h', () => {
    const wrapper = mountTable([ladderModel()], 0.5)
    const cells = wrapper.findAll('tbody td')
    expect(cells[4].text()).toContain('≤272K')
    expect(cells[4].text()).toContain('$5.00')
    expect(cells[4].text()).toContain('>272K')
    expect(cells[4].text()).toContain('$10.00')
    expect(cells[5].text()).toContain('$30.00')
    expect(cells[5].text()).toContain('$45.00')
    expect(cells[6].text()).toContain('$6.25')
    expect(cells[6].text()).toContain('$12.50')
    expect(cells[6].text()).toContain('$1.00')
    expect(cells[6].text()).not.toContain('(1h')
  })

  it('整单计价的档位标签带 tooltip;边际计价在模型名旁加徽章并换用边际说明', () => {
    const whole = mountTable([ladderModel()], 1)
    const wholeLabels = whole.findAll('tbody td span[title="modelPlaza.table.tierHint"]')
    expect(wholeLabels.length).toBeGreaterThan(0)
    expect(whole.text()).not.toContain('modelPlaza.table.marginalBadge')

    const marginal = mountTable([ladderModel({ long_context_basis: 'marginal' })], 1)
    const marginalLabels = marginal.findAll('tbody td span[title="modelPlaza.table.tierHintMarginal"]')
    expect(marginalLabels.length).toBeGreaterThan(0)
    expect(marginal.findAll('tbody td')[0].text()).toContain('modelPlaza.table.marginalBadge')
  })

  it('无标签的多档按区间生成统一形态(≤上限 / >下限),并按下限升序展示', () => {
    const model = ladderModel({
      pricing: {
        ...ladderModel().pricing!,
        // 故意乱序:展示必须按上下文从低到高
        intervals: [
          { ...ladderIntervals()[1], min_tokens: 1000000, tier_label: '' },
          { ...ladderIntervals()[0], min_tokens: 100000, max_tokens: 200000, tier_label: '' },
          { ...ladderIntervals()[0], max_tokens: 100000, tier_label: '' },
          { ...ladderIntervals()[1], min_tokens: 200000, max_tokens: 1000000, tier_label: '' }
        ]
      }
    })
    const rows = mountTable([model], 1).findAll('tbody td')[1].findAll('.leading-5')
    expect(rows.map((r) => r.text().split(/\s+/)[0])).toEqual(['≤100K', '≤200K', '≤1M', '>1M'])
  })

  it('官方无 intervals 字段(旧响应)时官方列保持平价,实付无阶梯时缓存列保持两行', () => {
    const wrapper = mountTable([tokenModel()], 1)
    const cells = wrapper.findAll('tbody td')
    expect(cells[3].text()).toContain('modelPlaza.table.cacheWrite')
    expect(cells[3].text()).toContain('modelPlaza.table.cacheRead')
    expect(cells[3].findAll('.leading-5')).toHaveLength(0)
    expect(cells[6].text()).toContain('(1h')
  })
})

describe('PlazaModelPricingTable 分时计价', () => {
  function timePricedModel() {
    return tokenModel({
      name: 'deepseek-chat',
      platform: 'deepseek',
      time_pricing: {
        timezone: 'Asia/Shanghai',
        periods: [
          { start_time: '00:30', end_time: '08:30:00', multiplier: 0.5 },
          { start_time: '18:00', end_time: '22:00', multiplier: 1.2 }
        ]
      }
    })
  }

  it('有分时倍率的模型仍只有一行:价格与倍率列保持标准口径,时段以芯片挂在模型名下', () => {
    const wrapper = mountTable([timePricedModel()], 0.8)
    const trs = wrapper.findAll('tbody tr')
    expect(trs).toHaveLength(1)

    const cells = trs[0].findAll('td')
    expect(cells[0].text()).toContain('deepseek-chat')
    expect(cells[1].text()).toContain('$2.40')
    expect(cells[7].text()).toBe('0.8x')
    // 官方列不受时段影响
    expect(cells[4].text()).toContain('$3.00')

    // 夜间 0.8 × 0.5 = 0.4x;晚高峰 0.8 × 1.2 = 0.96x;标注时段不含时区
    const text = cells[0].text()
    expect(text).toContain('0.4x')
    expect(text).toContain('00:30–08:30')
    expect(text).toContain('0.96x')
    expect(text).toContain('18:00–22:00')
    expect(text).not.toContain('Asia/Shanghai')
  })

  it('低于标准价的时段用翠绿芯片,高于标准价的用玫红芯片', () => {
    const wrapper = mountTable([timePricedModel()], 1)
    const chips = wrapper.findAll('[title="modelPlaza.table.timePricingHint"]')
    expect(chips).toHaveLength(2)
    expect(chips[0].text()).toContain('0.5x')
    expect(chips[0].classes().join(' ')).toContain('emerald')
    expect(chips[1].text()).toContain('1.2x')
    expect(chips[1].classes().join(' ')).toContain('rose')
  })

  it('倍率相同的多个时段合并成一个芯片', () => {
    const model = tokenModel({
      name: 'deepseek-v4.1-flash',
      platform: 'deepseek',
      time_pricing: {
        timezone: 'Asia/Shanghai',
        weekdays_only: true,
        periods: [
          { start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 },
          { start_time: '14:00:00', end_time: '18:00:00', multiplier: 2 }
        ]
      }
    })
    const wrapper = mountTable([model], 0.3)
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    const chips = wrapper.findAll('[title="modelPlaza.table.timePricingHintWeekdays"]')
    expect(chips).toHaveLength(1)
    expect(chips[0].text()).toContain('0.6x')
    expect(chips[0].text()).toContain('09:00–12:00 · 14:00–18:00')
  })

  it('仅工作日生效时芯片带工作日前缀,tooltip 换用周末回落文案', () => {
    const model = timePricedModel()
    model.time_pricing!.weekdays_only = true
    const wrapper = mountTable([model], 1)
    const chips = wrapper.findAll('[title="modelPlaza.table.timePricingHintWeekdays"]')
    expect(chips).toHaveLength(2)
    expect(chips[0].text()).toContain('modelPlaza.table.timePricingWeekdays')
    expect(wrapper.find('[title="modelPlaza.table.timePricingHint"]').exists()).toBe(false)
  })

  it('每日生效(无 weekdays_only)不渲染工作日前缀', () => {
    const wrapper = mountTable([timePricedModel()], 1)
    expect(wrapper.find('tbody').text()).not.toContain('modelPlaza.table.timePricingWeekdays')
    expect(wrapper.find('[title="modelPlaza.table.timePricingHint"]').exists()).toBe(true)
  })

  it('分组启用高峰倍率时芯片 tooltip 追加高峰披露,价格与倍率列保持不含高峰的口径', () => {
    const wrapper = mountTable([timePricedModel()], 0.8, null, {
      peakWindow: '14:00-18:00 ×1.5 (UTC+08:00)',
      peakRateMultiplier: 1.5
    })
    const chip = wrapper.find('[title*="modelPlaza.table.timePricingHint"]')
    expect(chip.attributes('title')).toContain('modelPlaza.table.timePricingHintPeak')
    const cells = wrapper.findAll('tbody tr')[0].findAll('td')
    expect(cells[1].text()).toContain('$2.40')
    expect(cells[7].text()).toBe('0.8x')
  })

  it('分组未启用高峰(peakWindow 缺省)时 tooltip 不含高峰披露', () => {
    const wrapper = mountTable([timePricedModel()], 1)
    const chip = wrapper.find('[title*="modelPlaza.table.timePricingHint"]')
    expect(chip.attributes('title')).not.toContain('modelPlaza.table.timePricingHintPeak')
  })

  it('用户专属倍率生效时,芯片倍率按专属倍率 × 时段倍率', () => {
    const wrapper = mountTable([timePricedModel()], 1, 0.4)
    const chips = wrapper.findAll('[title="modelPlaza.table.timePricingHint"]')
    expect(chips[0].text()).toContain('0.2x')
    expect(chips[1].text()).toContain('0.48x')
  })

  it('无分时倍率时不渲染时段芯片', () => {
    const wrapper = mountTable([tokenModel()], 1)
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.find('[title*="modelPlaza.table.timePricingHint"]').exists()).toBe(false)
  })
})
