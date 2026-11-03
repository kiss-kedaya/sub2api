/** Unknown timer state must keep the destructive-action confirmation enabled. */
export const shouldConfirmImmediateSchedulableToggle = (hasSchedule: boolean | undefined): boolean =>
  hasSchedule !== false
