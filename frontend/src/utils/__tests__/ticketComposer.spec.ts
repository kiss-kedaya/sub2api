import { describe, expect, it } from 'vitest'
import { isComposerSendKey } from '../ticketComposer'

function key(partial: Partial<Parameters<typeof isComposerSendKey>[0]> = {}) {
  return {
    key: 'Enter',
    shiftKey: false,
    altKey: false,
    isComposing: false,
    repeat: false,
    keyCode: 13,
    ...partial
  }
}

describe('ticket composer keys', () => {
  it('sends on Enter and Ctrl/Cmd+Enter', () => {
    expect(isComposerSendKey(key())).toBe(true)
    expect(isComposerSendKey(key({ keyCode: 13 }))).toBe(true)
  })

  it('keeps Shift+Enter as a newline', () => {
    expect(isComposerSendKey(key({ shiftKey: true }))).toBe(false)
  })

  it('does not send while IME is composing', () => {
    expect(isComposerSendKey(key({ isComposing: true }))).toBe(false)
    expect(isComposerSendKey(key({ keyCode: 229 }))).toBe(false)
  })

  it('ignores held Enter and empty-key noise', () => {
    expect(isComposerSendKey(key({ repeat: true }))).toBe(false)
    expect(isComposerSendKey(key({ key: 'a' }))).toBe(false)
    expect(isComposerSendKey(key({ altKey: true }))).toBe(false)
  })
})
