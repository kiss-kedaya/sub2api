/**
 * One social sign-in provider rendered by SocialLoginButtons.
 *
 * Multi-colour brand marks (Google, LinuxDo, DingTalk…) are supplied through the
 * component's dynamic slot `#icon-<id>`; single-glyph brands can just pass a
 * UIcon name. The component never builds OAuth requests — the page owns that.
 */
export interface SocialLoginProvider {
  /** Stable id; also the dynamic icon slot name (`#icon-<id>`). */
  id: string
  label: string
  /** Iconify/UIcon name when the brand can be drawn by a single icon. */
  icon?: string
  ariaLabel?: string
  disabled?: boolean
  testId?: string
}
