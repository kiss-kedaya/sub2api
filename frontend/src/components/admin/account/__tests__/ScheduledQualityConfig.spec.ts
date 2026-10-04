import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ScheduledQualityConfig from '../ScheduledQualityConfig.vue'
import ChanshuiAuditReport from '../ChanshuiAuditReport.vue'
import { completeChanshuiConfig, defaultChanshuiConfig, isChanshuiPolicyValid } from '@/utils/scheduledTestQuality'
import ChanshuiStopConditions from '../ChanshuiStopConditions.vue'
import Select from '@/components/common/Select.vue'
import type { ChanshuiStopCondition } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('scheduled Chanshui configuration', () => {
  it('shows service URL and multi-select only for Chanshui and does not ask for a key', async () => {
    const wrapper = mount(ScheduledQualityConfig, {
      props: { provider: 'pelican', config: defaultChanshuiConfig(), model: 'gpt-test' },
      global: { stubs: { Select: true } },
    })
    expect(wrapper.find('[data-testid="chanshui-service-url"]').exists()).toBe(false)
    await wrapper.setProps({ provider: 'chanshui' })
    expect((wrapper.get('[data-testid="chanshui-service-url"]').element as HTMLInputElement).value).toBe('https://chanshui.dev')
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.findAll('input[data-section]')).toHaveLength(9)
    expect(wrapper.findAll('input[data-section]').filter(input => (input.element as HTMLInputElement).checked).map(input => input.attributes('data-section'))).toEqual(['fingerprint'])
    await wrapper.get('[data-testid="chanshui-service-url"]').setValue('https://audit.example')
    expect(wrapper.emitted('update:config')?.[0]).toEqual([{ ...defaultChanshuiConfig(), base_url: 'https://audit.example' }])
    wrapper.unmount()
  })

  it('emits exact selected sections and blocks deselecting the last section', async () => {
    const config = { ...defaultChanshuiConfig(), sections: ['fingerprint', 'tools'] }
    const wrapper = mount(ScheduledQualityConfig, {
      props: { provider: 'chanshui', config, model: 'gpt-test' },
      global: { stubs: { Select: true } },
    })
    await wrapper.get('input[data-section="tools"]').setValue(false)
    expect(wrapper.emitted('update:config')?.[0]).toEqual([{ ...config, sections: ['fingerprint'] }])
    await wrapper.setProps({ config: { ...config, sections: ['thinking'] } })
    expect(wrapper.get('input[data-section="thinking"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.scheduledTests.thinkingOnlyClaude')
    wrapper.unmount()
  })
})

describe('Chanshui stop conditions', () => {
  it('defaults missing sections to fingerprint without replacing explicit selections', () => {
    expect(completeChanshuiConfig().sections).toEqual(['fingerprint'])
    expect(completeChanshuiConfig({ sections: ['tools'] }).sections).toEqual(['tools'])
    expect(completeChanshuiConfig({ sections: [] }).sections).toEqual([])
    expect(defaultChanshuiConfig().stop_condition?.rules).toEqual([{ type: 'fingerprint_mismatch' }])
  })

  it('edits expected model, any/all and a required score threshold', async () => {
    const wrapper = mount(ChanshuiStopConditions, {
      props: { modelValue: defaultChanshuiConfig().stop_condition, model: 'gpt-6-astra' },
      global: { stubs: { Select: true } },
    })
    const latest = () => wrapper.emitted('update:modelValue')!.at(-1)![0] as ChanshuiStopCondition
    expect(wrapper.get('input[type="text"]').attributes('placeholder')).toBe('gpt-6-astra')
    await wrapper.get('input[type="text"]').setValue('my-model')
    expect(latest().expected_model).toBe('my-model')
    await wrapper.setProps({ modelValue: latest() })
    wrapper.findAllComponents(Select)[0].vm.$emit('update:modelValue', 'all')
    expect(latest().match).toBe('all')
    await wrapper.setProps({ modelValue: latest() })
    wrapper.findAllComponents(Select)[1].vm.$emit('update:modelValue', 'total_score_below')
    await wrapper.setProps({ modelValue: latest() })
    expect(isChanshuiPolicyValid({ ...defaultChanshuiConfig(), stop_condition: latest() })).toBe(false)
    await wrapper.get('[data-testid="chanshui-score-threshold"]').setValue('80')
    expect(latest().rules).toEqual([{ type: 'total_score_below', threshold: 80 }])
    expect(latest().expected_model).toBe('my-model')
    expect(latest().match).toBe('all')
    expect(isChanshuiPolicyValid({ ...defaultChanshuiConfig(), stop_condition: latest() })).toBe(true)
    await wrapper.get('[data-testid="chanshui-score-threshold"]').setValue('101')
    expect(isChanshuiPolicyValid({ ...defaultChanshuiConfig(), stop_condition: latest() })).toBe(false)
    wrapper.unmount()
  })

  it('requires the selected section for a literal status rule', () => {
    const config = defaultChanshuiConfig()
    config.stop_condition = { match: 'any', rules: [{ type: 'section_status', section: 'tools', status: 'fail' }] }
    expect(isChanshuiPolicyValid(config)).toBe(false)
    config.sections.push('tools')
    expect(isChanshuiPolicyValid(config)).toBe(true)
  })
})

describe('Chanshui report contract', () => {
  it('uses total.score rather than integrity.score and does not treat unfinished IQ as passed', () => {
    const wrapper = mount(ChanshuiAuditReport, { props: { report: {
      provider: 'chanshui', audit_id: 'fixture-audit',
      probes: [{ id: 'Q1', status: 'queued' }],
      verdict: { total: { score: 56, max: 100 }, integrity: { score: 78 }, iq: { score: null, complete: false },
        tools: { status: 'pass', reason: 'tool roundtrip verified' },
        knowledge: { label: 'Knowledge fixture; not scored' },
        fingerprint: { top_model: 'fixture-model', note: 'relative candidates only' },
        hidden: { reason: '<script>alert(1)</script>' } },
    } } })
    expect(wrapper.get('[data-testid="audit-total"]').text()).toBe('56 / 100')
    expect(wrapper.text()).toContain('admin.scheduledTests.auditNotCompleted')
    expect(wrapper.text()).toContain('admin.scheduledTests.auditIncomplete')
    expect(wrapper.text()).toContain('Knowledge fixture; not scored')
    expect(wrapper.text()).toContain('fixture-model')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.find('iframe').exists()).toBe(false)
    wrapper.unmount()
  })
})
