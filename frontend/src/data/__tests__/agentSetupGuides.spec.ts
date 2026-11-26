import { describe, expect, it } from 'vitest'
import {
  getAgentSetupCatalog,
  getHomeAgentHighlights,
  resolveAgentGatewayOrigin,
  resolveAgentGuide,
} from '../agentSetupGuides'
import { CC_SWITCH_APPS } from '@/utils/ccswitchImport'

describe('agentSetupGuides data', () => {
  it('resolves gateway origin safely without duplicating /v1 or accepting invalid schemes', () => {
    expect(resolveAgentGatewayOrigin('https://api.example.test/v1/', 'https://web.example.test')).toBe('https://api.example.test')
    expect(resolveAgentGatewayOrigin('https://api.example.test/v1/v1', 'https://web.example.test')).toBe('https://api.example.test')
    expect(resolveAgentGatewayOrigin('', 'http://127.0.0.1:17777/docs')).toBe('http://127.0.0.1:17777')
    expect(resolveAgentGatewayOrigin('javascript:alert(1)', 'https://web.example.test')).toBe('https://web.example.test')
  })

  it.each(['zh', 'en'])('provides unique agents, covers all 14 CC Switch apps, and avoids private hosts or real keys in %s', (locale) => {
    const origin = 'http://127.0.0.1:18888'
    const catalog = getAgentSetupCatalog(locale, origin)

    expect(catalog.origin).toBe(origin)
    expect(catalog.v1BaseUrl).toBe(`${origin}/v1`)
    expect(catalog.siteWorkflowSteps).toHaveLength(4)
    expect(new Set(catalog.agents.map((agent) => agent.id)).size).toBe(catalog.agents.length)

    // Every app in CC_SWITCH_APPS (farion1231/cc-switch v4) must be represented by at least one guide.
    const coveredApps = new Set(catalog.agents.flatMap((agent) => agent.ccSwitch.appIds))
    for (const app of CC_SWITCH_APPS) {
      expect(coveredApps.has(app), `${locale} missing CC Switch app ${app}`).toBe(true)
    }

    const serialized = JSON.stringify(catalog)
    expect(serialized).not.toMatch(/kedaya\.(ai|xyz)/i)
    expect(serialized).not.toMatch(/sk-[A-Za-z0-9]{8,}/)
    for (const agent of catalog.agents) {
      expect(agent.baseUrl).not.toContain('/v1/v1')
      expect(agent.ccSwitch.endpoint).not.toContain('/v1/v1')
      for (const step of agent.manualSteps) {
        const snippets = [
          ...(step.genericSnippets ?? []),
          ...Object.values(step.snippets ?? {}).flat(),
        ]
        for (const snippet of snippets) {
          expect(snippet.code, `${agent.id}:${snippet.label}`).not.toContain('/v1/v1')
        }
      }
      for (const cmd of Object.values(agent.verifyCommands)) {
        expect(cmd).not.toContain('/v1/v1')
      }
    }
  })

  it('keeps Base URL rules accurate per protocol (bare origin for Claude & Gemini, /v1 for Codex & Grok)', () => {
    const origin = 'https://gateway.example.test'
    const catalog = getAgentSetupCatalog('zh', origin)

    const claude = resolveAgentGuide(catalog.agents, 'claude-code')!
    const codex = resolveAgentGuide(catalog.agents, 'codex')!
    const gemini = resolveAgentGuide(catalog.agents, 'gemini-cli')!
    const grok = resolveAgentGuide(catalog.agents, 'grok-build')!

    expect(claude.baseUrl).toBe(origin)
    expect(claude.ccSwitch.endpoint).toBe(origin)
    expect(codex.baseUrl).toBe(`${origin}/v1`)
    expect(gemini.baseUrl).toBe(origin)
    expect(gemini.baseUrlRule).toContain('/v1beta')
    expect(grok.baseUrl).toBe(`${origin}/v1`)
    expect(grok.ccSwitch.endpoint).toBe(`${origin}/v1`)
  })

  it('interpolates a custom model override cleanly into manual snippets without leaving stale defaults', () => {
    const catalog = getAgentSetupCatalog('en', 'https://gateway.example.test', 'claude-opus-5-5')
    const claude = resolveAgentGuide(catalog.agents, 'claude-code')!
    const unixSnippets = claude.manualSteps[0].snippets!.unix!
    expect(unixSnippets.some((snippet) => snippet.code.includes('ANTHROPIC_MODEL="claude-opus-5-5"'))).toBe(true)
    expect(unixSnippets.some((snippet) => snippet.code.includes('"ANTHROPIC_MODEL": "claude-opus-5-5"'))).toBe(true)
  })

  it('returns null for unknown agent IDs and builds 6 home highlights', () => {
    const catalog = getAgentSetupCatalog('en', 'https://gateway.example.test')
    expect(resolveAgentGuide(catalog.agents, 'unknown-agent')).toBeNull()
    expect(resolveAgentGuide(catalog.agents, undefined)).toBeNull()
    expect(resolveAgentGuide(catalog.agents, '  CLAUDE-CODE ')).toMatchObject({ id: 'claude-code' })

    const highlights = getHomeAgentHighlights('zh', 'https://gateway.example.test')
    expect(highlights).toHaveLength(6)
    expect(highlights[0].id).toBe('claude-code')
    expect(highlights[1].id).toBe('codex')
  })
})
