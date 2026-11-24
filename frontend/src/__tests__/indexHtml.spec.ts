import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const html = readFileSync(resolve(__dirname, '../../index.html'), 'utf8')

// index.html is served by the Go server with a Content-Security-Policy of `script-src 'self' 'nonce-…'`. The server puts that
// nonce on the one inline script it injects itself (window.__APP_CONFIG__); an inline script written here would be blocked
// in production without any error in development. And the first-load bar is drawn with `#app:empty`, so #app must stay empty.
describe('index.html', () => {
  it('has no inline classic <script> (the production CSP would block it)', () => {
    const scripts = [...html.matchAll(/<script\b([^>]*)>/gi)].map((m) => m[1])
    expect(scripts.length).toBeGreaterThan(0)
    for (const attrs of scripts) {
      const external = /\bsrc\s*=/.test(attrs)
      const module = /\btype\s*=\s*["']module["']/.test(attrs)
      // a module script with a src is bundled into an external file by Vite; anything else is an inline script
      expect(external && module, `unexpected inline script: <script${attrs}>`).toBe(true)
    }
  })

  it('keeps #app empty, because the first-load bar is `#app:empty` and must disappear when Vue mounts', () => {
    expect(html).toMatch(/<div id="app"[^>]*><\/div>/)
    expect(html).toContain('#app:empty::before')
    expect(html).toContain('#app:empty::after')
  })

  it('shows the bar only after a delay, so a load that is fast enough never flashes it', () => {
    const style = /<style>([\s\S]*?)<\/style>/.exec(html)?.[1] ?? ''
    // opacity starts at 0, and every animation (the fade-in and the moving part, in every rule) waits 0.4 s before it starts
    expect(style).toMatch(/opacity:\s*0\s*;/)
    const declarations = [...style.matchAll(/animation:\s*([^;]+);/g)].map((m) => m[1])
    expect(declarations.length).toBeGreaterThan(0)
    for (const declaration of declarations) {
      // split on the commas between animations, not the ones inside cubic-bezier(...)
      for (const part of declaration.split(/,(?![^(]*\))/)) expect(part, `no 0.4s delay in: ${part.trim()}`).toMatch(/\b0\.4s\b/)
    }
  })

  it('respects prefers-reduced-motion by not running the moving part', () => {
    const style = /<style>([\s\S]*?)<\/style>/.exec(html)?.[1] ?? ''
    expect(style).toMatch(/@media \(prefers-reduced-motion: reduce\)[\s\S]*?#app:empty::after\s*\{\s*display:\s*none/)
  })

  it('does not hard-code a page background that would fight the theme', () => {
    const style = /<style>([\s\S]*?)<\/style>/.exec(html)?.[1] ?? ''
    // light/dark and the site's default mode are decided by JS after mount; a colour written here would flash the wrong one
    expect(style).not.toMatch(/\b(html|body)\b\s*\{[^}]*background/)
  })
})
