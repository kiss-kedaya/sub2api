import { afterEach, describe, expect, it } from 'vitest'
import { compileStyle, parse } from 'vue/compiler-sfc'
import source from '../ConsoleAtmosphere.vue?raw'

let sheet: HTMLStyleElement | undefined

afterEach(() => {
  sheet?.remove()
  document.documentElement.classList.remove('dark')
})

describe('console background style isolation', () => {
  it('keeps document colors and opacity intact in dark mode', () => {
    const { descriptor } = parse(source)
    sheet = document.createElement('style')
    sheet.textContent = descriptor.styles.map(style => {
      const result = compileStyle({ source: style.content, id: 'data-v-atmosphere-check', scoped: style.scoped })
      expect(result.errors).toEqual([])
      return result.code
    }).join('\n')
    document.head.append(sheet)
    document.documentElement.classList.add('dark')

    for (const element of [document.documentElement, document.body]) {
      const style = getComputedStyle(element)
      expect(['', 'none']).toContain(style.filter)
      expect(['', '1']).toContain(style.opacity)
      expect(['', 'normal']).toContain(style.mixBlendMode)
    }
  })
})
