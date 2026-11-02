// @vitest-environment node
import { createSSRApp, defineComponent, h, nextTick } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'
import DataTable from '../DataTable.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const ServerTable = defineComponent({
  name: 'UTable',
  setup: () => () => h('table', { 'data-testid': 'ssr-table' }),
})

describe('DataTable server rendering', () => {
  it.each([0, 100])('renders %i rows without touching browser event targets', async (count) => {
    expect(typeof window).toBe('undefined')
    expect(typeof document).toBe('undefined')
    const errors: unknown[] = []
    const app = createSSRApp(DataTable, {
      columns: [{ key: 'name', label: 'Name', sortable: true }],
      data: Array.from({ length: count }, (_, id) => ({ id, name: `Row ${id}` })),
      virtualizeThreshold: 20,
      loading: count === 0,
    })
    app.component('UTable', ServerTable)
    app.config.errorHandler = (error) => errors.push(error)
    const html = await renderToString(app)
    // The old immediate async viewport watcher crashed after nextTick().
    await nextTick()
    await nextTick()
    expect(errors).toEqual([])
    expect(html).toContain('<table')
  })
})
