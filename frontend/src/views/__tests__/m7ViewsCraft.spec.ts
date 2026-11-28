import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const srcRoot = resolve(__dirname, '../..')

const M7_VIEWS = [
  'views/auth/DingTalkCallbackView.vue',
  'views/auth/DingTalkEmailCompletionView.vue',
  'views/auth/EmailVerifyView.vue',
  'views/auth/ForgotPasswordView.vue',
  'views/auth/LinuxDoCallbackView.vue',
  'views/auth/LoginView.vue',
  'views/auth/OAuthCallbackView.vue',
  'views/auth/OidcCallbackView.vue',
  'views/auth/RegisterView.vue',
  'views/auth/ResetPasswordView.vue',
  'views/auth/WechatCallbackView.vue',
  'views/auth/WechatPaymentCallbackView.vue',
  'views/public/LegalDocumentView.vue',
  'views/setup/SetupWizardView.vue',
  'views/HomeView.vue',
  'views/KeyUsageView.vue',
  'views/NotFoundView.vue',
  'views/ModelPlazaView.vue',
  'views/TicketsView.vue',
  'views/TrafficView.vue',
] as const

const FADE_AUTH_VIEWS = [
  'views/auth/DingTalkCallbackView.vue',
  'views/auth/ForgotPasswordView.vue',
  'views/auth/LinuxDoCallbackView.vue',
  'views/auth/OidcCallbackView.vue',
  'views/auth/RegisterView.vue',
  'views/auth/ResetPasswordView.vue',
  'views/auth/WechatCallbackView.vue',
] as const

describe('M7 UI/UX craft invariants', () => {
  it.each(M7_VIEWS)('%s avoids legacy gray/dark/slate/zinc tokens, 100vh/min-h-screen, break-all, raw <select>, autofocus, and watch array traps', (relPath) => {
    const content = readFileSync(resolve(srcRoot, relPath), 'utf8')
    expect(content).not.toMatch(/\b(?:text|bg|border|divide|from|to|via|ring)-(?:gray|dark|slate|zinc)-/)
    expect(content).not.toContain('min-h-screen')
    expect(content).not.toContain('100vh')
    expect(content).not.toContain('break-all')
    expect(content).not.toContain('#14b8a6')
    expect(content).not.toMatch(/<select[\s>]/)
    expect(content).not.toMatch(/<input[^>]*\bautofocus\b/)
    expect(content).not.toMatch(/watch\(\(\)\s*=>\s*\[/)
    expect(content).not.toMatch(/transition\s*:[^;]*box-shadow/)

    const spinLines = content
      .split(/\r?\n/)
      .filter((line) => line.includes('animate-spin'))
    for (const line of spinLines) {
      expect(line).toContain('motion-reduce:animate-none')
    }
  })

  it('synchronizes STEP_EXIT_MS = 150 with auth-quiet.css in LoginView and RegisterView', () => {
    const loginSource = readFileSync(resolve(srcRoot, 'views/auth/LoginView.vue'), 'utf8')
    const registerSource = readFileSync(resolve(srcRoot, 'views/auth/RegisterView.vue'), 'utf8')
    expect(loginSource).toContain('const STEP_EXIT_MS = 150')
    expect(registerSource).toContain('const STEP_EXIT_MS = 150')
    expect(registerSource).toContain("'input-error': invitationValidation.invalid")
    expect(registerSource).toContain("'input-error': promoValidation.invalid")
    expect(registerSource).toContain('ring-1 ring-inset ring-emerald-500')
  })

  it.each(FADE_AUTH_VIEWS)('%s uses fast ease-out .fade-* transitions with reduced-motion fallback', (relPath) => {
    const content = readFileSync(resolve(srcRoot, relPath), 'utf8')
    expect(content).toContain('180ms var(--ease-out')
    expect(content).toContain('140ms var(--ease-out')
    expect(content).toContain('@media (prefers-reduced-motion: reduce)')
    expect(content).toContain('120ms var(--ease-out')
  })

  it('preserves absence of .bg-red-50 in OAuthCallbackView and WechatPaymentCallbackView', () => {
    for (const relPath of ['views/auth/OAuthCallbackView.vue', 'views/auth/WechatPaymentCallbackView.vue']) {
      const content = readFileSync(resolve(srcRoot, relPath), 'utf8')
      expect(content).not.toContain('bg-red-50')
      expect(content).toContain('min-h-dvh')
    }
    const oauthContent = readFileSync(resolve(srcRoot, 'views/auth/OAuthCallbackView.vue'), 'utf8')
    expect(oauthContent.match(/class="input min-w-0 flex-1 /g)).toHaveLength(3)
  })

  it('uses min-h-dvh and reduced-motion aligned SHELL_TRANSITION_MS across ModelPlazaView and App.vue', () => {
    const plazaSource = readFileSync(resolve(srcRoot, 'views/ModelPlazaView.vue'), 'utf8')
    const appSource = readFileSync(resolve(srcRoot, 'App.vue'), 'utf8')
    expect(plazaSource).toContain('min-h-dvh')
    expect(appSource).toContain('PREFERS_REDUCED_MOTION ? { enter: 120, leave: 120 } : { enter: 150, leave: 80 }')
  })
})
