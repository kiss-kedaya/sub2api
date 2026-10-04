import type { ChanshuiQualityConfig, ChanshuiStopCondition } from '@/types'

export const chanshuiSections = ['fingerprint', 'injection', 'hidden', 'cache', 'tools', 'web', 'knowledge', 'thinking', 'iq'] as const
export const defaultChanshuiStopCondition = (): ChanshuiStopCondition => ({ match: 'any', expected_model: '', rules: [{ type: 'fingerprint_mismatch' }] })
export const defaultChanshuiConfig = (): ChanshuiQualityConfig => ({
  base_url: 'https://chanshui.dev', protocol: 'auto', timeout: 360, sections: ['fingerprint'],
  stop_condition: defaultChanshuiStopCondition(),
})

export function completeChanshuiConfig(config?: Partial<ChanshuiQualityConfig>): ChanshuiQualityConfig {
  const condition = config?.stop_condition || defaultChanshuiStopCondition()
  return { ...defaultChanshuiConfig(), ...config,
    sections: [...(config?.sections ?? ['fingerprint'])],
    stop_condition: { ...condition, rules: condition.rules.map(rule => ({ ...rule })) },
  }
}

export function isChanshuiPolicyValid(config: ChanshuiQualityConfig): boolean {
  const policy = config.stop_condition || defaultChanshuiStopCondition()
  const selected = (section: string) => config.sections.length === 0 || config.sections.includes(section)
  return ['any', 'all'].includes(policy.match) && policy.rules.length > 0 && policy.rules.length <= 10 && policy.rules.every(rule => {
    if (rule.type === 'fingerprint_mismatch') return selected('fingerprint')
    if (rule.type === 'total_score_below') return typeof rule.threshold === 'number' && Number.isFinite(rule.threshold) && rule.threshold >= 0 && rule.threshold <= 100 && !(config.sections.length === 1 && config.sections[0] === 'knowledge')
    return rule.type === 'section_status' && ['tools', 'web', 'cache', 'hidden', 'knowledge'].includes(rule.section || '') && selected(rule.section || '') && !!rule.status?.trim() && rule.status.length <= 64
  })
}

export function auditRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function parseChanshuiReport(text: string): {
  provider: 'chanshui'; audit_id?: string; audit_status?: string; expires_at?: number; model?: string
  verdict?: Record<string, unknown>; probes?: unknown; skipped?: unknown
  decision?: { status: string; reason: string }
} | null {
  try {
    const result = JSON.parse(text)
    return result && result.provider === 'chanshui' ? result : null
  } catch { return null }
}
