import { readFileSync } from 'node:fs'
import postcss from 'postcss'
import { compileStyle, parse as parseSfc } from 'vue/compiler-sfc'
import { describe, expect, it } from 'vitest'

function readCss(relPath: string): string {
  return readFileSync(relPath, 'utf8')
}

type Specificity = [number, number, number]

function splitTopLevelCommas(input: string): string[] {
  const parts: string[] = []
  let depthParen = 0
  let depthBracket = 0
  let quote: string | null = null
  let start = 0
  for (let i = 0; i < input.length; i += 1) {
    const ch = input[i]
    if (quote) {
      if (ch === '\\') {
        i += 1
      } else if (ch === quote) {
        quote = null
      }
      continue
    }
    if (ch === "'" || ch === '"') {
      quote = ch
    } else if (ch === '[') {
      depthBracket += 1
    } else if (ch === ']') {
      depthBracket = Math.max(0, depthBracket - 1)
    } else if (ch === '(') {
      depthParen += 1
    } else if (ch === ')') {
      depthParen = Math.max(0, depthParen - 1)
    } else if (ch === ',' && depthParen === 0 && depthBracket === 0) {
      parts.push(input.slice(start, i).trim())
      start = i + 1
    }
  }
  parts.push(input.slice(start).trim())
  return parts.filter(Boolean)
}

function compareSpecificity(a: Specificity, b: Specificity): number {
  if (a[0] !== b[0]) return a[0] - b[0]
  if (a[1] !== b[1]) return a[1] - b[1]
  return a[2] - b[2]
}

function maxSpecificity(list: Specificity[]): Specificity {
  return list.reduce<Specificity>(
    (best, cur) => (compareSpecificity(cur, best) > 0 ? cur : best),
    [0, 0, 0]
  )
}

function calculateSelectorSpecificity(selector: string): Specificity {
  let a = 0
  let b = 0
  let c = 0
  let i = 0
  const s = selector.trim()

  while (i < s.length) {
    const ch = s[i]
    if (/\s/.test(ch) || ch === '>' || ch === '+' || ch === '~') {
      i += 1
      continue
    }
    if (ch === '*') {
      i += 1
      continue
    }
    if (ch === '#') {
      a += 1
      i += 1
      while (i < s.length && /[\w-]/.test(s[i])) i += 1
      continue
    }
    if (ch === '.') {
      b += 1
      i += 1
      while (i < s.length && /[\w-]/.test(s[i])) i += 1
      continue
    }
    if (ch === '[') {
      b += 1
      i += 1
      let quote: string | null = null
      while (i < s.length) {
        const cur = s[i]
        if (quote) {
          if (cur === '\\') i += 1
          else if (cur === quote) quote = null
        } else if (cur === "'" || cur === '"') {
          quote = cur
        } else if (cur === ']') {
          i += 1
          break
        }
        i += 1
      }
      continue
    }
    if (ch === ':') {
      const isPseudoElement = s[i + 1] === ':'
      i += isPseudoElement ? 2 : 1
      const nameStart = i
      while (i < s.length && /[\w-]/.test(s[i])) i += 1
      const name = s.slice(nameStart, i)
      if (s[i] === '(') {
        let depth = 1
        i += 1
        const argStart = i
        let quote: string | null = null
        while (i < s.length && depth > 0) {
          const cur = s[i]
          if (quote) {
            if (cur === '\\') i += 1
            else if (cur === quote) quote = null
          } else if (cur === "'" || cur === '"') {
            quote = cur
          } else if (cur === '(') {
            depth += 1
          } else if (cur === ')') {
            depth -= 1
            if (depth === 0) break
          }
          i += 1
        }
        const inner = s.slice(argStart, i)
        i += 1 // consume ')'
        if (!isPseudoElement && name === 'where') {
          continue
        }
        if (!isPseudoElement && (name === 'not' || name === 'is' || name === 'has')) {
          const innerBest = maxSpecificity(splitTopLevelCommas(inner).map(calculateSelectorSpecificity))
          a += innerBest[0]
          b += innerBest[1]
          c += innerBest[2]
          continue
        }
      }
      if (isPseudoElement) c += 1
      else b += 1
      continue
    }
    if (/[a-zA-Z_-]/.test(ch)) {
      c += 1
      while (i < s.length && /[\w-]/.test(s[i])) i += 1
      continue
    }
    i += 1
  }

  return [a, b, c]
}

interface SquircleRuleEntry {
  selector: string
  specificity: Specificity
  cornerShape: string
  order: number
}

function collectSquircleRules(cssText: string): SquircleRuleEntry[] {
  const root = postcss.parse(cssText)
  const entries: SquircleRuleEntry[] = []
  let order = 0

  root.walkAtRules('supports', (atRule) => {
    if (!atRule.params.includes('corner-shape: squircle')) return
    atRule.walkRules((rule) => {
      let cornerShape = ''
      rule.walkDecls('corner-shape', (decl) => {
        cornerShape = decl.value.trim()
      })
      if (!cornerShape) return
      for (const sel of splitTopLevelCommas(rule.selector)) {
        entries.push({
          selector: sel,
          specificity: calculateSelectorSpecificity(sel),
          cornerShape,
          order: order++,
        })
      }
    })
  })

  return entries
}

function winningCornerShape(
  entries: SquircleRuleEntry[],
  matcher: (selector: string) => boolean
): SquircleRuleEntry | undefined {
  const matched = entries.filter((entry) => matcher(entry.selector))
  matched.sort((x, y) => compareSpecificity(x.specificity, y.specificity) || x.order - y.order)
  return matched.at(-1)
}

function collectHoverSelectorsOutsideCapabilityGate(cssText: string): string[] {
  const root = postcss.parse(cssText)
  const ungated: string[] = []

  root.walkRules((rule) => {
    if (!rule.selector.includes(':hover')) return
    let parent = rule.parent
    let isGated = false
    while (parent && parent.type !== 'root') {
      if (
        parent.type === 'atrule' &&
        parent.name === 'media' &&
        parent.params.includes('hover: hover') &&
        parent.params.includes('pointer: fine')
      ) {
        isGated = true
        break
      }
      parent = parent.parent
    }
    if (!isGated) {
      ungated.push(rule.selector)
    }
  })

  return ungated
}

describe('M1 Core CSS & Stylesheet Craft Invariants', () => {
  const styleCss = readCss('src/style.css')
  const authQuietCss = readCss('src/styles/auth-quiet.css')
  const ticketsCss = readCss('src/styles/tickets.css')
  const onboardingCss = readCss('src/styles/onboarding.css')
  const elevationCss = readCss('src/styles/console-elevation.css')
  const signalCss = readCss('src/styles/console-signal.css')
  const studioCss = readCss('src/styles/console-studio.css')
  const workspaceCss = readCss('src/styles/console-workspace.css')
  const markdownCss = readCss('src/styles/announcement-markdown.css')

  it('aligns :root --c-primary-50..950 to Studio Blue (#155dfc / 21 93 252)', () => {
    const root = postcss.parse(styleCss)
    let rootRule: postcss.Rule | undefined
    root.walkRules(':root', (rule) => {
      if (!rootRule) rootRule = rule
    })
    expect(rootRule).toBeDefined()
    const decls = new Map<string, string>()
    rootRule!.walkDecls((d) => {
      if (d.prop.startsWith('--c-primary-')) decls.set(d.prop, d.value)
    })
    expect(decls.get('--c-primary-50')).toBe('239 246 255')
    expect(decls.get('--c-primary-600')).toBe('21 93 252')
    expect(decls.get('--c-primary-950')).toBe('22 36 86')
  })

  it('uses exact-token ~= pill exclusion, pill round guard with winning specificity, and pseudo-element corner-shape inheritance', () => {
    const root = postcss.parse(styleCss)
    const selectors: string[] = []
    root.walkRules((rule) => {
      selectors.push(rule.selector)
    })
    const joinedSelectors = selectors.join('\n')

    expect(joinedSelectors).toContain("[class*='rounded-']:not([class~='rounded-full']):not([class~='rounded-none'])")
    expect(joinedSelectors).not.toContain("[class*='rounded-full']")
    expect(joinedSelectors).not.toContain("[class*='rounded-none']")
    expect(joinedSelectors).toContain('.legal-footer-link:focus-visible')
    expect(joinedSelectors).toContain('.tx-list-row--fresh::after')
    expect(joinedSelectors).toContain(".console-sidebar [data-slot='link']::before")
    expect(joinedSelectors).toContain(".console-sidebar [data-slot='childLink']::before")
    expect(joinedSelectors).toContain("[data-slot='link']::before")
    expect(joinedSelectors).toContain("[data-slot='childLink']::before")
    expect(joinedSelectors).toContain('.model-test-search-wrap')
    expect(joinedSelectors).toContain('.account-test-settings')
    expect(joinedSelectors).toContain('.admin-menu .admin-menu-item')
    expect(joinedSelectors).toContain('.site-theme-preview')
    expect(joinedSelectors).toContain('.v3-bar-hitbox:focus-visible')
    expect(joinedSelectors).toContain('.ephemeral-key-bar')
    expect(joinedSelectors).toContain('.copy-btn')
    expect(joinedSelectors).toContain('.console-signal .signal-channel-card-grid > .grid')
    expect(joinedSelectors).toContain('.settings-section-highlight')
    expect(joinedSelectors).toContain('.legal-document-content blockquote')
    expect(joinedSelectors).toContain('.legal-nav-link')
    expect(styleCss).toContain('corner-shape: inherit;')

    const squircleEntries = collectSquircleRules(styleCss)
    expect(squircleEntries.length).toBeGreaterThan(0)

    const genericBaseRule = squircleEntries.find(
      (entry) =>
        entry.cornerShape === 'squircle' &&
        entry.selector.startsWith(':where(') &&
        entry.selector.includes('button:not([class~=\'rounded-full\'])')
    )
    expect(genericBaseRule).toBeDefined()
    expect(genericBaseRule!.specificity).toEqual([0, 0, 0])

    // `<button class="auth-step-back">` and `<button class="auth-step-back rounded-lg">`
    // must resolve to `corner-shape: round`, strictly beating `:where(...)` / `button:not(...)`.
    const stepBackWinner = winningCornerShape(squircleEntries, (sel) => {
      if (sel.startsWith(':where(')) return true
      return sel === 'html button.auth-step-back' || sel === 'html .auth-step-back'
    })
    expect(stepBackWinner?.cornerShape).toBe('round')
    expect(compareSpecificity(stepBackWinner!.specificity, [0, 1, 1])).toBeGreaterThan(0)

    // `<button class="btn rounded-xl">` inside `<div class="auth-card">`
    // must resolve to `corner-shape: round`, strictly beating `.btn` and `[class*='rounded-']`.
    const authBtnWinner = winningCornerShape(squircleEntries, (sel) => {
      if (sel.startsWith(':where(')) return true
      return sel === '.btn' || sel === 'html .auth-card .btn'
    })
    expect(authBtnWinner?.cornerShape).toBe('round')
    expect(compareSpecificity(authBtnWinner!.specificity, [0, 2, 0])).toBeGreaterThan(0)

    // `.legal-nav-link` is squircle on desktop and reset to `round` for mobile pills (<= 768px).
    const legalNavEntries = squircleEntries.filter((entry) => entry.selector === '.legal-nav-link')
    expect(legalNavEntries.map((entry) => entry.cornerShape)).toEqual(['squircle', 'round'])
  })

  it('removes unused glow keyframes, console-signal hover lift, and converts toast/progress-bar to GPU transitions', () => {
    expect(styleCss).not.toContain('@keyframes glow')
    expect(styleCss).not.toContain('--animate-glow')
    expect(styleCss).not.toContain('translateY(var(--cb-lift))')
    expect(styleCss).not.toContain('@apply animate-slide-in-right')
    expect(styleCss).toMatch(/@utility toast\s*\{[\s\S]*@starting-style\s*\{/s)
    expect(styleCss).toMatch(/@utility progress-bar\s*\{[\s\S]*transition-property:\s*transform;/s)
    expect(styleCss).not.toMatch(/@utility progress-bar\s*\{[\s\S]*transition-property:\s*width;/s)
  })

  it('enforces auth-quiet.css <= 240ms durations with leave <= enter', () => {
    expect(authQuietCss).toContain('animation: auth-card-rise 220ms')
    expect(authQuietCss).toContain('transition: transform 220ms')
    expect(authQuietCss).toContain('animation: auth-step-in 200ms')
    expect(authQuietCss).toContain('animation: auth-step-out 150ms')
    expect(authQuietCss).not.toContain('320ms')
  })

  it('uses dvh instead of vh in tickets.css lightbox, enforces 16px touch inputs, and 44px touch targets in onboarding.css', () => {
    expect(ticketsCss).toContain('max-height: 90dvh;')
    expect(ticketsCss).toContain('max-height: calc(90dvh - 52px);')
    expect(ticketsCss).not.toContain('90vh')
    expect(ticketsCss).toMatch(/@media\s*\(pointer:\s*coarse\),\s*\(max-width:\s*767px\)\s*\{[\s\S]*\.tickets-search input[\s\S]*\.ticket-composer textarea[\s\S]*\.ticket-select[\s\S]*font-size:\s*max\(16px,\s*1rem\);/s)
    expect(ticketsCss).toMatch(/\.tickets-status-tabs button,\s*\.ticket-select\s*\{\s*min-height:\s*44px;/s)

    expect(onboardingCss).toMatch(/@media\s*\(pointer:\s*coarse\),\s*\(max-width:\s*639px\)\s*\{[\s\S]*\.driver-popover-close-btn[\s\S]*width:\s*44px\s*!important;[\s\S]*height:\s*44px\s*!important;/s)
  })

  it('gates all CSS :hover rules behind @media (hover: hover) and (pointer: fine) across M1 stylesheets', () => {
    const stylesheets: Array<[string, string]> = [
      ['src/style.css', styleCss],
      ['src/styles/auth-quiet.css', authQuietCss],
      ['src/styles/tickets.css', ticketsCss],
      ['src/styles/onboarding.css', onboardingCss],
      ['src/styles/console-elevation.css', elevationCss],
      ['src/styles/console-signal.css', signalCss],
      ['src/styles/console-studio.css', studioCss],
      ['src/styles/console-workspace.css', workspaceCss],
    ]

    for (const [name, css] of stylesheets) {
      expect(collectHoverSelectorsOutsideCapabilityGate(css), `${name} has ungated :hover`).toEqual([])
    }

    expect(markdownCss).not.toMatch(/@apply[^;]*hover:/)
    expect(markdownCss).toMatch(/@media\s*\(hover:\s*hover\)\s*and\s*\(pointer:\s*fine\)/)
  })

  it('enforces mobile 44px tap targets, Light-mode WCAG tokens, onboarding footer wrap/gap, and scoped html.dark compilation', () => {
    expect(styleCss).toMatch(/@media\s*\(max-width:\s*1023px\)\s*\{[\s\S]*\.btn:not\(\[data-ignore-tap-target\]\):not\(\[data-size='xs'\]\)[\s\S]*button\[data-tour\]:not\(\[data-ignore-tap-target\]\):not\(\[data-size='xs'\]\)[\s\S]*min-height:\s*44px;/s)

    const themeCss = readCss('src/styles/nuxt-ui-theme.css')
    for (const decl of [
      '--ui-text-dimmed: #52525b;',
      '--ui-text-muted: #3f3f46;',
      '--ui-success: #166534;',
      '--ui-warning: #92400e;',
      '--ui-error: #b91c1c;',
      '--ui-info: #1d4ed8;',
      '--app-amount-positive: #166534;',
    ]) {
      expect(themeCss).toContain(decl)
    }

    expect(onboardingCss).toMatch(/\.theme-tour-popover\s+\.driver-popover-footer\s*\{[^}]*gap:\s*16px\s*!important;[^}]*flex-wrap:\s*wrap\s*!important;/s)

    const scopedSfcChecks: Array<{ path: string; expected: RegExp }> = [
      {
        path: 'src/components/tickets/TicketStatusBadge.vue',
        expected: /html\.dark\s+\.ticket-status\[data-status=["']waiting_user["']\]\[data-v-testscope\]/,
      },
      {
        path: 'src/features/channel-monitor-v2/RelayPulseMatrix.vue',
        expected: /html\.dark\s+\.matrix-floating-tooltip\[data-v-testscope\]/,
      },
      {
        path: 'src/components/common/TransactionList.vue',
        expected: /html\.dark\s+\.tx-list-row--fresh\[data-v-testscope\]::after/,
      },
      {
        path: 'src/components/layout/AuthLayout.vue',
        expected: /html\.dark\s+\.auth-grid\[data-v-testscope\]/,
      },
      {
        path: 'src/views/public/LegalDocumentView.vue',
        expected: /html\.dark\s+\.legal-grid\[data-v-testscope\]/,
      },
    ]

    for (const { path, expected } of scopedSfcChecks) {
      const source = readCss(path)
      expect(source).not.toMatch(/:global\((?:html)?\.dark\)/)
      const { descriptor } = parseSfc(source, { filename: path })
      const scopedBlock = descriptor.styles.find((s) => s.scoped)
      expect(scopedBlock, `${path} missing <style scoped>`).toBeDefined()
      const compiled = compileStyle({
        source: scopedBlock!.content,
        filename: path,
        id: 'data-v-testscope',
        scoped: true,
      })
      expect(compiled.errors).toEqual([])
      expect(compiled.code).toMatch(expected)
      expect(compiled.code).not.toMatch(/^\.dark\s*\{/m)
    }
  })
})
