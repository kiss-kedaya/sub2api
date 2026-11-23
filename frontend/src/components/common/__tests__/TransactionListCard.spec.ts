import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TransactionListCard from '@/components/common/TransactionListCard.vue'
import type { TransactionItem } from '@/components/common/transactionList'

const income: TransactionItem = {
  id: 'credited',
  type: 'chain',
  title: 'On-chain deposit · BSC (BEP20)',
  subtitle: '10 USDT · 2026-01-01',
  amount: '67',
  amountPrefix: '$',
  tone: 'positive'
}

const expense: TransactionItem = {
  id: 'usage',
  type: 'usage',
  title: 'API usage',
  subtitle: '2.4M tokens',
  amount: '4.20',
  amountPrefix: '$',
  tone: 'negative'
}

const reversal: TransactionItem = {
  id: 'revoked',
  type: 'reversal',
  title: 'Deposit reversed · BSC (BEP20)',
  subtitle: '12 USDT',
  amount: '6.70',
  amountPrefix: '$',
  tone: 'reversal',
  status: { label: 'Rolled back', color: 'error', testId: 'status-revoked' }
}

const pending: TransactionItem = {
  id: 'pending',
  type: 'deposit',
  title: 'On-chain deposit · Base',
  subtitle: '20 USDC',
  tone: 'pending',
  status: { label: 'Confirming', color: 'warning' }
}

function mountCard(props: Record<string, unknown> = {}, slots: Record<string, unknown> = {}) {
  return mount(TransactionListCard, {
    props: {
      title: 'Recent transactions',
      subtitle: 'Updates every 5 seconds',
      items: [],
      ...props
    },
    slots: slots as never
  })
}

describe('TransactionListCard', () => {
  it('renders the title, subtitle and each row with its tone and amount', () => {
    const wrapper = mountCard({ items: [income, expense, reversal] })

    expect(wrapper.text()).toContain('Recent transactions')
    expect(wrapper.text()).toContain('Updates every 5 seconds')

    const rows = wrapper.findAll('[data-testid="transaction-row"]')
    expect(rows).toHaveLength(3)
    expect(rows.map(row => row.attributes('data-tone'))).toEqual(['positive', 'negative', 'reversal'])

    expect(rows[0].get('[data-testid="transaction-amount"]').text()).toBe('+$67')
    expect(rows[0].get('[data-testid="transaction-amount"]').classes()).toContain('amount-positive')
    expect(rows[1].get('[data-testid="transaction-amount"]').text()).toBe('-$4.20')
    expect(rows[1].get('[data-testid="transaction-amount"]').classes()).toContain('amount-negative')
    // 回滚是「曾经到账、又被扣回」，颜色和负号都要和普通支出区分。
    expect(rows[2].get('[data-testid="transaction-amount"]').text()).toBe('-$6.70')
    expect(rows[2].get('[data-testid="transaction-amount"]').classes()).toContain('amount-reversal')
    expect(rows[2].get('[data-testid="status-revoked"]').text()).toBe('Rolled back')
  })

  it('shows the icon plate mapped from the record type, with tone colour', () => {
    const wrapper = mountCard({ items: [income, pending] })
    const icons = wrapper.findAll('[data-testid="transaction-icon"]')
    expect(icons[0].classes()).toContain('icon-orb-positive')
    expect(icons[1].classes()).toContain('icon-orb-pending')
    expect(wrapper.find('svg, span.iconify').exists()).toBe(true)
  })

  it('renders an em dash for a row without a trustworthy amount', () => {
    const wrapper = mountCard({ items: [pending] })
    expect(wrapper.get('[data-testid="transaction-amount"]').text()).toBe('—')
  })

  it('renders a custom status test id and label when the row has one', () => {
    const wrapper = mountCard({
      items: [{ ...income, status: { label: 'Amount under review', color: 'warning', testId: 'credit-unknown' } }]
    })
    expect(wrapper.get('[data-testid="credit-unknown"]').text()).toBe('Amount under review')
  })

  it('forwards the item-details slot for page-specific lines', () => {
    const wrapper = mountCard({ items: [reversal] }, {
      'item-details': '<p data-testid="row-detail">Reason: reorg</p>'
    })
    expect(wrapper.get('[data-testid="row-detail"]').text()).toBe('Reason: reorg')
  })

  it('shows skeleton rows instead of records while loading', () => {
    const wrapper = mountCard({ items: [income], loading: true, skeletonRows: 2, loadingLabel: 'Loading records' })
    const skeletons = wrapper.get('[data-testid="transaction-list-loading"]')
    expect(skeletons.attributes('aria-busy')).toBe('true')
    expect(skeletons.attributes('aria-label')).toBe('Loading records')
    expect(skeletons.findAll('li')).toHaveLength(2)
    expect(wrapper.find('[data-testid="transaction-row"]').exists()).toBe(false)
  })

  it('shows the empty state with its own test id and an action slot', async () => {
    const onReset = vi.fn()
    const wrapper = mountCard(
      {
        items: [],
        emptyTitle: 'No records yet',
        emptyDescription: 'Your transfers appear here.',
        emptyTestId: 'no-filtered-records'
      },
      { 'empty-actions': '<button data-testid="reset">Reset</button>' }
    )

    const empty = wrapper.get('[data-testid="no-filtered-records"]')
    expect(empty.text()).toContain('No records yet')
    expect(empty.text()).toContain('Your transfers appear here.')
    await wrapper.get('[data-testid="reset"]').trigger('click')
    expect(onReset).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="transaction-row"]').exists()).toBe(false)
  })

  it('renders header actions and the toolbar slot', () => {
    const wrapper = mountCard({ items: [income] }, {
      actions: '<button data-testid="refresh">Refresh</button>',
      toolbar: '<div data-testid="filters">Filters</div>'
    })
    expect(wrapper.get('[data-testid="refresh"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="filters"]').text()).toBe('Filters')
  })

  it('emits select only when the list is interactive', async () => {
    const wrapper = mountCard({ items: [income], interactive: true })
    await wrapper.get('[data-testid="transaction-row"]').trigger('click')
    expect(wrapper.emitted('select')?.[0]?.[0]).toEqual(income)
  })

  it('keeps rows inert by default', async () => {
    const wrapper = mountCard({ items: [income] })
    await wrapper.get('[data-testid="transaction-row"]').trigger('click')
    expect(wrapper.emitted('select')).toBeUndefined()
  })
})
