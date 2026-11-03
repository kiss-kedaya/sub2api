import { describe, expect, it } from 'vitest'
import { shouldConfirmImmediateSchedulableToggle } from '../schedulableTimerState'

describe('immediate schedulable toggle confirmation', () => {
  it('skips confirmation only when the account is known to have no timer', () => {
    expect(shouldConfirmImmediateSchedulableToggle(false)).toBe(false)
    expect(shouldConfirmImmediateSchedulableToggle(true)).toBe(true)
    expect(shouldConfirmImmediateSchedulableToggle(undefined)).toBe(true)
  })
})
