export type ComposerKey = Pick<KeyboardEvent, 'key' | 'shiftKey' | 'altKey' | 'isComposing' | 'repeat' | 'keyCode'>

export function isComposerIME(event: ComposerKey): boolean {
  return event.isComposing || event.keyCode === 229
}

export function isComposerSendKey(event: ComposerKey): boolean {
  if (event.key !== 'Enter' || event.shiftKey || event.altKey) return false
  if (isComposerIME(event) || event.repeat) return false
  return true
}
