/**
 * Shared shape for the three-segment overview status strip. The page owns the
 * wording (i18n keys) and the entry action; the strip only renders it so the
 * status definitions cannot drift between the page and the component.
 */
export type OverviewStatusId = 'wallet' | 'security' | 'signer'

export interface OverviewStatusSegment {
  id: OverviewStatusId
  /** True renders the green "available" state; false is the red abnormal state. */
  ok: boolean
  /** i18n key of the segment label (wallet / security monitor / signer). */
  titleKey: string
  /** i18n key explaining the current state, including the abnormal reason. */
  hintKey: string
  /** Optional i18n key of the recovery entry shown only for abnormal segments. */
  actionLabel?: string
}
