import { describe, expect, it } from 'vitest'
import { splitHighlightSegments, tokenizeDocsCode } from '../docsHighlight'
import type { DocsLanguage } from '@/data/apiDocs'

describe('docsHighlight utilities', () => {
  it.each<[DocsLanguage, string]>([
    ['json', '{\n  "model": "claude-sonnet-4-6",\n  "url": "https://example.test/v1",\n  "stream": true,\n  "max_tokens": 1024\n}'],
    ['python', '# comment\nimport requests\nresp = requests.post("https://example.test/v1/models", timeout=30)\nprint(True, None)'],
    ['node', '// comment\nconst res = await fetch("https://example.test/v1/models");\nconsole.log(res.ok);'],
    ['curl', 'curl https://example.test/v1/models \\\n  -H "Authorization: Bearer $API_KEY"'],
    ['toml', '# config\n[model_providers.site]\nbase_url = "https://example.test/v1"'],
    ['shell', 'export API_KEY="demo"\ncurl https://example.test/v1/models'],
  ])('reconstructs %s code losslessly while classifying syntax tokens', (lang, sample) => {
    const lines = tokenizeDocsCode(sample, lang)
    const reconstructed = lines.map((line) => line.map((t) => t.text).join('')).join('\n')
    expect(reconstructed).toBe(sample)
    const kinds = new Set(lines.flat().map((t) => t.kind))
    expect(kinds.size).toBeGreaterThan(1)
  })

  it('splits search highlight segments safely without dropping characters', () => {
    const source = 'POST /v1/models returns model list'
    const segments = splitHighlightSegments(source, 'models list')
    expect(segments.map((s) => s.text).join('')).toBe(source)
    expect(segments.filter((s) => s.match).map((s) => s.text.toLowerCase())).toEqual(['models', 'list'])
  })
})
