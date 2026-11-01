import { DOMWrapper } from '@vue/test-utils'

/**
 * Nuxt UI overlays (UPopover, UModal, USlideover, UDropdownMenu) teleport their content to
 * <body>, so `wrapper.find()` cannot see it. These query the document instead and return
 * DOMWrappers, so specs keep using `.trigger()`, `.setValue()` and `.attributes()`.
 */
export function bodyFind<T extends Element = Element>(selector: string): DOMWrapper<T> | null {
  const element = document.body.querySelector<T>(selector)
  return element ? new DOMWrapper(element) : null
}

export function bodyGet<T extends Element = Element>(selector: string): DOMWrapper<T> {
  const found = bodyFind<T>(selector)
  if (!found) throw new Error(`no element in document.body matches ${selector}`)
  return found
}

export function bodyFindAll<T extends Element = Element>(selector: string): DOMWrapper<T>[] {
  return Array.from(document.body.querySelectorAll<T>(selector)).map((element) => new DOMWrapper(element))
}
