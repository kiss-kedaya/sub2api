import type { DocsLanguage } from '@/data/apiDocs'

export type DocsCodeTokenKind =
  | 'plain'
  | 'comment'
  | 'string'
  | 'keyword'
  | 'number'
  | 'key'
  | 'flag'
  | 'url'
  | 'var'

export interface DocsCodeToken {
  text: string
  kind: DocsCodeTokenKind
}

export interface HighlightSegment {
  text: string
  match: boolean
}

const PYTHON_KEYWORDS = new Set([
  'import', 'from', 'as', 'with', 'open', 'if', 'else', 'elif', 'for', 'in',
  'return', 'raise', 'def', 'class', 'try', 'except', 'finally', 'and', 'or',
  'not', 'True', 'False', 'None', 'print', 'await', 'async',
])

const NODE_KEYWORDS = new Set([
  'import', 'from', 'const', 'let', 'var', 'await', 'async', 'if', 'else',
  'for', 'of', 'in', 'return', 'throw', 'new', 'function', 'try', 'catch',
  'true', 'false', 'null', 'undefined', 'export', 'default',
])

const SHELL_KEYWORDS = new Set([
  'curl', 'export', 'set', 'echo', 'cat', 'npx', 'npm', 'pnpm', 'pip', 'uv',
  'python', 'node', 'claude', 'codex', 'gemini', 'opencode',
])

function tokenizeJsonLine(line: string): DocsCodeToken[] {
  const tokens: DocsCodeToken[] = []
  const pattern = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g
  let last = 0
  let m: RegExpExecArray | null
  while ((m = pattern.exec(line)) !== null) {
    if (m.index > last) tokens.push({ text: line.slice(last, m.index), kind: 'plain' })
    if (m[1]) {
      if (m[2]) {
        tokens.push({ text: m[1], kind: 'key' })
        tokens.push({ text: m[2], kind: 'plain' })
      } else {
        const isUrl = /^"https?:\/\//.test(m[1])
        tokens.push({ text: m[1], kind: isUrl ? 'url' : 'string' })
      }
    } else if (m[3]) {
      tokens.push({ text: m[3], kind: 'keyword' })
    } else {
      tokens.push({ text: m[0], kind: 'number' })
    }
    last = pattern.lastIndex
  }
  if (last < line.length) tokens.push({ text: line.slice(last), kind: 'plain' })
  return tokens.length ? tokens : [{ text: '', kind: 'plain' }]
}

function tokenizeScriptLine(line: string, keywords: Set<string>, hashComment: boolean): DocsCodeToken[] {
  const tokens: DocsCodeToken[] = []
  const commentRe = hashComment ? /#.*$/ : /\/\/.*$/
  const commentMatch = commentRe.exec(line)
  // Avoid treating # inside strings as comments by scanning strings first.
  const pattern = hashComment
    ? /(#[^\n]*)|("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|\b(\d+(?:\.\d+)?)\b|\b([A-Za-z_][A-Za-z0-9_]*)\b/g
    : /(\/\/[^\n]*)|(`(?:\\.|[^`\\])*`|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|\b(\d+(?:\.\d+)?)\b|\b([A-Za-z_][A-Za-z0-9_]*)\b/g
  void commentMatch

  let last = 0
  let m: RegExpExecArray | null
  while ((m = pattern.exec(line)) !== null) {
    if (m.index > last) tokens.push({ text: line.slice(last, m.index), kind: 'plain' })
    if (m[1]) {
      tokens.push({ text: m[1], kind: 'comment' })
    } else if (m[2]) {
      const str = m[2]
      const isUrl = /^["'`]https?:\/\//.test(str)
      tokens.push({ text: str, kind: isUrl ? 'url' : 'string' })
    } else if (m[3]) {
      tokens.push({ text: m[3], kind: 'number' })
    } else if (m[4]) {
      tokens.push({ text: m[4], kind: keywords.has(m[4]) ? 'keyword' : 'plain' })
    }
    last = pattern.lastIndex
  }
  if (last < line.length) tokens.push({ text: line.slice(last), kind: 'plain' })
  return tokens.length ? tokens : [{ text: '', kind: 'plain' }]
}

function tokenizeShellLine(line: string): DocsCodeToken[] {
  const trimmed = line.trimStart()
  if (trimmed.startsWith('#')) return [{ text: line, kind: 'comment' }]
  const tokens: DocsCodeToken[] = []
  const pattern = /(https?:\/\/[^\s"'`\\]+)|("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|(\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}]+\})|((?:^|\s)--?[A-Za-z][A-Za-z0-9-]*)|\b([A-Za-z_][A-Za-z0-9_-]*)\b/g
  let last = 0
  let m: RegExpExecArray | null
  while ((m = pattern.exec(line)) !== null) {
    if (m.index > last) tokens.push({ text: line.slice(last, m.index), kind: 'plain' })
    if (m[1]) {
      tokens.push({ text: m[1], kind: 'url' })
    } else if (m[2]) {
      tokens.push({ text: m[2], kind: 'string' })
    } else if (m[3]) {
      tokens.push({ text: m[3], kind: 'var' })
    } else if (m[4]) {
      const leadingSpace = m[4].match(/^\s+/)?.[0] ?? ''
      if (leadingSpace) tokens.push({ text: leadingSpace, kind: 'plain' })
      tokens.push({ text: m[4].slice(leadingSpace.length), kind: 'flag' })
    } else if (m[5]) {
      tokens.push({ text: m[5], kind: SHELL_KEYWORDS.has(m[5]) ? 'keyword' : 'plain' })
    }
    last = pattern.lastIndex
  }
  if (last < line.length) tokens.push({ text: line.slice(last), kind: 'plain' })
  return tokens.length ? tokens : [{ text: '', kind: 'plain' }]
}

function tokenizeTomlLine(line: string): DocsCodeToken[] {
  const trimmed = line.trimStart()
  if (trimmed.startsWith('#')) return [{ text: line, kind: 'comment' }]
  if (/^\[.*\]$/.test(trimmed)) return [{ text: line, kind: 'keyword' }]
  const eqIdx = line.indexOf('=')
  if (eqIdx > 0) {
    const left = line.slice(0, eqIdx)
    const right = line.slice(eqIdx + 1)
    return [
      { text: left, kind: 'key' },
      { text: '=', kind: 'plain' },
      ...tokenizeScriptLine(right, PYTHON_KEYWORDS, true),
    ]
  }
  return [{ text: line, kind: 'plain' }]
}

export function tokenizeDocsCode(code: string, language: DocsLanguage): DocsCodeToken[][] {
  const lines = code.replace(/\r\n/g, '\n').split('\n')
  return lines.map((line) => {
    switch (language) {
      case 'json':
        return tokenizeJsonLine(line)
      case 'python':
        return tokenizeScriptLine(line, PYTHON_KEYWORDS, true)
      case 'node':
        return tokenizeScriptLine(line, NODE_KEYWORDS, false)
      case 'toml':
        return tokenizeTomlLine(line)
      case 'curl':
      case 'shell':
      default:
        return tokenizeShellLine(line)
    }
  })
}

export function splitHighlightSegments(text: string, query: string): HighlightSegment[] {
  const trimmed = query.trim()
  if (!trimmed) return [{ text, match: false }]
  const terms = [...new Set(trimmed.toLowerCase().split(/\s+/).filter(Boolean))]
  if (!terms.length) return [{ text, match: false }]
  const escaped = terms.map((term) => term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).sort((a, b) => b.length - a.length)
  const regex = new RegExp(`(${escaped.join('|')})`, 'gi')
  const parts = text.split(regex).filter((part) => part.length > 0)
  return parts.map((part) => ({
    text: part,
    match: terms.includes(part.toLowerCase()),
  }))
}
