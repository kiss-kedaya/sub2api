import { describe, expect, it } from 'vitest'
import {
  CC_SWITCH_APPS,
  CLAUDE_CC_SWITCH_MODEL,
  GEMINI_CC_SWITCH_MODEL,
  GROK_CC_SWITCH_MODEL,
  OPENAI_CC_SWITCH_CODEX_MODEL,
  buildCcSwitchImportDeeplink,
  ccSwitchProviderDeeplinkSupported,
  parseCcSwitchModelsList,
  pickCcSwitchModel
} from '@/utils/ccswitchImport'
import type { GroupPlatform } from '@/types'

function paramsFromDeeplink(deeplink: string): URLSearchParams {
  const query = deeplink.split('?')[1] || ''
  return new URLSearchParams(query)
}

describe('ccswitchImport utils', () => {
  it('defaults OpenAI CC Switch imports to the current Codex model', () => {
    expect(OPENAI_CC_SWITCH_CODEX_MODEL).toBe('gpt-5.6-sol')
  })

  it('defaults Grok Build imports to the current Grok model', () => {
    expect(GROK_CC_SWITCH_MODEL).toBe('grok-4.6')
  })

  it('defaults Claude Code imports to Sonnet 4.6', () => {
    expect(CLAUDE_CC_SWITCH_MODEL).toBe('claude-sonnet-4-6')
  })

  const baseInput = {
    baseUrl: 'https://api.example.com',
    providerName: 'Sub2API',
    apiKey: 'sk-test',
    usageScript: 'return true'
  }

  it('adds the Codex model parameter for OpenAI imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'openai',
        clientType: 'claude'
      })
    )

    expect(params.get('resource')).toBe('provider')
    expect(params.get('app')).toBe('codex')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.get('model')).toBe(OPENAI_CC_SWITCH_CODEX_MODEL)
    expect(atob(params.get('usageScript') || '')).toBe(baseInput.usageScript)
  })

  it.each([
    'https://api.example.com',
    'https://api.example.com/',
    'https://api.example.com/v1',
    'https://api.example.com/v1/'
  ])('imports Grok Build with one /v1 suffix for base URL %s', (baseUrl) => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        baseUrl,
        platform: 'grok',
        clientType: 'claude'
      })
    )

    expect(params.get('app')).toBe('grokbuild')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('model')).toBe(GROK_CC_SWITCH_MODEL)
  })

  it.each([
    {
      platform: 'anthropic' as GroupPlatform,
      clientType: 'claude' as const,
      app: 'claude',
      model: CLAUDE_CC_SWITCH_MODEL
    },
    {
      platform: 'gemini' as GroupPlatform,
      clientType: 'gemini' as const,
      app: 'gemini',
      model: GEMINI_CC_SWITCH_MODEL
    }
  ])('adds the default model for $platform imports', ({ platform, clientType, app, model }) => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform,
        clientType
      })
    )

    expect(params.get('app')).toBe(app)
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.get('model')).toBe(model)
  })

  it('keeps Antigravity imports on the selected client endpoint with the Gemini default model', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'antigravity',
        clientType: 'gemini'
      })
    )

    expect(params.get('app')).toBe('gemini')
    expect(params.get('endpoint')).toBe(`${baseInput.baseUrl}/antigravity`)
    expect(params.get('model')).toBe(GEMINI_CC_SWITCH_MODEL)
  })

  it('lets the user override a Codex group import onto Claude Code', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'openai',
        clientType: 'claude',
        app: 'claude',
        model: ''
      })
    )

    expect(params.get('app')).toBe('claude')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
    expect(params.has('model')).toBe(false)
  })

  it('lets the user override the Codex model while keeping the Codex app', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'openai',
        app: 'codex',
        model: 'gpt-5.4'
      })
    )

    expect(params.get('app')).toBe('codex')
    expect(params.get('model')).toBe('gpt-5.4')
    expect(params.get('endpoint')).toBe(baseInput.baseUrl)
  })

  it('imports a Codex group into Grok with the Grok endpoint and default model', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'openai',
        app: 'grokbuild'
      })
    )

    expect(params.get('app')).toBe('grokbuild')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('model')).toBe(GROK_CC_SWITCH_MODEL)
  })

  it('keeps the Antigravity endpoint when the user picks Codex', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'antigravity',
        app: 'codex',
        model: 'gpt-5.5'
      })
    )

    expect(params.get('app')).toBe('codex')
    expect(params.get('endpoint')).toBe(`${baseInput.baseUrl}/antigravity`)
    expect(params.get('model')).toBe('gpt-5.5')
  })

  it('covers every CC Switch APP_IDS value', () => {
    expect(CC_SWITCH_APPS).toEqual([
      'claude',
      'claude-desktop',
      'codex',
      'gemini',
      'grokbuild',
      'opencode',
      'openclaw',
      'hermes',
      'pi'
    ])
  })

  it.each(['opencode', 'openclaw', 'hermes'] as const)(
    'writes app=%s for the extra CC Switch provider apps',
    (app) => {
      const params = paramsFromDeeplink(
        buildCcSwitchImportDeeplink({
          ...baseInput,
          platform: 'openai',
          app,
          model: ''
        })
      )

      expect(params.get('app')).toBe(app)
      expect(params.get('endpoint')).toBe(baseInput.baseUrl)
      expect(params.has('model')).toBe(false)
      expect(ccSwitchProviderDeeplinkSupported(app)).toBe(true)
    }
  )

  it('still emits app=pi even though CC Switch rejects Pi provider deeplinks', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'grok',
        app: 'pi',
        model: 'grok-4.5'
      })
    )

    expect(params.get('app')).toBe('pi')
    expect(params.get('model')).toBe('grok-4.5')
    expect(ccSwitchProviderDeeplinkSupported('pi')).toBe(false)
  })

  it('emits app=claude-desktop for Claude Desktop imports', () => {
    const params = paramsFromDeeplink(
      buildCcSwitchImportDeeplink({
        ...baseInput,
        platform: 'anthropic',
        app: 'claude-desktop',
        model: ''
      })
    )

    expect(params.get('app')).toBe('claude-desktop')
    expect(ccSwitchProviderDeeplinkSupported('claude-desktop')).toBe(false)
  })

  it('parses OpenAI-style /v1/models payloads and drops duplicates', () => {
    expect(
      parseCcSwitchModelsList({
        object: 'list',
        data: [
          { id: 'gpt-5.6-sol' },
          { id: 'grok-4.6' },
          { id: 'gpt-5.6-sol' },
          { name: 'gemini-2.5-pro' },
          'claude-sonnet-4-6',
          { id: '  ' },
          null
        ]
      })
    ).toEqual(['gpt-5.6-sol', 'grok-4.6', 'gemini-2.5-pro', 'claude-sonnet-4-6'])
  })

  it('prefers the platform default when it is in the key model list', () => {
    expect(pickCcSwitchModel('gpt-5.6-sol', ['gpt-5.5', 'gpt-5.6-sol', 'gpt-5.6'])).toBe(
      'gpt-5.6-sol'
    )
  })

  it('falls back to the first key model when the platform default is missing', () => {
    expect(pickCcSwitchModel('claude-sonnet-4-6', ['grok-4.6', 'grok-4.5'])).toBe('grok-4.6')
  })
})
