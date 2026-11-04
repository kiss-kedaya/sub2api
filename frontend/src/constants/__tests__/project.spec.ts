import { describe, expect, it } from 'vitest'
import { PROJECT_GITHUB_URL } from '../project'

// Every user-facing "GitHub" link must lead to the fork we run, not to upstream.
const userFacingSources = import.meta.glob(
  ['../../views/HomeView.vue', '../../views/KeyUsageView.vue', '../../components/layout/UserMenu.vue'],
  { query: '?raw', import: 'default', eager: true }
) as Record<string, string>

describe('project GitHub link', () => {
  it('points at our own repository over https', () => {
    expect(PROJECT_GITHUB_URL).toBe('https://github.com/kiss-kedaya/sub2api')
  })

  it('is the only GitHub project URL used by the home, key usage and user menu entries', () => {
    const entries = Object.entries(userFacingSources)
    expect(entries).toHaveLength(3)
    for (const [path, source] of entries) {
      expect(source, path).not.toContain('github.com/Wei-Shaw/sub2api')
      expect(source, path).toContain('PROJECT_GITHUB_URL')
    }
  })
})
