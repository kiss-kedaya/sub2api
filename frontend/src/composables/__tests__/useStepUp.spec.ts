import { describe, it, expect, vi } from 'vitest'
import {
  useStepUp,
  isStepUpRequired,
  isStepUpBlocked,
  isStepUpUnavailable,
  isStepUpCancelled,
  stepUpBlockReason,
  StepUpCancelledError
} from '../useStepUp'

describe('useStepUp error classification', () => {
  it('detects STEP_UP_REQUIRED from code field', () => {
    expect(isStepUpRequired({ status: 403, code: 'STEP_UP_REQUIRED' })).toBe(true)
    expect(isStepUpRequired({ status: 403, reason: 'STEP_UP_REQUIRED' })).toBe(true)
    expect(isStepUpRequired({ status: 500, code: 'INTERNAL' })).toBe(false)
    expect(isStepUpRequired(null)).toBe(false)
  })

  it('detects blocked (non-retryable) step-up errors', () => {
    expect(isStepUpBlocked({ code: 'STEP_UP_TOTP_NOT_ENABLED' })).toBe(true)
    expect(isStepUpBlocked({ reason: 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' })).toBe(true)
    expect(isStepUpBlocked({ code: 'STEP_UP_REQUIRED' })).toBe(false)
    expect(isStepUpUnavailable({ code: 'STEP_UP_UNAVAILABLE' })).toBe(true)
  })

  it('surfaces the block reason marker', () => {
    expect(stepUpBlockReason({ reason: 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' })).toBe('STEP_UP_ADMIN_API_KEY_FORBIDDEN')
    expect(stepUpBlockReason({ code: 'OTHER' })).toBe('')
  })
})

describe('useStepUp.run', () => {
  it('returns the action result directly on success', async () => {
    const stepUp = useStepUp()
    const result = await stepUp.run(async () => 42)
    expect(result).toBe(42)
    expect(stepUp.visible.value).toBe(false)
  })

  it('re-throws non-step-up errors without prompting', async () => {
    const stepUp = useStepUp()
    const err = { status: 500, code: 'INTERNAL' }
    await expect(stepUp.run(async () => { throw err })).rejects.toBe(err)
    expect(stepUp.visible.value).toBe(false)
  })

  it('re-throws blocked errors without prompting', async () => {
    const stepUp = useStepUp()
    const err = { status: 403, code: 'STEP_UP_TOTP_NOT_ENABLED' }
    await expect(stepUp.run(async () => { throw err })).rejects.toBe(err)
    expect(stepUp.visible.value).toBe(false)
  })

  it('prompts on STEP_UP_REQUIRED and retries after verification', async () => {
    const stepUp = useStepUp()
    let calls = 0
    const action = async () => {
      calls++
      if (calls === 1) throw { status: 403, code: 'STEP_UP_REQUIRED' }
      return 'ok'
    }
    const promise = stepUp.run(action)
    // The dialog should now be open, awaiting verification (after the first rejection is handled).
    await vi.waitFor(() => expect(stepUp.visible.value).toBe(true))
    stepUp.onVerified()
    await expect(promise).resolves.toBe('ok')
    expect(calls).toBe(2)
    expect(stepUp.visible.value).toBe(false)
  })

  it('throws a cancellation sentinel (not the original error) if the user cancels the prompt', async () => {
    const stepUp = useStepUp()
    const err = { status: 403, code: 'STEP_UP_REQUIRED' }
    const promise = stepUp.run(async () => { throw err })
    await vi.waitFor(() => expect(stepUp.visible.value).toBe(true))
    stepUp.onCancel()
    await expect(promise).rejects.toBeInstanceOf(StepUpCancelledError)
    expect(stepUp.visible.value).toBe(false)
  })

  it('classifies the cancellation sentinel distinctly from step-up errors', () => {
    const cancelled = new StepUpCancelledError()
    expect(isStepUpCancelled(cancelled)).toBe(true)
    expect(isStepUpRequired(cancelled)).toBe(false)
    expect(isStepUpBlocked(cancelled)).toBe(false)
    expect(isStepUpCancelled({ status: 403, code: 'STEP_UP_REQUIRED' })).toBe(false)
  })

  it('shares one pending prompt for concurrent sensitive operations', async () => {
    const stepUp = useStepUp()
    const first = stepUp.prompt()
    const second = stepUp.prompt()
    expect(second).toBe(first)
    expect(stepUp.visible.value).toBe(true)
    stepUp.onVerified()
    await expect(Promise.all([first, second])).resolves.toEqual([true, true])
  })

  it('shares one prompt and retries every concurrent run() after verification', async () => {
    const stepUp = useStepUp()
    const failures = new Map<number, number>()
    const action = (id: number) => async () => {
      const calls = (failures.get(id) ?? 0) + 1
      failures.set(id, calls)
      if (calls === 1) throw { status: 403, code: 'STEP_UP_REQUIRED' }
      return id
    }
    const batch = Promise.all([stepUp.run(action(1)), stepUp.run(action(2))])
    await vi.waitFor(() => expect(stepUp.visible.value).toBe(true))
    stepUp.onVerified()
    await expect(batch).resolves.toEqual([1, 2])
    expect(failures.get(1)).toBe(2)
    expect(failures.get(2)).toBe(2)
  })

  it('re-throws STEP_UP_UNAVAILABLE without prompting', async () => {
    const stepUp = useStepUp()
    const err = { status: 503, code: 'STEP_UP_UNAVAILABLE' }
    await expect(stepUp.run(async () => { throw err })).rejects.toBe(err)
    expect(stepUp.visible.value).toBe(false)
    expect(stepUp.blockedReason.value).toBe('STEP_UP_UNAVAILABLE')
  })
})
