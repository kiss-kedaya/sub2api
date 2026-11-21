import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  __resetClipboardSchedulerForTests,
  clearClipboardBestEffort,
  copySensitiveText,
  copyText,
  execCommandCopy
} from '../clipboard'

const writeText = vi.fn()

function installClipboard(value: unknown): void {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value })
}

function installExecCommand(handler: (() => boolean) | undefined): void {
  Object.defineProperty(document, 'execCommand', { configurable: true, value: handler })
}

describe('sensitive clipboard copy', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    writeText.mockResolvedValue(undefined)
    installClipboard({ writeText })
    installExecCommand(undefined)
    __resetClipboardSchedulerForTests()
  })

  afterEach(() => {
    __resetClipboardSchedulerForTests()
    vi.useRealTimers()
    vi.restoreAllMocks()
    delete (navigator as { clipboard?: unknown }).clipboard
    delete (document as { execCommand?: unknown }).execCommand
  })

  it('uses navigator.clipboard when available', async () => {
    const result = await copyText('S2V1-AAAA')
    expect(result).toEqual({ ok: true, mode: 'clipboard' })
    expect(writeText).toHaveBeenCalledWith('S2V1-AAAA')
  })

  it('falls back to execCommand when the Clipboard API rejects (iOS gesture / permissions)', async () => {
    writeText.mockRejectedValue(new DOMException('denied', 'NotAllowedError'))
    const exec = vi.fn(() => true)
    installExecCommand(exec)
    const result = await copyText('S2V1-BBBB')
    expect(result).toEqual({ ok: true, mode: 'execCommand' })
    expect(exec).toHaveBeenCalledTimes(1)
  })

  it('reports manual when every mechanism fails, so the UI can say "write it down"', async () => {
    writeText.mockRejectedValue(new Error('denied'))
    installExecCommand(() => false)
    const result = await copyText('S2V1-CCCC')
    expect(result).toEqual({ ok: false, mode: 'manual' })
  })

  it('works without any Clipboard API (plain http) through execCommand only', async () => {
    installClipboard(undefined)
    installExecCommand(() => true)
    const result = await copyText('S2V1-DDDD')
    expect(result).toEqual({ ok: true, mode: 'execCommand' })
  })

  it('leaves no textarea behind in the document', () => {
    installClipboard(undefined)
    installExecCommand(() => true)
    execCommandCopy('S2V1-EEEE')
    expect(document.querySelectorAll('textarea')).toHaveLength(0)
  })

  it('clears the clipboard best-effort after the scheduled delay', async () => {
    vi.useFakeTimers()
    const handle = await copySensitiveText('S2V1-FFFF')
    expect(handle.result.ok).toBe(true)
    handle.scheduleClear(500)
    expect(writeText).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(499)
    expect(writeText).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    expect(writeText).toHaveBeenLastCalledWith('')
  })

  it('can cancel the scheduled wipe when the operator closes the dialog', async () => {
    vi.useFakeTimers()
    const handle = await copySensitiveText('S2V1-GGGG')
    const cancel = handle.scheduleClear(500)
    cancel()
    vi.advanceTimersByTime(1000)
    expect(writeText).not.toHaveBeenCalledWith('')
  })

  it('clearClipboardBestEffort never throws when the API rejects', async () => {
    writeText.mockRejectedValue(new Error('denied'))
    expect(() => clearClipboardBestEffort()).not.toThrow()
  })
})
