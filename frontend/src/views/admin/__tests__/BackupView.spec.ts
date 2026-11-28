import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import BackupView from '../BackupView.vue'

const {
  getS3Config,
  getImageStorageConfig,
  getSchedule,
  updateSchedule,
  createBackup,
  listBackups,
  getDownloadURL,
} = vi.hoisted(() => ({
  getS3Config: vi.fn(),
  getImageStorageConfig: vi.fn(),
  getSchedule: vi.fn(),
  updateSchedule: vi.fn(),
  createBackup: vi.fn(),
  listBackups: vi.fn(),
  getDownloadURL: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    backup: {
      getS3Config,
      updateS3Config: vi.fn(),
      testS3Connection: vi.fn(),
      getImageStorageConfig,
      updateImageStorageConfig: vi.fn(),
      testImageStorageConnection: vi.fn(),
      getSchedule,
      updateSchedule,
      createBackup,
      listBackups,
      getBackup: vi.fn(),
      deleteBackup: vi.fn(),
      getDownloadURL,
      restoreBackup: vi.fn(),
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (fn: () => unknown) => fn() }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string, params?: Record<string, unknown>) =>
        params?.index === undefined ? key : `${key}:${params.index}`,
    },
  }),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params?.index === undefined ? key : `${key}:${params.index}`,
  }),
}))

const baseRecord = (id: string, parts?: unknown[]) => ({
  id,
  status: 'completed',
  backup_type: 'postgres',
  file_name: `${id}.sql.gz`,
  s3_key: `backups/${id}.sql.gz`,
  parts,
  size_bytes: 10,
  triggered_by: 'manual',
  started_at: '2026-08-09T00:00:00Z',
})

function mountBackupView() {
  return mount(BackupView, {
    global: {
      stubs: {
        TotpStepUpDialog: true,
      },
    },
  })
}

describe('admin BackupView 分卷备份', () => {
  beforeEach(() => {
    getS3Config.mockResolvedValue({})
    getImageStorageConfig.mockResolvedValue({ config: {}, secret_configured: false })
    getSchedule.mockResolvedValue({ enabled: false, cron_expr: '', retain_days: 14, retain_count: 10 })
    updateSchedule.mockReset()
    createBackup.mockReset()
    getDownloadURL.mockReset()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('显示分卷数并在下载时列出每个分卷链接', async () => {
    listBackups.mockResolvedValue({
      items: [baseRecord('split', [{ index: 1 }, { index: 2 }, { index: 3 }])],
    })
    getDownloadURL.mockResolvedValue({
      parts: [
        { index: 1, size_bytes: 5, url: 'https://example.test/part-1' },
        { index: 2, size_bytes: 6, url: 'https://example.test/part-2' },
        { index: 3, size_bytes: 7, url: 'https://example.test/part-3' },
      ],
    })

    const wrapper = mountBackupView()
    await flushPromises()

    expect(wrapper.text()).toContain('3')
    const downloadButton = wrapper.findAll('button').find(button =>
      button.text().includes('admin.backup.actions.download'),
    )
    expect(downloadButton).toBeDefined()
    await downloadButton!.trigger('click')
    await flushPromises()

    expect(document.body.textContent).toContain('admin.backup.actions.partLabel:1')
    expect(document.body.textContent).toContain('admin.backup.actions.partLabel:3')
    expect(document.body.querySelector('a[href="https://example.test/part-2"]')).not.toBeNull()
  })

  it('旧单文件记录仍使用单个下载地址', async () => {
    listBackups.mockResolvedValue({ items: [baseRecord('legacy')] })
    getDownloadURL.mockResolvedValue({ url: 'https://example.test/legacy.sql.gz' })

    const wrapper = mountBackupView()
    await flushPromises()
    const downloadButton = wrapper.findAll('button').find(button =>
      button.text().includes('admin.backup.actions.download'),
    )
    await downloadButton!.trigger('click')
    await flushPromises()

    expect(getDownloadURL).toHaveBeenCalledWith('legacy')
    expect(document.body.textContent).not.toContain('admin.backup.actions.downloadParts')
  })

  it('保留份数输入框的上限与后端一致', async () => {
    listBackups.mockResolvedValue({ items: [] })

    const wrapper = mountBackupView()
    await flushPromises()

    // 后端记录列表最多 100 条，其中一条留给正在执行的备份，所以保留份数最大 99。
    const bounded = wrapper.findAll('input[type="number"][max="99"]')
    expect(bounded).toHaveLength(1)
    expect((bounded[0].element as HTMLInputElement).value).toBe('10')
    expect(bounded[0].attributes('min')).toBe('0')
  })

  it('运行中的备份不显示删除入口', async () => {
    listBackups.mockResolvedValue({
      items: [{ ...baseRecord('running'), status: 'running', progress: 'uploading' }],
    })

    const wrapper = mountBackupView()
    await flushPromises()

    expect(wrapper.find('tbody tr td:nth-child(5)').text()).toBe('-')
    expect(wrapper.findAll('button').some(button => button.text() === 'common.delete')).toBe(false)
  })

  it('定时备份支持切换并保存只备份关键表开关，并同步到手动备份', async () => {
    listBackups.mockResolvedValue({ items: [] })
    updateSchedule.mockResolvedValue({
      enabled: false,
      cron_expr: '0 2 * * *',
      retain_days: 14,
      retain_count: 10,
      critical_only: true,
    })
    createBackup.mockResolvedValue({
      ...baseRecord('crit-1'),
      status: 'completed',
      scope: 'critical',
      file_name: 'sub2api_20261126_030000.critical.sql.gz',
    })

    const wrapper = mountBackupView()
    await flushPromises()

    const scheduleToggle = wrapper.find('[data-testid="schedule-critical-only-toggle"]')
    expect(scheduleToggle.exists()).toBe(true)
    expect(scheduleToggle.attributes('aria-checked')).toBe('false')

    await scheduleToggle.trigger('click')
    await flushPromises()
    expect(scheduleToggle.attributes('aria-checked')).toBe('true')

    // 点击定时备份卡片的保存按钮（页面中第 3 个 common.save 按钮：S3 / 异步生图 / 定时备份）
    const saveButtons = wrapper.findAll('button').filter(button => button.text().includes('common.save'))
    expect(saveButtons).toHaveLength(3)
    await saveButtons[2].trigger('click')
    await flushPromises()

    expect(updateSchedule).toHaveBeenCalledWith({
      enabled: false,
      cron_expr: '0 2 * * *',
      retain_days: 14,
      retain_count: 10,
      critical_only: true,
      age_recipients: [],
      max_rate_mbps: 0,
    })

    // 保存后手动备份的仅关键表开关同步开启，点击创建备份会传 critical_only: true
    const manualToggle = wrapper.find('[data-testid="manual-critical-only-toggle"]')
    expect(manualToggle.attributes('aria-checked')).toBe('true')

    const createBtn = wrapper.findAll('button').find(button =>
      button.text().includes('admin.backup.operations.createBackup'),
    )
    expect(createBtn).toBeDefined()
    await createBtn!.trigger('click')
    await flushPromises()

    expect(createBackup).toHaveBeenCalledWith({
      expire_days: 14,
      critical_only: true,
    })
    wrapper.unmount()
  })

  it('手动备份开关可独立关闭以执行单次全量备份', async () => {
    listBackups.mockResolvedValue({ items: [] })
    getSchedule.mockResolvedValue({
      enabled: true,
      cron_expr: '0 3 * * *',
      retain_days: 7,
      retain_count: 5,
      critical_only: true,
    })
    createBackup.mockResolvedValue({ ...baseRecord('full-1'), status: 'completed' })

    const wrapper = mountBackupView()
    await flushPromises()

    const manualToggle = wrapper.find('[data-testid="manual-critical-only-toggle"]')
    expect(manualToggle.attributes('aria-checked')).toBe('true')

    await manualToggle.trigger('click')
    await flushPromises()
    expect(manualToggle.attributes('aria-checked')).toBe('false')

    const createBtn = wrapper.findAll('button').find(button =>
      button.text().includes('admin.backup.operations.createBackup'),
    )
    await createBtn!.trigger('click')
    await flushPromises()

    expect(createBackup).toHaveBeenCalledWith({
      expire_days: 14,
      critical_only: false,
    })
    wrapper.unmount()
  })

  it('服务端强制关键表模式时锁定开关并展示服务端提示', async () => {
    listBackups.mockResolvedValue({ items: [] })
    getSchedule.mockResolvedValue({
      enabled: true,
      cron_expr: '0 3 * * *',
      retain_days: 7,
      retain_count: 5,
      critical_only: false,
      server_critical_forced: true,
      age_encryption_enabled: true,
    })

    const wrapper = mountBackupView()
    await flushPromises()

    const scheduleToggle = wrapper.find('[data-testid="schedule-critical-only-toggle"]')
    const manualToggle = wrapper.find('[data-testid="manual-critical-only-toggle"]')
    const manualLabel = wrapper.find('[data-testid="manual-critical-only-label"]')
    expect(scheduleToggle.attributes('aria-checked')).toBe('true')
    expect(scheduleToggle.attributes('disabled')).toBeDefined()
    expect(manualToggle.attributes('aria-checked')).toBe('true')
    expect(manualToggle.attributes('disabled')).toBeDefined()
    expect(manualLabel.attributes('for')).toBe(manualToggle.attributes('id'))
    expect(manualLabel.element.contains(manualToggle.element)).toBe(false)
    expect(wrapper.text()).toContain('admin.backup.schedule.criticalForcedByServer')
    expect(wrapper.text()).toContain('admin.backup.schedule.ageEncryptionActive')
    expect(wrapper.find('[data-testid="schedule-age-status-badge"]').text()).toContain('admin.backup.schedule.ageStatusEnabledLegacy')

    // 锁定态点击两个开关都不得把状态改回 false
    await scheduleToggle.trigger('click')
    await manualToggle.trigger('click')
    await flushPromises()
    expect(scheduleToggle.attributes('aria-checked')).toBe('true')
    expect(manualToggle.attributes('aria-checked')).toBe('true')
  })

  it('关键表或加密备份显示标识并禁用在线恢复按钮，全量明文备份（含特殊前缀路径）仍可恢复', async () => {
    listBackups.mockResolvedValue({
      items: [
        {
          ...baseRecord('full-plain'),
          s3_key: 'backups/.critical.sql.gz/2026/11/26/full-plain.sql.gz',
        },
        {
          ...baseRecord('critical-plain'),
          file_name: 'sub2api_20261126_030000.critical.sql.gz',
          s3_key: 'backups/sub2api_20261126_030000.critical.sql.gz',
        },
        {
          ...baseRecord('encrypted-only'),
          file_name: 'sub2api_20261126_040000.sql.gz.age',
          s3_key: 'backups/sub2api_20261126_040000.sql.gz.age',
          encrypted: true,
        },
      ],
    })

    const wrapper = mountBackupView()
    await flushPromises()

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(3)

    // 第 1 行：全量明文（即便 S3 前缀包含 .critical.sql.gz）-> 无关键表/加密标签，恢复按钮可用
    expect(rows[0].text()).not.toContain('admin.backup.scope.critical')
    expect(rows[0].text()).not.toContain('admin.backup.scope.encrypted')
    const fullRestoreBtn = rows[0].findAll('button').find(b => b.text().includes('admin.backup.actions.restore'))
    expect(fullRestoreBtn).toBeDefined()
    expect(fullRestoreBtn!.attributes('disabled')).toBeUndefined()

    // 第 2 行：关键表（无 scope 标志时靠后缀兜底）-> 显示关键表标签，恢复按钮禁用并带提示
    expect(rows[1].text()).toContain('admin.backup.scope.critical')
    const criticalRestoreBtn = rows[1].findAll('button').find(b => b.text().includes('admin.backup.actions.restore'))
    expect(criticalRestoreBtn).toBeDefined()
    expect(criticalRestoreBtn!.attributes('disabled')).toBeDefined()
    expect(criticalRestoreBtn!.attributes('title')).toBe('admin.backup.actions.restoreManualOnly')

    // 第 3 行：加密 -> 显示已加密标签，恢复按钮禁用
    expect(rows[2].text()).toContain('admin.backup.scope.encrypted')
    const encryptedRestoreBtn = rows[2].findAll('button').find(b => b.text().includes('admin.backup.actions.restore'))
    expect(encryptedRestoreBtn).toBeDefined()
    expect(encryptedRestoreBtn!.attributes('disabled')).toBeDefined()
  })

  it('支持在后台配置 age 公钥和导出限速，并本地拦截私钥粘贴', async () => {
    listBackups.mockResolvedValue({ items: [] })
    const validKey = `age1${'q'.repeat(58)}`
    updateSchedule.mockResolvedValue({
      enabled: false,
      cron_expr: '0 2 * * *',
      retain_days: 14,
      retain_count: 10,
      critical_only: false,
      age_recipients: [validKey],
      max_rate_mbps: 15,
      age_encryption_enabled: true,
    })

    const wrapper = mountBackupView()
    await flushPromises()

    const ageTextarea = wrapper.find('[data-testid="schedule-age-recipients-input"] textarea')
    const textareaEl = ageTextarea.exists() ? ageTextarea : wrapper.find('[data-testid="schedule-age-recipients-input"]')
    const rateInput = wrapper.find('[data-testid="schedule-max-rate-input"] input')
    const rateEl = rateInput.exists() ? rateInput : wrapper.find('[data-testid="schedule-max-rate-input"]')
    const saveButtons = wrapper.findAll('button').filter(button => button.text().includes('common.save'))
    expect(saveButtons).toHaveLength(3)

    // 整份粘贴 age-keygen 输出（含前两行注释 + 第 3 行私钥）时立即展示私钥拦截告警并禁止提交
    await textareaEl.setValue(`# created: 2026-11-27T00:00:00Z\n# public key: ${validKey}\nAGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ`)
    await flushPromises()
    expect(wrapper.find('[data-testid="schedule-age-error-alert"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.backup.schedule.agePrivateKeyForbidden')
    expect(saveButtons[2].attributes('disabled')).toBeDefined()
    await saveButtons[2].trigger('click')
    await flushPromises()
    expect(updateSchedule).not.toHaveBeenCalled()

    // 粘贴合法 age1... 公钥但尚未点击保存时：状态仍为「未加密」+「待保存」，不得提前伪报「已启用」
    await textareaEl.setValue(`  ${validKey}  \n\n${validKey}`)
    await rateEl.setValue('15')
    await flushPromises()
    expect(wrapper.find('[data-testid="schedule-age-error-alert"]').exists()).toBe(false)
    expect(saveButtons[2].attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="schedule-age-status-badge"]').text()).toContain('admin.backup.schedule.ageStatusDisabled')
    expect(wrapper.find('[data-testid="schedule-age-unsaved-badge"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.backup.schedule.ageEncryptionActive')

    await saveButtons[2].trigger('click')
    await flushPromises()
    expect(updateSchedule).toHaveBeenCalledWith({
      enabled: false,
      cron_expr: '0 2 * * *',
      retain_days: 14,
      retain_count: 10,
      critical_only: false,
      age_recipients: [validKey],
      max_rate_mbps: 15,
    })
    expect(wrapper.find('[data-testid="schedule-age-status-badge"]').text()).toContain('admin.backup.schedule.ageStatusEnabled')
    expect(wrapper.find('[data-testid="schedule-age-unsaved-badge"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('服务端强制开启 age 加密时展示只读提示', async () => {
    listBackups.mockResolvedValue({ items: [] })
    getSchedule.mockResolvedValue({
      enabled: true,
      cron_expr: '0 3 * * *',
      retain_days: 7,
      retain_count: 5,
      critical_only: true,
      age_encryption_enabled: true,
      server_age_encryption_forced: true,
      server_age_recipient_count: 2,
    })

    const wrapper = mountBackupView()
    await flushPromises()

    expect(wrapper.find('[data-testid="schedule-age-server-forced-alert"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="schedule-age-status-badge"]').text()).toContain('admin.backup.schedule.ageStatusServerForced')
    wrapper.unmount()
  })
})
