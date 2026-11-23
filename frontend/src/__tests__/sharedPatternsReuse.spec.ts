import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

/**
 * Guard: the three shared UI patterns must stay wired into the pages that
 * motivated them, and the copies they replaced must not creep back.
 *
 * A component nobody renders is a demo, not a refactor — these assertions read
 * the actual production sources, not the component files alone.
 */
const sources = import.meta.glob('../**/*.{ts,vue}', {
  query: '?raw',
  import: 'default',
  eager: true
}) as Record<string, string>

// CSS is read from disk: Vite hands `.css?raw` to the runner as an empty module.
// Vitest runs with the frontend package as cwd (see package.json scripts).
const styleCss = readFileSync('src/style.css', 'utf8')
const themeCss = readFileSync('src/styles/nuxt-ui-theme.css', 'utf8')

const production = Object.entries(sources)
  .filter(([path]) => !path.includes('/__tests__/') && !/\.(spec|test)\.ts$/.test(path))

function read(path: string): string {
  const match = production.find(([source]) => source.endsWith('/' + path))
  if (!match) throw new Error(`source not found: ${path}`)
  return match[1]
}

describe('shared pattern wiring', () => {
  it('renders TransactionListCard from the deposit history area with mapped rows', () => {
    const deposit = read('views/user/DepositView.vue')
    expect(deposit).toContain('<TransactionListCard')
    expect(deposit).toContain(':items="historyItems"')
    // 状态/原因/回滚入口还在，只是搬进了共享列表的明细插槽。
    expect(deposit).toContain('#item-details')
    expect(deposit).toContain('deposit-revoke-reason')
  })

  it('reuses the bare TransactionList in the admin balance history dialog', () => {
    const modal = read('components/admin/user/UserBalanceHistoryModal.vue')
    expect(modal).toContain('<TransactionList')
    expect(modal).toContain('transactionItems')
  })

  it('renders VerificationCodeInput from the email verification card', () => {
    const card = read('components/auth/EmailVerifyCard.vue')
    expect(card).toContain('<VerificationCodeInput')
    expect(card).toContain(':auto-submit="canAutoSubmitCode"')
  })

  it.each([
    'components/auth/EmailOAuthButtons.vue',
    'components/auth/LinuxDoOAuthSection.vue',
    'components/auth/DingTalkOAuthSection.vue',
    'components/auth/WechatOAuthSection.vue',
    'components/auth/OidcOAuthSection.vue'
  ])('renders SocialLoginButtons from %s', (path) => {
    expect(read(path)).toContain('<SocialLoginButtons')
  })

  it('keeps the social sections free of the old hand-written button markup', () => {
    const socialSections = [
      read('components/auth/EmailOAuthButtons.vue'),
      read('components/auth/LinuxDoOAuthSection.vue'),
      read('components/auth/DingTalkOAuthSection.vue'),
      read('components/auth/WechatOAuthSection.vue'),
      read('components/auth/OidcOAuthSection.vue')
    ]
    for (const source of socialSections) {
      expect(source).not.toContain('btn btn-secondary')
      expect(source).not.toContain('bg-gray-200 dark:bg-dark-700')
    }
  })
})

describe('shared semantic classes are the single owner of each pattern', () => {
  it('does not keep the duplicated icon-plate literal anywhere', () => {
    const copies = production
      .filter(([, source]) => source.includes('p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25'))
      .map(([path]) => path)
    expect(copies).toEqual([])
  })

  it('uses the amount / surface / icon-plate classes from the pages that own them', () => {
    expect(read('views/admin/PromoCodesView.vue')).toContain('amount-positive')
    expect(read('views/admin/affiliates/AdminAffiliateRecordsTable.vue')).toContain('amount-positive')
    expect(read('views/user/AffiliateView.vue')).toContain('surface-card')
    expect(read('components/auth/EmailVerifyCard.vue')).toContain('icon-orb-primary')
    expect(read('components/common/TransactionList.vue')).toContain('record-divide')
  })

  it('declares the tokens and utilities that back the classes', () => {
    for (const utility of ['surface-card', 'record-divide', 'icon-orb', 'amount-positive', 'amount-negative', 'amount-reversal']) {
      expect(styleCss).toContain(`@utility ${utility}`)
    }
    for (const token of ['--app-amount-positive', '--app-amount-negative', '--app-amount-reversal', '--app-control-border']) {
      expect(themeCss).toContain(token)
    }
  })
})
