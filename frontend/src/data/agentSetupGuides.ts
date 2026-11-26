import { resolveDocsOrigin } from '@/data/apiDocs'
import { apiSiteRoot } from '@/utils/apiBaseUrl'
import { CC_SWITCH_APPS, type CcSwitchApp } from '@/utils/ccswitchImport'
import type { GroupPlatform } from '@/types'

export type AgentWorkflow = 'terminal' | 'ide' | 'extension' | 'desktop'
export type AgentFilterKey = 'all' | 'ccswitch' | AgentWorkflow
export type AgentShellOs = 'unix' | 'powershell' | 'cmd'

export interface AgentCodeSnippet {
  label: string
  path?: string
  code: string
  note?: string
}

export interface AgentSetupStep {
  title: string
  description: string
  snippets?: Partial<Record<AgentShellOs, AgentCodeSnippet[]>>
  genericSnippets?: AgentCodeSnippet[]
  links?: { label: string; href?: string; to?: string }[]
}

export interface AgentAccordionItem {
  question: string
  answer: string
}

export interface AgentCcSwitchProfile {
  supported: boolean
  oneClickInstallInApp: boolean
  appIds: CcSwitchApp[]
  appLabel: string
  sidebarName: string
  installNote: string
  endpoint: string
  protocolLabel: string
  defaultModel: string
  importHint: string
}

export interface AgentGuide {
  id: string
  name: string
  vendor: string
  iconKey: string
  platform?: GroupPlatform
  summary: string
  description: string
  workflows: AgentWorkflow[]
  recommended?: boolean
  openSource?: boolean
  siteGroupAdvice: string
  protocolLabel: string
  endpointPath: string
  baseUrl: string
  baseUrlRule: string
  defaultModel: string
  modelPresets: string[]
  officialDocsUrl: string
  launchCommand: string
  firstLaunchNote: string
  ccSwitch: AgentCcSwitchProfile
  manualSteps: AgentSetupStep[]
  verifyCommands: Partial<Record<AgentShellOs, string>>
  examplePrompts: string[]
  tips: string[]
  faq: AgentAccordionItem[]
  troubleshooting: AgentAccordionItem[]
}

export interface SiteWorkflowStep {
  step: string
  title: string
  where: string
  description: string
  tip?: string
}

export interface AgentConceptCard {
  title: string
  description: string
}

export interface AgentTradeoffItem {
  left: string
  right: string
  summary: string
}

export interface AgentSetupCatalog {
  origin: string
  v1BaseUrl: string
  ccSwitchUrl: string
  ccSwitchRepoUrl: string
  ccSwitchApps: readonly CcSwitchApp[]
  siteWorkflowSteps: SiteWorkflowStep[]
  agents: AgentGuide[]
  keyConcepts: AgentConceptCard[]
  tradeoffs: AgentTradeoffItem[]
  globalTroubleshooting: AgentAccordionItem[]
}

export interface HomeAgentHighlight {
  id: string
  name: string
  vendor: string
  iconKey: string
  platform?: GroupPlatform
  summary: string
  siteGroupAdvice: string
  badge: string
  supportsCcSwitch: boolean
}

export const CC_SWITCH_HOME_ZH = 'https://ccswitch.io/zh/'
export const CC_SWITCH_HOME_EN = 'https://ccswitch.io/en/'
export const CC_SWITCH_REPO_URL = 'https://github.com/farion1231/cc-switch'

export function resolveAgentGatewayOrigin(configuredBaseUrl: string | null | undefined, browserOrigin: string): string {
  const configuredRoot = apiSiteRoot(configuredBaseUrl)
  if (configuredRoot) {
    const resolved = resolveDocsOrigin(configuredRoot)
    if (resolved !== 'http://localhost' || /^https?:\/\/localhost(?::\d+)?(?:\/|$)/i.test(configuredRoot)) {
      return configuredRoot
    }
  }
  return resolveDocsOrigin(browserOrigin)
}

export function getAgentSetupCatalog(
  locale: string,
  originValue: string,
  modelOverride = '',
): AgentSetupCatalog {
  const zh = locale.toLowerCase().startsWith('zh')
  const l = (cn: string, en: string) => (zh ? cn : en)
  const origin = resolveAgentGatewayOrigin(originValue, originValue)
  const v1Base = `${origin}/v1`
  const customModel = modelOverride.trim()
  const pickModel = (fallback: string) => customModel || fallback
  const ccSwitchUrl = zh ? CC_SWITCH_HOME_ZH : CC_SWITCH_HOME_EN

  const claudeModel = pickModel('YOUR_MODEL_ID')
  const codexModel = pickModel('YOUR_MODEL_ID')
  const geminiModel = pickModel('gemini-2.5-pro')
  const grokModel = pickModel('grok-4.6')
  const openCodeModel = pickModel('YOUR_MODEL_ID')

  const siteWorkflowSteps: SiteWorkflowStep[] = [
    {
      step: '01',
      title: l('在 CC Switch「应用」页一键安装智能体', 'Install your agent inside CC Switch → Apps'),
      where: l('CC Switch 左下角 → 应用', 'CC Switch bottom-left → Apps'),
      description: l(
        '安装并打开 CC Switch 后，点左下角「应用」；在「未安装」列表找到想用的智能体（如 Claude Code、Codex、Gemini CLI、Grok Build、OpenCode、Hermes、MiniMax Code、Kimi Code 等），直接点右侧「安装」并打开开关显示在左侧栏——无需自己找安装命令。',
        'Open CC Switch and click Apps in the bottom-left corner. Find the agent you want, click Install, and toggle it on so it appears in the sidebar—no manual install scripts needed.',
      ),
      tip: l('已安装的工具会自动显示本地路径与版本号；未安装的工具点击「安装」会自动运行官方安装脚本或 npm。', 'Installed agents are detected automatically; uninstalled ones run their official installer or npm in one click.'),
    },
    {
      step: '02',
      title: l('在本站选好分组并创建 API 密钥', 'Choose groups & create an API key on this site'),
      where: l('本站控制台 → API 密钥 → 创建密钥', 'Site Console → API Keys → Create Key'),
      description: l(
        '先到「模型广场 / 可用渠道」确认你想用的模型属于哪个分组，再到「API 密钥」新建密钥。只用一款工具可直接选对应平台分组；想一把密钥同时配好 Claude Code、Codex、OpenCode 等多款 Agent，强烈推荐开启「智能路由」按顺序绑定多个分组。',
        'Check Model Plaza / Available Channels for the models you need, then create a key under API Keys. Pick a single group, or enable Smart Routing across multiple groups so one key powers multiple agents.',
      ),
      tip: l('可在创建时一并设置总额度、5h/1d/7d 限额和过期时间，防止 Agent 跑飞产生意外花费。', 'Optionally set spend caps (total / 5h / 1d / 7d) and expiry when creating the key.'),
    },
    {
      step: '03',
      title: l('在密钥回执弹窗点击「导入到 CCS」', 'Click "Import to CCS" in the key receipt dialog'),
      where: l('本站密钥创建成功弹窗 / 「查看密钥」回执', 'Key created receipt / "Reveal Key" dialog'),
      description: l(
        '密钥创建后会弹出明文回执（老密钥可点「查看密钥」输入密码解密打开回执），直接点击「导入到 CCS」：选目标应用、确认模型（弹窗会自动用当前密钥拉取 /v1/models 可用列表），点确认即可自动唤起 CC Switch，一次性写入端点、密钥与 30 分钟余额查询脚本。',
        'Right after creation (or after revealing a saved key), click "Import to CCS". Pick the target app and model (loaded live from /v1/models), then confirm—ccswitch:// writes the endpoint, API key, and balance script automatically.',
      ),
      tip: l('同一把智能路由密钥可以重复点「导入到 CCS」，分别导入给 Claude Code、Codex、Gemini CLI 等多个应用。', 'You can click "Import to CCS" multiple times on the same smart-routing key to configure several agents.'),
    },
    {
      step: '04',
      title: l('在 CC Switch 点击「启用」并开始使用', 'Activate in CC Switch & launch your agent'),
      where: l('CC Switch 左侧目标应用 → 供应商列表 → 启用', 'CC Switch sidebar app → Provider row → Activate'),
      description: l(
        '切回 CC Switch 左侧刚导入的应用，点击本站配置右侧的「启用」（变为「使用中」）。新开一个终端窗口输入启动命令（如 claude、codex、gemini、grok、opencode）即可对话；随时可在本站「使用记录」查看真实扣费与缓存命中。',
        'Switch to the app in CC Switch’s sidebar and click Activate next to this site’s provider. Open a fresh terminal and run the agent command (claude, codex, gemini, etc.). Check Usage Records on this site for real-time spend and cache hits.',
      ),
      tip: l('如果终端里之前手动 export 过旧的环境变量，重开新终端窗口再启动即可生效。', 'Open a new terminal window if an older shell had manual environment variables exported.'),
    },
  ]

  const agents: AgentGuide[] = [
    {
      id: 'claude-code',
      name: 'Claude Code',
      vendor: 'Anthropic',
      iconKey: 'anthropic',
      platform: 'anthropic',
      recommended: true,
      summary: l(
        'Anthropic 官方终端编程 Agent。在 CC Switch「应用」一键安装后，从本站密钥回执点「导入到 CCS」即可开用。',
        'Anthropic’s terminal coding agent. Install inside CC Switch → Apps, then push config from this site with "Import to CCS".',
      ),
      description: l(
        'Claude Code 走 Anthropic Messages 协议（/v1/messages），原生支持 Prompt 缓存。装好 CC Switch 后，直接在其「应用」页一键安装 Claude Code，再从本站「API 密钥」一键导入，几秒内完成配置。',
        'Claude Code uses Anthropic Messages (/v1/messages) with native prompt caching. Install it from CC Switch → Apps and push your gateway key in one click via "Import to CCS".',
      ),
      workflows: ['terminal', 'extension'],
      siteGroupAdvice: l('Anthropic 分组，或包含 Anthropic 的「智能路由」密钥', 'Anthropic group, or a Smart Routing key containing Anthropic'),
      protocolLabel: 'Anthropic Messages',
      endpointPath: '/v1/messages',
      baseUrl: origin,
      baseUrlRule: l('CCS 一键导入自动填裸域名；即使手动填成 /v1，本站网关也会自动折叠重复前缀。', 'CCS writes the bare origin automatically; even if /v1 is added manually, this gateway folds duplicates.'),
      defaultModel: 'claude-sonnet-4-6',
      modelPresets: ['YOUR_MODEL_ID', 'claude-sonnet-4-6', 'claude-opus-4-6', 'claude-sonnet-5-5', 'claude-opus-5-5'],
      officialDocsUrl: 'https://docs.anthropic.com/en/docs/claude-code',
      launchCommand: 'claude',
      firstLaunchNote: l('首次启动如果弹出 Anthropic 登录引导，保持 CC Switch 处于「使用中」并新开终端后即可直接走中转密钥，无需官方账号。', 'With CC Switch active and a fresh terminal, Claude Code authenticates via the imported key without needing an Anthropic account.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['claude'],
        appLabel: 'Claude Code (app=claude)',
        sidebarName: 'Claude Code',
        installNote: l('在 CC Switch 左下角「应用」找到 Claude Code 点击「安装」，安装完成后打开右侧开关显示在左侧栏。', 'In CC Switch → Apps, click Install next to Claude Code and enable its sidebar toggle.'),
        endpoint: origin,
        protocolLabel: 'Anthropic Messages',
        defaultModel: 'claude-sonnet-4-6',
        importHint: l(
          '在控制台「API 密钥」新建或查看密钥 → 点击「导入到 CCS」→ 应用选 Claude Code → 从下拉列表选择本密钥可用模型（默认跟随平台），确认后自动写入端点、密钥、CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC 及余额查询脚本。',
          'In Console → API Keys, click "Import to CCS" → Claude Code → choose an allowed model from the live list to write endpoint, key, and balance tracking automatically.',
        ),
      },
      manualSteps: [
        {
          title: l('从本站控制台直接复制配置（最快）', 'Copy generated config from Console → Use Key'),
          description: l(
            '如果你在无桌面 Linux 服务器或不装 CC Switch，可在控制台「API 密钥」找到密钥打开「使用密钥 → Claude Code」，站内会直接生成带密钥的环境变量与 settings.json；下方为通用模板。',
            'On headless Linux servers or without CC Switch, Console → API Keys → "Use Key" generates ready-to-paste snippets with your key; generic templates are below.',
          ),
          snippets: {
            unix: [
              {
                label: 'Terminal (临时生效)',
                code: [
                  `export ANTHROPIC_BASE_URL="${origin}"`,
                  'export ANTHROPIC_AUTH_TOKEN="YOUR_API_KEY"',
                  ...(claudeModel !== 'YOUR_MODEL_ID' ? [`export ANTHROPIC_MODEL="${claudeModel}"`] : []),
                  'export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1',
                ].join('\n'),
              },
              {
                label: '~/.claude/settings.json',
                path: '~/.claude/settings.json',
                code: JSON.stringify(
                  {
                    $schema: 'https://json.schemastore.org/claude-code-settings.json',
                    env: {
                      ANTHROPIC_BASE_URL: origin,
                      ANTHROPIC_AUTH_TOKEN: 'YOUR_API_KEY',
                      ...(claudeModel !== 'YOUR_MODEL_ID' ? { ANTHROPIC_MODEL: claudeModel } : {}),
                      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
                    },
                  },
                  null,
                  2,
                ),
                note: l('保存后重开终端或 VS Code 即生效；VS Code 的 Claude Code 扩展会共用此文件。', 'Restart your terminal or VS Code after saving; the VS Code extension reads this file too.'),
              },
            ],
            powershell: [
              {
                label: 'PowerShell',
                code: [
                  `$env:ANTHROPIC_BASE_URL="${origin}"`,
                  '$env:ANTHROPIC_AUTH_TOKEN="YOUR_API_KEY"',
                  ...(claudeModel !== 'YOUR_MODEL_ID' ? [`$env:ANTHROPIC_MODEL="${claudeModel}"`] : []),
                  '$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1',
                ].join('\n'),
              },
              {
                label: '%USERPROFILE%\\.claude\\settings.json',
                path: '%USERPROFILE%\\.claude\\settings.json',
                code: JSON.stringify(
                  {
                    $schema: 'https://json.schemastore.org/claude-code-settings.json',
                    env: {
                      ANTHROPIC_BASE_URL: origin,
                      ANTHROPIC_AUTH_TOKEN: 'YOUR_API_KEY',
                      ...(claudeModel !== 'YOUR_MODEL_ID' ? { ANTHROPIC_MODEL: claudeModel } : {}),
                      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
                    },
                  },
                  null,
                  2,
                ),
              },
            ],
            cmd: [
              {
                label: 'Command Prompt',
                code: [
                  `set ANTHROPIC_BASE_URL=${origin}`,
                  'set ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY',
                  ...(claudeModel !== 'YOUR_MODEL_ID' ? [`set ANTHROPIC_MODEL=${claudeModel}`] : []),
                  'set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1',
                ].join('\n'),
              },
              {
                label: '%USERPROFILE%\\.claude\\settings.json',
                path: '%USERPROFILE%\\.claude\\settings.json',
                code: JSON.stringify(
                  {
                    $schema: 'https://json.schemastore.org/claude-code-settings.json',
                    env: {
                      ANTHROPIC_BASE_URL: origin,
                      ANTHROPIC_AUTH_TOKEN: 'YOUR_API_KEY',
                      ...(claudeModel !== 'YOUR_MODEL_ID' ? { ANTHROPIC_MODEL: claudeModel } : {}),
                      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
                    },
                  },
                  null,
                  2,
                ),
              },
            ],
          },
        },
        {
          title: l('在项目根目录启动', 'Launch in your repository'),
          description: l('确认未装 CLI 时可从 CC Switch「应用」一键装或参考官方安装指南，随后在项目目录运行 claude。', 'Run claude inside your project folder once installed.'),
          genericSnippets: [{ label: 'Shell', code: 'cd your-project\nclaude' }],
        },
      ],
      verifyCommands: {
        unix: `curl ${origin}/v1/models \\\n  -H "Authorization: Bearer YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${origin}/v1/models" -Headers @{ Authorization = "Bearer YOUR_API_KEY" }`,
        cmd: `curl ${origin}/v1/models -H "Authorization: Bearer YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('帮我梳理当前仓库的目录结构、核心入口文件和运行测试的命令。', 'Summarize this repository structure, entry points, and test commands.'),
        l('阅读最近一次 git diff，检查有没有边界条件遗漏或潜在回归。', 'Review the latest git diff for edge cases or regressions.'),
      ],
      tips: [
        l('在本站创建「智能路由」密钥绑定多个 Anthropic 分组后，网关会综合可用率、缓存率、首字速度与倍率选组并粘住当前会话，减少缓存重复创建。', 'With Smart Routing across Anthropic groups, the gateway chooses the best group and sticks per session to preserve prompt cache hits.'),
        l('如果要在 Claude Code 里用本站的 Grok / DeepSeek / MiniMax 分组，可在控制台「使用密钥 → Claude Code」查看对应的模型别名映射。', 'To run Grok, DeepSeek, or MiniMax groups through Claude Code, check the model-alias env vars in Console → Use Key.'),
        l('CC Switch 导入的供应商默认自带本站余额查询脚本，在 CC Switch 卡片上就能直接看到实时余额。', 'Imported CC Switch providers include this site’s balance script automatically.'),
      ],
      faq: [
        {
          question: l('在 CC Switch 启用后，Claude Code 还是连官方或报 401？', 'Still hits official API or returns 401 after enabling in CC Switch?'),
          answer: l(
            '先检查当前终端是否之前手动 export 过 ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN / ANTHROPIC_API_KEY（Shell 环境变量优先级高于 settings.json）。关闭旧终端新开一个窗口再执行 claude 即可。',
            'Shell env vars override settings.json. Open a fresh terminal without stale ANTHROPIC_* exports and run claude again.',
          ),
        },
        {
          question: l('用 CC Switch 配好 Claude Code 后，VS Code 插件还要再配吗？', 'Do I need to configure the Claude Code VS Code extension separately?'),
          answer: l(
            '不需要。VS Code 里的 Claude Code 官方扩展直接读取同一份 ~/.claude/settings.json，在 CC Switch 点启用后重载 VS Code 窗口即可。',
            'No. The official VS Code extension reads ~/.claude/settings.json directly—just reload VS Code after activating in CC Switch.',
          ),
        },
        {
          question: l('怎么把同一把密钥既导入给 Claude Code 又导入给 Codex？', 'Can I import the same key into both Claude Code and Codex?'),
          answer: l(
            '可以。在本站创建密钥时开启「智能路由」，同时添加 Anthropic 分组和 OpenAI 分组；然后分别点两次「导入到 CCS」，一次选 Claude Code，一次选 Codex 即可。',
            'Yes. Create a Smart Routing key with both Anthropic and OpenAI groups, then click "Import to CCS" twice—once for Claude Code and once for Codex.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('提示 403 或提示模型不可用', '403 Forbidden or model unavailable'),
          answer: l(
            '打开本站「API 密钥」确认密钥绑定的分组包含你选的模型，并核对账户余额、密钥额度上限或 IP 白名单。',
            'Check the key’s bound groups, wallet balance, quota cap, and IP allow-list in Console → API Keys.',
          ),
        },
        {
          question: l('手动填 Base URL 时多写了 /v1 会报错吗？', 'Will adding /v1 manually break Claude Code?'),
          answer: l(
            '不会。本站网关会自动把重复的 /v1/v1/messages 折叠为 /v1/messages，但仍推荐用裸域名保持规范。',
            'No—this gateway auto-folds /v1/v1/messages into /v1/messages, though the bare origin is still recommended.',
          ),
        },
      ],
    },
    {
      id: 'codex',
      name: 'Codex',
      vendor: 'OpenAI',
      iconKey: 'openai',
      platform: 'openai',
      recommended: true,
      openSource: true,
      summary: l(
        'OpenAI 终端 CLI 与桌面端编程 Agent。在 CC Switch「应用」安装后，从本站一键「导入到 CCS」自动生成 config.toml。',
        'OpenAI’s coding agent CLI & app. Install in CC Switch → Apps and push config.toml + auth via "Import to CCS".',
      ),
      description: l(
        'Codex 走 OpenAI Responses 协议（/v1/responses），手动写 TOML 容易出错。最省事的做法是在 CC Switch「应用」一键装好 Codex，再从本站控制台对 OpenAI / 智能路由密钥点「导入到 CCS → Codex」。',
        'Codex uses the OpenAI Responses API (/v1/responses) with TOML configuration. Install Codex inside CC Switch → Apps, then click "Import to CCS" → Codex from your API key receipt.',
      ),
      workflows: ['terminal', 'desktop', 'extension'],
      siteGroupAdvice: l('OpenAI 分组，或包含 OpenAI 的「智能路由」密钥', 'OpenAI group, or a Smart Routing key containing OpenAI'),
      protocolLabel: 'OpenAI Responses',
      endpointPath: '/v1/responses',
      baseUrl: v1Base,
      baseUrlRule: l('CCS 导入自动配好 Responses 协议与端点；手动写 config.toml 时 base_url 填带 /v1 的地址。', 'CCS sets Responses protocol automatically; manual config.toml uses the /v1 base URL.'),
      defaultModel: 'gpt-5.6-sol',
      modelPresets: ['YOUR_MODEL_ID', 'gpt-5.6-sol', 'gpt-5.5', 'gpt-5.4', 'gpt-6.1-sol'],
      officialDocsUrl: 'https://developers.openai.com/codex/',
      launchCommand: 'codex',
      firstLaunchNote: l('启动后输入 /model 可查看当前可用的模型列表；若需在本站拉取完整模型清单，也可在控制台「使用密钥 → Codex」下载 codex-models.json。', 'Run /model inside Codex to verify available models; Console → Use Key can also fetch codex-models.json.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['codex'],
        appLabel: 'Codex (app=codex)',
        sidebarName: 'Codex',
        installNote: l('在 CC Switch 左下角「应用」找到 Codex 点「安装」，并打开右侧开关。', 'In CC Switch → Apps, click Install next to Codex and toggle it on.'),
        endpoint: origin,
        protocolLabel: 'OpenAI Responses',
        defaultModel: 'gpt-5.6-sol',
        importHint: l(
          '对 OpenAI 分组（或含 OpenAI 的智能路由）密钥点「导入到 CCS」→ 应用选 Codex → 选择推荐模型（如 gpt-5.6-sol），CC Switch 会自动写好 config.toml、auth.json 与余额查询脚本。',
          'Click "Import to CCS" → Codex on an OpenAI or Smart Routing key to auto-generate config.toml, auth.json, and usage tracking.',
        ),
      },
      manualSteps: [
        {
          title: l('在无桌面环境下手动写入 config.toml 与 auth.json', 'Manual config.toml and auth.json (without CC Switch)'),
          description: l(
            '也可以直接在本站控制台「API 密钥 → 使用密钥 → Codex」一键复制带真实密钥与模型清单 URL 的完整配置，支持 HTTP/SSE 与 WebSocket v2 两种模式。',
            'You can also open Console → API Keys → "Use Key" → Codex to copy a complete config (HTTP/SSE or WebSocket v2) with your key filled in.',
          ),
          snippets: {
            unix: [
              {
                label: '~/.codex/config.toml',
                path: '~/.codex/config.toml',
                code: [
                  'model_provider = "OpenAI"',
                  `model = "${codexModel}"`,
                  `review_model = "${codexModel}"`,
                  'disable_response_storage = true',
                  'network_access = "enabled"',
                  '',
                  '[model_providers.OpenAI]',
                  'name = "OpenAI"',
                  `base_url = "${v1Base}"`,
                  `model_catalog_url = "${v1Base}/models"`,
                  'wire_api = "responses"',
                  'requires_openai_auth = true',
                ].join('\n'),
              },
              {
                label: '~/.codex/auth.json',
                path: '~/.codex/auth.json',
                code: JSON.stringify({ OPENAI_API_KEY: 'YOUR_API_KEY' }, null, 2),
                note: l('如走非 OpenAI 分组路由，把 requires_openai_auth 改为 false，并加 supports_websockets = false 与 env_key = "SUB2API_API_KEY"。', 'For non-OpenAI routed groups, set requires_openai_auth = false, supports_websockets = false, and env_key = "SUB2API_API_KEY".'),
              },
            ],
            powershell: [
              {
                label: '%USERPROFILE%\\.codex\\config.toml',
                path: '%USERPROFILE%\\.codex\\config.toml',
                code: [
                  'model_provider = "OpenAI"',
                  `model = "${codexModel}"`,
                  `review_model = "${codexModel}"`,
                  'disable_response_storage = true',
                  'network_access = "enabled"',
                  'windows_wsl_setup_acknowledged = true',
                  '',
                  '[model_providers.OpenAI]',
                  'name = "OpenAI"',
                  `base_url = "${v1Base}"`,
                  `model_catalog_url = "${v1Base}/models"`,
                  'wire_api = "responses"',
                  'requires_openai_auth = true',
                ].join('\n'),
              },
              {
                label: '%USERPROFILE%\\.codex\\auth.json',
                path: '%USERPROFILE%\\.codex\\auth.json',
                code: JSON.stringify({ OPENAI_API_KEY: 'YOUR_API_KEY' }, null, 2),
              },
            ],
            cmd: [
              {
                label: '%USERPROFILE%\\.codex\\config.toml',
                path: '%USERPROFILE%\\.codex\\config.toml',
                code: [
                  'model_provider = "OpenAI"',
                  `model = "${codexModel}"`,
                  `review_model = "${codexModel}"`,
                  'disable_response_storage = true',
                  'network_access = "enabled"',
                  'windows_wsl_setup_acknowledged = true',
                  '',
                  '[model_providers.OpenAI]',
                  'name = "OpenAI"',
                  `base_url = "${v1Base}"`,
                  `model_catalog_url = "${v1Base}/models"`,
                  'wire_api = "responses"',
                  'requires_openai_auth = true',
                ].join('\n'),
              },
              {
                label: '%USERPROFILE%\\.codex\\auth.json',
                path: '%USERPROFILE%\\.codex\\auth.json',
                code: JSON.stringify({ OPENAI_API_KEY: 'YOUR_API_KEY' }, null, 2),
              },
            ],
          },
        },
        {
          title: l('启动 Codex', 'Launch Codex'),
          description: l('在项目目录运行 codex 即可开始，使用 /model 切换当前模型。', 'Run codex in your project root and use /model to switch models.'),
          genericSnippets: [{ label: 'Shell', code: 'cd your-project\ncodex' }],
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/responses \\\n  -H "Authorization: Bearer YOUR_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"${codexModel}","input":"Say ok"}'`,
        powershell: `Invoke-RestMethod -Method Post -Uri "${v1Base}/responses" -Headers @{ Authorization = "Bearer YOUR_API_KEY" } -ContentType "application/json" -Body '{"model":"${codexModel}","input":"Say ok"}'`,
        cmd: `curl ${v1Base}/responses -H "Authorization: Bearer YOUR_API_KEY" -H "Content-Type: application/json" -d "{\\"model\\":\\"${codexModel}\\",\\"input\\":\\"Say ok\\"}"`,
      },
      examplePrompts: [
        l('分析当前仓库的构建脚本，找出可能拖慢构建的步骤。', 'Inspect the build scripts to find what slows down builds.'),
        l('先写复现失败用例，再给出最小修复并跑通测试。', 'Write a failing reproduction test first, then apply a minimal fix and run the test suite.'),
      ],
      tips: [
        l('保持 wire_api = "responses"，不要改成 chat_completions，否则工具调用与推理流事件会异常。', 'Keep wire_api = "responses" so tool calls and reasoning stream events work properly.'),
        l('如网络代理不支持 WebSocket，在配置中设 supports_websockets = false 即可平滑走 HTTP/SSE。', 'If your proxy blocks WebSockets, set supports_websockets = false to use HTTP/SSE.'),
      ],
      faq: [
        {
          question: l('Codex 启动后还是让我登录 ChatGPT 账号怎么办？', 'Why does Codex still prompt for ChatGPT sign-in?'),
          answer: l(
            '确认已在 CC Switch 中点击了本站配置右侧的「启用」，并确保没有旧的环境变量或残缺的 auth.json 干扰；新开终端再运行 codex。',
            'Confirm the imported provider is Activated in CC Switch and open a fresh terminal so auth.json and config.toml are loaded cleanly.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('WebSocket 握手失败或频繁断连', 'WebSocket handshake fails or drops'),
          answer: l(
            '在 CC Switch 编辑该供应商配置（或 ~/.codex/config.toml），加上 supports_websockets = false 切换为 HTTP/SSE。',
            'Edit the provider in CC Switch (or ~/.codex/config.toml) and add supports_websockets = false for HTTP/SSE.',
          ),
        },
      ],
    },
    {
      id: 'gemini-cli',
      name: 'Gemini CLI',
      vendor: 'Google',
      iconKey: 'gemini',
      platform: 'gemini',
      openSource: true,
      summary: l(
        'Google 开源终端 Agent，走原生 /v1beta 协议。支持在 CC Switch「应用」一键安装并从本站一键导入。',
        'Google’s open-source terminal agent on native /v1beta. One-click install in CC Switch → Apps + "Import to CCS".',
      ),
      description: l(
        'Gemini CLI 走 Google 原生 /v1beta 路径，最容易踩的坑是手填 Base URL 时多写 /v1。通过本站「导入到 CCS」会自动为 Gemini 分组填裸域名、为 Antigravity 分组自动拼 /antigravity，彻底避坑。',
        'Gemini CLI uses native /v1beta paths—appending /v1 manually causes 404. "Import to CCS" sets the bare origin (or /antigravity for Antigravity groups) automatically.',
      ),
      workflows: ['terminal'],
      siteGroupAdvice: l('Gemini 分组或 Antigravity 分组', 'Gemini group or Antigravity group'),
      protocolLabel: 'Gemini Native',
      endpointPath: '/v1beta',
      baseUrl: origin,
      baseUrlRule: l('必须填裸域名（Gemini 原生走 /v1beta，绝对不能加 /v1！）；Antigravity 分组填 <origin>/antigravity，CCS 导入会自动处理。', 'Must be the bare origin (Gemini uses /v1beta — never add /v1); Antigravity groups use <origin>/antigravity automatically via CCS.'),
      defaultModel: 'gemini-2.5-pro',
      modelPresets: ['gemini-2.5-pro', 'gemini-2.5-flash', 'gemini-3-pro-preview', 'gemini-3.1-pro-preview'],
      officialDocsUrl: 'https://github.com/google-gemini/gemini-cli',
      launchCommand: 'gemini',
      firstLaunchNote: l('首次启动如果弹出认证选择，选「Use Gemini API Key」即可直接使用导入好的本站密钥。', 'If prompted on first launch, choose "Use Gemini API Key".'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['gemini'],
        appLabel: 'Gemini CLI (app=gemini)',
        sidebarName: 'Gemini CLI',
        installNote: l('在 CC Switch 左下角「应用」找到 Gemini CLI 点击「安装」并开启侧边栏显示。', 'In CC Switch → Apps, click Install next to Gemini CLI and enable the sidebar toggle.'),
        endpoint: origin,
        protocolLabel: 'Gemini Native (/v1beta)',
        defaultModel: 'gemini-2.5-pro',
        importHint: l(
          '在控制台为 Gemini 或 Antigravity 分组密钥点「导入到 CCS」→ 应用选 Gemini CLI；Antigravity 分组会自动将端点设为 <origin>/antigravity。',
          'Click "Import to CCS" → Gemini CLI on a Gemini or Antigravity key; Antigravity groups automatically target <origin>/antigravity.',
        ),
      },
      manualSteps: [
        {
          title: l('手动设置环境变量（注意不要带 /v1）', 'Manual environment variables (without /v1)'),
          description: l(
            `将 GOOGLE_GEMINI_BASE_URL 指向站点根地址。若密钥绑定的是 Antigravity 分组，请将地址改为 ${origin}/antigravity。`,
            `Set GOOGLE_GEMINI_BASE_URL to the site root. For Antigravity groups, use ${origin}/antigravity instead.`,
          ),
          snippets: {
            unix: [
              {
                label: 'Terminal',
                code: [
                  `export GOOGLE_GEMINI_BASE_URL="${origin}"`,
                  'export GEMINI_API_KEY="YOUR_API_KEY"',
                  `export GEMINI_MODEL="${geminiModel}"`,
                ].join('\n'),
              },
            ],
            powershell: [
              {
                label: 'PowerShell',
                code: [
                  `$env:GOOGLE_GEMINI_BASE_URL="${origin}"`,
                  '$env:GEMINI_API_KEY="YOUR_API_KEY"',
                  `$env:GEMINI_MODEL="${geminiModel}"`,
                ].join('\n'),
              },
            ],
            cmd: [
              {
                label: 'Command Prompt',
                code: [
                  `set GOOGLE_GEMINI_BASE_URL=${origin}`,
                  'set GEMINI_API_KEY=YOUR_API_KEY',
                  `set GEMINI_MODEL=${geminiModel}`,
                ].join('\n'),
              },
            ],
          },
        },
        {
          title: l('启动 Gemini CLI', 'Launch Gemini CLI'),
          description: l('在项目目录执行 gemini，认证方式选择「Use Gemini API Key」。', 'Run gemini in your project root and select "Use Gemini API Key".'),
          genericSnippets: [{ label: 'Shell', code: 'cd your-project\ngemini' }],
        },
      ],
      verifyCommands: {
        unix: `curl "${origin}/v1beta/models" \\\n  -H "x-goog-api-key: YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${origin}/v1beta/models" -Headers @{ "x-goog-api-key" = "YOUR_API_KEY" }`,
        cmd: `curl "${origin}/v1beta/models" -H "x-goog-api-key: YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('通读 docs/ 下的所有文档，帮我总结系统架构和关键配置项。', 'Read everything under docs/ and summarize the system architecture.'),
      ],
      tips: [
        l('只有 Gemini 原生接口走 /v1beta，不属于 /v1 折叠范围；使用「导入到 CCS」能保证端点格式 100% 正确。', 'Gemini uses /v1beta (not rewritten by /v1 folding); "Import to CCS" guarantees the exact endpoint.'),
      ],
      faq: [
        {
          question: l('为什么加了 /v1 会报 404？', 'Why does adding /v1 return 404?'),
          answer: l(
            '多写 /v1 会让 Gemini CLI 请求 /v1/v1beta/*，删掉末尾 /v1（或重新点一次「导入到 CCS」）即可恢复。',
            'Adding /v1 makes Gemini CLI request /v1/v1beta/* which 404s. Remove /v1 or re-import via CCS.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('启动后弹出 Google 账号 OAuth 登录', 'Prompts for Google OAuth login'),
          answer: l(
            '确认已在 CC Switch 启用本站供应商，然后在 Gemini CLI 中执行 /auth 切换为 Gemini API Key 模式。',
            'Verify the provider is active in CC Switch, then run /auth inside Gemini CLI to switch to API Key mode.',
          ),
        },
      ],
    },
    {
      id: 'grok-build',
      name: 'Grok Build',
      vendor: 'xAI',
      iconKey: 'grok',
      platform: 'grok',
      summary: l(
        'xAI 终端编程 Agent CLI。在 CC Switch「应用」安装后，从本站 Grok 密钥一键「导入到 CCS」自动配置 /v1 端点。',
        'xAI’s terminal coding CLI. Install via CC Switch → Apps and import your Grok key with "Import to CCS".',
      ),
      description: l(
        'Grok Build CLI 通过本站 Grok 分组的 /v1/responses 接入。从本站「API 密钥」对 Grok 分组密钥点「导入到 CCS → Grok」会自动补齐 /v1 端点与推荐模型（grok-4.6），无需手写几十行 config.toml。',
        'Grok Build connects via /v1/responses on Grok groups. Clicking "Import to CCS" → Grok automatically appends /v1 and configures the default model without hand-writing config.toml.',
      ),
      workflows: ['terminal'],
      siteGroupAdvice: l('Grok 分组（如需生图/生视频请确认分组已开图像权限）', 'Grok group (with image permission enabled if using media tools)'),
      protocolLabel: 'OpenAI Responses',
      endpointPath: '/v1/responses',
      baseUrl: v1Base,
      baseUrlRule: l('「导入到 CCS」会自动为 Grok Build 补齐 /v1 后缀。', '"Import to CCS" automatically appends /v1 for Grok Build.'),
      defaultModel: 'grok-4.6',
      modelPresets: ['grok-4.6', 'grok-4.5', 'grok-build-0.1', 'grok-4.3'],
      officialDocsUrl: 'https://docs.x.ai/',
      launchCommand: 'grok',
      firstLaunchNote: l('启动前可先跑一次 grok inspect，确认认证方式显示 api_key 且端点指向本站。', 'Run grok inspect first to confirm api_key auth and the gateway endpoint.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['grokbuild'],
        appLabel: 'Grok (app=grokbuild)',
        sidebarName: 'Grok Build',
        installNote: l('在 CC Switch 左下角「应用」找到 Grok Build 点击「安装」并开启左侧栏显示。', 'In CC Switch → Apps, install Grok Build and toggle it on in the sidebar.'),
        endpoint: v1Base,
        protocolLabel: 'OpenAI Responses',
        defaultModel: 'grok-4.6',
        importHint: l(
          '在控制台为 Grok 分组密钥点「导入到 CCS」→ 应用默认自动选中 Grok，端点自动带 /v1，点击确认即可。',
          'Click "Import to CCS" on a Grok-group key—Grok and its /v1 endpoint are pre-selected automatically.',
        ),
      },
      manualSteps: [
        {
          title: l('不使用 CC Switch 时的环境变量与 config.toml', 'Manual env vars & config.toml (without CC Switch)'),
          description: l(
            '也可在控制台「使用密钥 → Grok CLI」复制完整配置。务必保留 api_backend = "responses" 与 preferred_method = "api_key"。',
            'Or copy the full config from Console → Use Key → Grok CLI. Keep api_backend = "responses" and preferred_method = "api_key".',
          ),
          snippets: {
            unix: [
              { label: 'Terminal', code: `export GROK_MODELS_BASE_URL="${v1Base}"\nexport XAI_API_KEY="YOUR_API_KEY"` },
              {
                label: '~/.grok/config.toml',
                path: '~/.grok/config.toml',
                code: [
                  '[endpoints]',
                  `models_base_url = "${v1Base}"`,
                  `models_list_url = "${v1Base}/models"`,
                  '',
                  '[auth]',
                  'preferred_method = "api_key"',
                  '',
                  `[model."${grokModel}"]`,
                  `model = "${grokModel}"`,
                  'env_key = "XAI_API_KEY"',
                  'api_backend = "responses"',
                  'supports_backend_search = true',
                  '',
                  '[models]',
                  `default = "${grokModel}"`,
                ].join('\n'),
              },
            ],
            powershell: [
              { label: 'PowerShell', code: `$env:GROK_MODELS_BASE_URL="${v1Base}"\n$env:XAI_API_KEY="YOUR_API_KEY"` },
              {
                label: '%USERPROFILE%\\.grok\\config.toml',
                path: '%USERPROFILE%\\.grok\\config.toml',
                code: [
                  '[endpoints]',
                  `models_base_url = "${v1Base}"`,
                  `models_list_url = "${v1Base}/models"`,
                  '',
                  '[auth]',
                  'preferred_method = "api_key"',
                  '',
                  `[model."${grokModel}"]`,
                  `model = "${grokModel}"`,
                  'env_key = "XAI_API_KEY"',
                  'api_backend = "responses"',
                  'supports_backend_search = true',
                  '',
                  '[models]',
                  `default = "${grokModel}"`,
                ].join('\n'),
              },
            ],
            cmd: [
              { label: 'Command Prompt', code: `set GROK_MODELS_BASE_URL=${v1Base}\nset XAI_API_KEY=YOUR_API_KEY` },
            ],
          },
        },
        {
          title: l('校验并启动', 'Inspect & launch'),
          description: l('运行 grok inspect 校验配置，再运行 grok 开始会话。', 'Run grok inspect to verify, then start grok.'),
          genericSnippets: [{ label: 'Shell', code: 'grok inspect\ngrok' }],
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/models \\\n  -H "Authorization: Bearer YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${v1Base}/models" -Headers @{ Authorization = "Bearer YOUR_API_KEY" }`,
        cmd: `curl ${v1Base}/models -H "Authorization: Bearer YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('检查这个后端 handler 的并发安全隐患并给出最小修复补丁。', 'Audit this backend handler for concurrency bugs and propose a minimal patch.'),
      ],
      tips: [
        l('Grok Build 默认如果不写 api_backend 会退回 chat_completions；通过 CC Switch 导入会自动保证走 Responses 协议。', 'CC Switch keeps Grok Build on Responses instead of falling back to chat_completions.'),
      ],
      faq: [
        {
          question: l('grok inspect 显示认证方式仍为 session 登录？', 'grok inspect still reports session login?'),
          answer: l(
            '确认在 CC Switch 中点击了「启用」并新开终端；手动配置时检查 [auth] preferred_method = "api_key" 与 XAI_API_KEY。',
            'Ensure the provider is Activated in CC Switch and open a new terminal; for manual config, verify preferred_method = "api_key".',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('调用生图/生视频报错 403', 'Image/video tools return 403'),
          answer: l(
            '在本站「可用渠道 / 模型广场」确认该密钥绑定的 Grok 分组已开启图像生成权限。',
            'Confirm the bound Grok group allows image generation on this site.',
          ),
        },
      ],
    },
    {
      id: 'opencode',
      name: 'OpenCode',
      vendor: 'Anomaly',
      iconKey: 'opencode',
      openSource: true,
      summary: l(
        '开源高颜值终端 TUI，天生多厂商 BYOK。搭配本站「智能路由」密钥 + CC Switch 一键导入最省心。',
        'Open-source multi-provider TUI agent. Pairs naturally with a Smart Routing key and CC Switch import.',
      ),
      description: l(
        'OpenCode 支持在同一界面里随时切换 Claude、GPT、Gemini、Grok 模型。在 CC Switch「应用」安装好 OpenCode 后，直接把本站「智能路由」密钥点「导入到 CCS → OpenCode」即可使用。',
        'OpenCode switches across Claude, GPT, Gemini, and Grok inside one TUI. Install it in CC Switch → Apps, then import a Smart Routing key via "Import to CCS" → OpenCode.',
      ),
      workflows: ['terminal'],
      siteGroupAdvice: l('推荐「智能路由」密钥（可同时绑定 Claude / OpenAI / Gemini / Grok 分组）', 'Recommended: Smart Routing key spanning Claude / OpenAI / Gemini / Grok'),
      protocolLabel: 'Multi-protocol',
      endpointPath: '/v1/messages & /v1/responses',
      baseUrl: v1Base,
      baseUrlRule: l('CCS 导入自动处理；手动写 opencode.json 时 Anthropic/OpenAI/Grok 填 /v1，Gemini 填 /v1beta。', 'Handled by CCS; manual opencode.json uses /v1 for Anthropic/OpenAI/Grok and /v1beta for Gemini.'),
      defaultModel: 'claude-sonnet-4-6',
      modelPresets: ['YOUR_MODEL_ID', 'claude-sonnet-4-6', 'gpt-5.6-sol', 'gemini-2.5-pro', 'grok-4.6'],
      officialDocsUrl: 'https://opencode.ai/docs/',
      launchCommand: 'opencode',
      firstLaunchNote: l('进入界面后执行 /models 即可选择已导入的本站模型。', 'Run /models inside OpenCode to select your imported gateway model.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['opencode'],
        appLabel: 'OpenCode (app=opencode)',
        sidebarName: 'OpenCode',
        installNote: l('在 CC Switch 左下角「应用」找到 OpenCode 点击「安装」并开启左侧栏显示。', 'In CC Switch → Apps, install OpenCode and toggle it on.'),
        endpoint: origin,
        protocolLabel: 'Multi-protocol',
        defaultModel: 'gpt-5.6-sol',
        importHint: l(
          '在控制台对密钥点「导入到 CCS」→ 应用选 OpenCode → 确认后在 CC Switch 点「启用」即可写好 opencode.json。',
          'Click "Import to CCS" → OpenCode from Console → API Keys, then Activate in CC Switch to write opencode.json.',
        ),
      },
      manualSteps: [
        {
          title: l('不使用 CC Switch：直接写入 opencode.json', 'Manual opencode.json (without CC Switch)'),
          description: l(
            '控制台「使用密钥 → OpenCode」会按你的分组自动生成带完整上下文窗口参数的 opencode.json；也可使用下方精简模板。',
            'Console → Use Key → OpenCode generates a full platform-specific opencode.json; a minimal multi-provider template is below.',
          ),
          genericSnippets: [
            {
              label: '~/.config/opencode/opencode.json',
              path: '~/.config/opencode/opencode.json',
              code: JSON.stringify(
                {
                  $schema: 'https://opencode.ai/config.json',
                  provider: {
                    anthropic: {
                      npm: '@ai-sdk/anthropic',
                      options: { baseURL: v1Base, apiKey: 'YOUR_API_KEY' },
                      models: {
                        [openCodeModel !== 'YOUR_MODEL_ID' ? openCodeModel : 'claude-sonnet-4-6']: { name: 'Claude via Gateway' },
                      },
                    },
                    openai: {
                      options: { baseURL: v1Base, apiKey: 'YOUR_API_KEY' },
                      models: {
                        'gpt-5.6-sol': { name: 'GPT via Gateway', options: { store: false } },
                      },
                    },
                  },
                  agent: {
                    build: { options: { store: false } },
                    plan: { options: { store: false } },
                  },
                },
                null,
                2,
              ),
            },
          ],
        },
        {
          title: l('启动 OpenCode', 'Launch OpenCode'),
          description: l('在项目目录执行 opencode，按 /models 切换模型。', 'Run opencode in your project and use /models to pick a model.'),
          genericSnippets: [{ label: 'Shell', code: 'cd your-project\nopencode' }],
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/models \\\n  -H "Authorization: Bearer YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${v1Base}/models" -Headers @{ Authorization = "Bearer YOUR_API_KEY" }`,
        cmd: `curl ${v1Base}/models -H "Authorization: Bearer YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('先进入 Plan 模式，列出重构这个组件的分步计划，等我确认再动手。', 'Start in Plan mode and outline the refactor plan before editing files.'),
      ],
      tips: [
        l('OpenAI 分组在 opencode.json 中建议保留 options.store = false，与中转无状态行为对齐。', 'Keep options.store = false for OpenAI models to match stateless relay behavior.'),
      ],
      faq: [
        {
          question: l('OpenCode 报找不到模型怎么办？', 'OpenCode reports model not found?'),
          answer: l(
            '确认所选模型 ID 在该密钥的 GET /v1/models 返回结果里（智能路由密钥要确认对应平台分组已绑定）。',
            'Ensure the model ID appears in GET /v1/models for that key and the matching platform group is bound.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('切换供应商后 OpenCode 仍读取旧模型', 'OpenCode still uses old provider'),
          answer: l(
            '检查当前项目根目录有没有残留的局部 opencode.json（项目级配置优先级高于全局配置），删掉或更新后重启 opencode。',
            'Check for a project-local opencode.json overriding the global config written by CC Switch.',
          ),
        },
      ],
    },
    {
      id: 'claude-desktop',
      name: 'Claude Desktop',
      vendor: 'Anthropic',
      iconKey: 'anthropic',
      platform: 'anthropic',
      summary: l(
        'Anthropic 桌面端对话与 MCP 客户端，通过 CC Switch 路由模式接入本站并统一管理 MCP。',
        'Anthropic’s desktop client routed to this gateway via CC Switch with shared MCP management.',
      ),
      description: l(
        'CC Switch 支持为 Claude Desktop 提供本地路由托管（左侧栏显示「路由」角标）。在本站创建 Anthropic 分组密钥后点「导入到 CCS → Claude Desktop」，并在 CC Switch 中启用即可。',
        'CC Switch routes Claude Desktop locally (marked with a Route badge in the sidebar). Import your Anthropic key with "Import to CCS" → Claude Desktop and activate it.',
      ),
      workflows: ['desktop'],
      siteGroupAdvice: l('Anthropic 分组，或包含 Anthropic 的「智能路由」密钥', 'Anthropic group or Smart Routing key with Anthropic'),
      protocolLabel: 'Anthropic Messages',
      endpointPath: '/v1/messages',
      baseUrl: origin,
      baseUrlRule: l('由 CC Switch 托管路由到本站裸域名。', 'Routed to the bare site origin via CC Switch.'),
      defaultModel: 'claude-sonnet-4-6',
      modelPresets: ['claude-sonnet-4-6', 'claude-opus-4-6', 'claude-sonnet-5-5'],
      officialDocsUrl: 'https://claude.ai/download',
      launchCommand: 'CC Switch → Claude Desktop → 启用',
      firstLaunchNote: l('在 CC Switch 点「启用」后，请完全退出 Claude Desktop（含托盘图标）再重新打开。', 'After clicking Activate in CC Switch, fully quit Claude Desktop (including tray) and reopen it.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: false,
        appIds: ['claude-desktop'],
        appLabel: 'Claude Desktop (app=claude-desktop)',
        sidebarName: 'Claude Desktop',
        installNote: l('先在电脑安装 Claude Desktop 客户端，并在 CC Switch 左下角「应用」打开 Claude Desktop 的左侧栏开关。', 'Install Claude Desktop on your computer, then enable it in CC Switch → Apps.'),
        endpoint: origin,
        protocolLabel: 'Anthropic Messages',
        defaultModel: 'claude-sonnet-4-6',
        importHint: l(
          '在控制台点「导入到 CCS」→ 应用选 Claude Desktop → 确认导入，随后在 CC Switch 左侧切到 Claude Desktop 点「启用」。',
          'Click "Import to CCS" → Claude Desktop in Console → API Keys, then click Activate in CC Switch.',
        ),
      },
      manualSteps: [
        {
          title: l('在 CC Switch 中手动添加 Claude Desktop 供应商', 'Add provider manually in CC Switch'),
          description: l(
            '如果系统未注册 ccswitch:// 深链，在 CC Switch 左侧切到 Claude Desktop，点右上角 + 填入以下字段并启用：',
            'If ccswitch:// deeplinks are unavailable, open Claude Desktop in CC Switch, click +, and fill in:',
          ),
          genericSnippets: [
            {
              label: 'CC Switch → Claude Desktop',
              code: [
                'App:       Claude Desktop',
                `Base URL:  ${origin}`,
                'API Key:   YOUR_API_KEY',
                `Model:     ${claudeModel !== 'YOUR_MODEL_ID' ? claudeModel : 'claude-sonnet-4-6'}`,
              ].join('\n'),
            },
          ],
        },
      ],
      verifyCommands: {
        unix: `curl ${origin}/v1/sub2api/key \\\n  -H "x-api-key: YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${origin}/v1/sub2api/key" -Headers @{ "x-api-key" = "YOUR_API_KEY" }`,
        cmd: `curl ${origin}/v1/sub2api/key -H "x-api-key: YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('把这份会议纪要整理成按负责人分组的行动清单。', 'Turn these meeting notes into an action list grouped by owner.'),
      ],
      tips: [
        l('可在 CC Switch 左侧「MCP」面板一次性把常用 MCP 同步给 Claude Desktop、Claude Code 和 Codex。', 'Use CC Switch’s MCP tab to share MCP servers across Claude Desktop, Claude Code, and Codex.'),
      ],
      faq: [
        {
          question: l('启用后桌面端没有切过来？', 'Desktop client did not switch after activating?'),
          answer: l(
            'macOS 用 Cmd+Q、Windows 在托盘右键彻底退出 Claude Desktop 后重开，并确认 CC Switch 本地路由已处于开启状态。',
            'Fully quit Claude Desktop (Cmd+Q or Windows tray) and relaunch it with CC Switch running.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('提示连接本地路由失败', 'Local route connection error'),
          answer: l(
            '确认 CC Switch 正在后台运行，且安全软件未拦截本地回环端口。',
            'Make sure CC Switch is running in the background and local loopback traffic is allowed.',
          ),
        },
      ],
    },
    {
      id: 'openclaw',
      name: 'OpenClaw',
      vendor: 'OpenClaw',
      iconKey: 'openclaw',
      openSource: true,
      summary: l(
        '开源个人 AI 助手与多通道 Agent 网关，在 CC Switch「应用」管理并支持本站一键导入。',
        'Open-source personal AI assistant & gateway managed in CC Switch with one-click site import.',
      ),
      description: l(
        '在 CC Switch 左下角「应用」打开 OpenClaw 后，从本站「API 密钥」点击「导入到 CCS → OpenClaw」即可写入供应商配置，搭配智能路由密钥可按任务自动路由到不同模型。',
        'Enable OpenClaw in CC Switch → Apps, then click "Import to CCS" → OpenClaw from your site API key receipt. Pair with a Smart Routing key for multi-model tasks.',
      ),
      workflows: ['terminal', 'desktop'],
      siteGroupAdvice: l('推荐「智能路由」密钥或目标模型对应平台分组', 'Smart Routing key or the target model’s platform group'),
      protocolLabel: 'Multi-protocol',
      endpointPath: '/v1/chat/completions & /v1/messages',
      baseUrl: v1Base,
      baseUrlRule: l('CCS 导入自动配置；手动写配置时 OpenAI 兼容用 /v1，Anthropic 用裸域名。', 'Handled by CCS; manually use /v1 for OpenAI-compatible or bare origin for Anthropic.'),
      defaultModel: 'gpt-5.6-sol',
      modelPresets: ['YOUR_MODEL_ID', 'gpt-5.6-sol', 'claude-sonnet-4-6', 'grok-4.6'],
      officialDocsUrl: 'https://github.com/openclaw/openclaw',
      launchCommand: 'openclaw',
      firstLaunchNote: l('在 CC Switch 点「启用」写入配置后，记得重启 OpenClaw gateway 进程使其加载新供应商。', 'Restart the OpenClaw gateway process after clicking Activate in CC Switch.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['openclaw'],
        appLabel: 'OpenClaw (app=openclaw)',
        sidebarName: 'OpenClaw',
        installNote: l('在 CC Switch 左下角「应用」安装或启用 OpenClaw，使其出现在左侧栏。', 'Install or toggle on OpenClaw inside CC Switch → Apps.'),
        endpoint: origin,
        protocolLabel: 'Multi-protocol',
        defaultModel: 'gpt-5.6-sol',
        importHint: l(
          '在控制台点「导入到 CCS」→ 应用选 OpenClaw → 确认导入，再回到 CC Switch 启用并重启 OpenClaw 网关。',
          'Click "Import to CCS" → OpenClaw, activate in CC Switch, and restart the OpenClaw gateway.',
        ),
      },
      manualSteps: [
        {
          title: l('不使用 CC Switch：手动配置自定义供应商', 'Manual provider snippet (without CC Switch)'),
          description: l('修改 openclaw.json 前建议先备份旧文件，保存后重启 gateway 进程。', 'Back up openclaw.json before editing, then restart the gateway.'),
          genericSnippets: [
            {
              label: 'Provider snippet',
              code: JSON.stringify(
                {
                  provider: 'sub2api',
                  baseUrl: v1Base,
                  apiKey: 'YOUR_API_KEY',
                  model: openCodeModel !== 'YOUR_MODEL_ID' ? openCodeModel : 'gpt-5.6-sol',
                },
                null,
                2,
              ),
            },
          ],
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/models \\\n  -H "Authorization: Bearer YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${v1Base}/models" -Headers @{ Authorization = "Bearer YOUR_API_KEY" }`,
        cmd: `curl ${v1Base}/models -H "Authorization: Bearer YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('帮我检查今天待办事项，按紧急程度排个优先级。', 'Review my tasks for today and rank them by urgency.'),
      ],
      tips: [
        l('通过本站「导入到 CCS」导入时，建议先用 GET /v1/models 核对密钥可用模型。', 'Pick a model listed by your key’s /v1/models in the Import to CCS modal.'),
      ],
      faq: [
        {
          question: l('启用后没生效？', 'Changes not taking effect?'),
          answer: l('OpenClaw 在网关启动时加载配置，需重启 OpenClaw gateway 进程。', 'Restart the OpenClaw gateway after activating the provider in CC Switch.'),
        },
      ],
      troubleshooting: [
        {
          question: l('返回 401 或 403', 'Getting 401 or 403'),
          answer: l('用 GET /v1/sub2api/key 检查密钥是否过期、停用、超额或被 IP 白名单拦截。', 'Inspect key status, quota, expiry, and IP allow-list via GET /v1/sub2api/key.'),
        },
      ],
    },
    {
      id: 'ccswitch-ecosystem',
      name: l('Hermes / Pi / Kimi / MiniMax / DSH / Antigravity / Oh My Pi', 'Hermes / Pi / Kimi / MiniMax / DSH / Antigravity / Oh My Pi'),
      vendor: 'CC Switch Ecosystem',
      iconKey: 'ccswitch',
      openSource: true,
      summary: l(
        'CC Switch「应用」页全部支持一键安装，且本站「导入到 CCS」原生覆盖这 7 款应用的一键深链配置。',
        'All 7 agents can be installed in one click inside CC Switch → Apps and configured via this site’s "Import to CCS".',
      ),
      description: l(
        '这 7 款工具全部内置在 CC Switch 左下角「应用」页（支持一键安装或自动探测路径），并且全部在站内「导入到 CCS」应用列表中。你不需要分别去查它们各自的配置文件路径——统一走「应用页点安装 → 控制台点导入到 CCS → CC Switch 点启用」即可。',
        'Every one of these 7 agents lives right inside CC Switch → Apps (with one-click Install) and in this site’s "Import to CCS" dropdown. Follow one unified flow with zero manual config-path hunting.',
      ),
      workflows: ['terminal'],
      siteGroupAdvice: l('按目标模型选对应分组，或直接用「智能路由」密钥通配', 'Match the model’s group, or use one Smart Routing key across all of them'),
      protocolLabel: 'Multi-protocol (CC Switch)',
      endpointPath: '/v1/* & /v1beta/*',
      baseUrl: origin,
      baseUrlRule: l('全部由「导入到 CCS」按所选应用自动填充；注意 Pi 系列（pi / mcode / kimi / dsh / omp）导入时必须选定默认模型。', 'Filled automatically by "Import to CCS". Note: Pi-family apps (pi, mcode, kimi, dsh, omp) require a model ID.'),
      defaultModel: 'gpt-5.6-sol',
      modelPresets: ['gpt-5.6-sol', 'claude-sonnet-4-6', 'deepseek-v4.1', 'gemini-2.5-pro', 'grok-4.6'],
      officialDocsUrl: ccSwitchUrl,
      launchCommand: 'hermes / pi / dsh / agy / omp',
      firstLaunchNote: l('在 CC Switch 左侧切到对应应用并点击本站配置右侧「启用」后，新开终端运行该工具命令即可。', 'Activate the imported provider under that app in CC Switch, then open a fresh terminal.'),
      ccSwitch: {
        supported: true,
        oneClickInstallInApp: true,
        appIds: ['hermes', 'pi', 'mcode', 'kimi', 'dsh', 'agy', 'omp'],
        appLabel: 'hermes / pi / mcode / kimi / dsh / agy / omp',
        sidebarName: l('Hermes / Pi / MiniMax Code / Kimi Code / DeepSeek Harness / Antigravity / Oh My Pi', 'Hermes / Pi / MiniMax Code / Kimi Code / DeepSeek Harness / Antigravity / Oh My Pi'),
        installNote: l('在 CC Switch 左下角「应用」里，这几款工具均提供一键「安装」按钮（自动跑官方脚本或 npm），装完后打开右侧开关即可在左侧栏看到。', 'In CC Switch → Apps, click Install next to any of these tools and flip its sidebar toggle on.'),
        endpoint: origin,
        protocolLabel: 'Auto per app',
        defaultModel: 'gpt-5.6-sol',
        importHint: l(
          '在控制台「API 密钥」点「导入到 CCS」，下拉框选择对应工具即可。Pi 系列（pi / mcode / kimi / dsh / omp）会强制要求填模型名，直接从弹窗自动拉取的下拉列表里选一个即可。',
          'Pick the app in Console → "Import to CCS". Pi-family apps (pi, mcode, kimi, dsh, omp) require a model—pick one from the auto-loaded dropdown.',
        ),
      },
      manualSteps: [
        {
          title: l('7 款应用在「导入到 CCS」中的参数对照', 'App IDs & defaults for manual CC Switch entry'),
          description: l(
            '若需要手动在 CC Switch 新增供应商，参考下列 app 标识与推荐默认模型：',
            'If adding a provider manually inside CC Switch instead of using the deeplink, match these app IDs and endpoints:',
          ),
          genericSnippets: [
            {
              label: l('CC Switch 7 款生态应用对照', 'CC Switch ecosystem mapping'),
              code: [
                `hermes  (Hermes Agent)     → ${origin}   (gpt-5.6-sol / claude-sonnet-4-6)`,
                `pi      (Pi)               → ${origin}   (必填模型 ID)`,
                `omp     (Oh My Pi)         → ${origin}   (必填模型 ID)`,
                `mcode   (MiniMax Code)     → ${origin}   (必填模型 ID)`,
                `kimi    (Kimi Code)        → ${origin}   (必填模型 ID)`,
                `dsh     (DeepSeek Harness) → ${origin}   (deepseek-v4.1 / gpt-5.6-sol)`,
                `agy     (Antigravity)      → ${origin}   (gemini-2.5-pro)`,
              ].join('\n'),
            },
          ],
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/sub2api/account \\\n  -H "Authorization: Bearer YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${v1Base}/sub2api/account" -Headers @{ Authorization = "Bearer YOUR_API_KEY" }`,
        cmd: `curl ${v1Base}/sub2api/account -H "Authorization: Bearer YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('检查我当前目录的 git 状态，总结未提交的改动。', 'Check git status in this folder and summarize uncommitted changes.'),
      ],
      tips: [
        l('如果工具协议与所选分组不一致，可在 CC Switch 供应商详情中开启「本地路由」自动转换协议。', 'Enable CC Switch Local Router if an agent’s wire format differs from the bound group.'),
        l('所有通过「导入到 CCS」写入的配置都会自带本站余额查询脚本，在 CC Switch 左侧栏和卡片上可直接看到用量。', 'Every "Import to CCS" entry automatically attaches this site’s balance script.'),
      ],
      faq: [
        {
          question: l('为什么 Pi / Kimi / MiniMax / DSH / Oh My Pi 导入时必须选模型？', 'Why is a model required for Pi-family apps?'),
          answer: l(
            'CC Switch v4 深链协议要求 Pi 系列应用显式携带 model 字段。本站「导入到 CCS」弹窗已自动调用 /v1/models 帮你列出该密钥可用的模型，直接点选即可。',
            'CC Switch v4 requires the model field for Pi-family deeplinks. Our "Import to CCS" modal preloads your key’s /v1/models list so you can pick one in a click.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('提示当前 CC Switch 版本不支持该应用深链', 'CC Switch says deeplink is unsupported for this app'),
          answer: l(
            '请在 CC Switch 右上角检查更新升级到 v4.0.26 及以上版本，或在 CC Switch 内手动点「+」新增供应商。',
            'Update CC Switch to v4.0.26+ from its top-right update button, or add the provider manually inside CC Switch.',
          ),
        },
      ],
    },
    {
      id: 'cursor',
      name: 'Cursor',
      vendor: 'Cursor',
      iconKey: 'cursor',
      summary: l(
        'AI 原生代码编辑器，在设置里填本站 Base URL（/v1）与 API Key 即可直连，无需 CC Switch。',
        'AI-first IDE that connects directly via Settings → Models with this site’s /v1 Base URL and API Key.',
      ),
      description: l(
        'Cursor 自带图形化自定义 API 设置，不需要经过 CC Switch。在本站控制台创建密钥（推荐开启智能路由），到 Cursor Settings → Models 填入本站 /v1 地址、密钥并添加可用模型 ID 即可。',
        'Cursor has built-in custom API settings (no CC Switch needed). Create a key on this site (Smart Routing recommended), paste the /v1 Base URL and key in Cursor Settings → Models, and add your allowed model IDs.',
      ),
      workflows: ['ide'],
      siteGroupAdvice: l('推荐「智能路由」密钥，方便在 Cursor 里自由切换 Claude / GPT / Gemini', 'Smart Routing key so you can switch Claude, GPT, and Gemini without swapping keys'),
      protocolLabel: 'OpenAI Compatible',
      endpointPath: '/v1/chat/completions',
      baseUrl: v1Base,
      baseUrlRule: l('在 Override OpenAI Base URL 中填写完整的 <origin>/v1。', 'Enter the full <origin>/v1 URL in Override OpenAI Base URL.'),
      defaultModel: 'YOUR_MODEL_ID',
      modelPresets: ['YOUR_MODEL_ID', 'claude-sonnet-4-6', 'gpt-5.6-sol', 'gemini-2.5-pro'],
      officialDocsUrl: 'https://docs.cursor.com/',
      launchCommand: 'Cursor → Settings → Models',
      firstLaunchNote: l('点击 Verify 前，先在 Model Names 里添加并勾选你在本站有权限的模型 ID，关掉未绑定的默认模型。', 'Add a model ID from GET /v1/models and disable unavailable default models before clicking Verify.'),
      ccSwitch: {
        supported: false,
        oneClickInstallInApp: false,
        appIds: [],
        appLabel: l('IDE 内置图形设置（无需 CC Switch）', 'Built-in IDE Settings (no CC Switch needed)'),
        sidebarName: 'Cursor',
        installNote: l('Cursor 为独立桌面 IDE，在图形界面直接填入本站 /v1 地址与密钥即可。', 'Cursor is a standalone IDE configured directly inside Settings → Models.'),
        endpoint: v1Base,
        protocolLabel: 'OpenAI Compatible (/v1)',
        defaultModel: 'YOUR_MODEL_ID',
        importHint: l('Cursor 不需要 CC Switch，直接在 Settings → Models 粘贴下方 Base URL 与本站密钥。', 'No CC Switch needed—paste the Base URL and key directly in Cursor Settings → Models.'),
      },
      manualSteps: [
        {
          title: l('在本站创建密钥并查看可用模型 ID', 'Create a key and check allowed models on this site'),
          description: l(
            '在控制台「API 密钥」创建密钥（推荐开启智能路由绑定常用分组），再前往「模型广场」或用下方验证命令查出精确模型名。',
            'Create a key under Console → API Keys (Smart Routing recommended), and copy exact model IDs from Model Plaza or GET /v1/models.',
          ),
        },
        {
          title: l('打开 Cursor Settings → Models 填入 Base URL 与密钥', 'Configure Base URL & API Key in Cursor Settings → Models'),
          description: l(
            '按 Ctrl + Shift + J（macOS 为 Cmd + Shift + J）→ Models → 粘贴本站 API Key → 展开 Override OpenAI Base URL 填入下方地址并打开开关。',
            'Open Settings (Ctrl+Shift+J / Cmd+Shift+J) → Models → paste your site API key → enable Override OpenAI Base URL with the address below.',
          ),
          genericSnippets: [
            {
              label: 'Override OpenAI Base URL',
              code: v1Base,
            },
          ],
        },
        {
          title: l('添加本站模型名并点击 Verify', 'Add your allowed model IDs and click Verify'),
          description: l(
            '在 Model Names 中点 + Add model 添加你的可用模型（并关闭密钥未绑定的默认模型），点击 Verify 通过即可使用。',
            'Add your allowed model IDs under Model Names, disable unsubscribed defaults, then click Verify.',
          ),
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/chat/completions \\\n  -H "Authorization: Bearer YOUR_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"${openCodeModel}","messages":[{"role":"user","content":"Hello!"}]}'`,
        powershell: `Invoke-RestMethod -Method Post -Uri "${v1Base}/chat/completions" -Headers @{ Authorization = "Bearer YOUR_API_KEY" } -ContentType "application/json" -Body '{"model":"${openCodeModel}","messages":[{"role":"user","content":"Hello!"}]}'`,
        cmd: `curl ${v1Base}/chat/completions -H "Authorization: Bearer YOUR_API_KEY" -H "Content-Type: application/json" -d "{\\"model\\":\\"${openCodeModel}\\",\\"messages\\":[{\\"role\\":\\"user\\",\\"content\\":\\"Hello!\\"}]}"`,
      },
      examplePrompts: [
        l('用 Composer 帮我给这个表单补上校验逻辑和加载状态。', 'Use Composer to add validation and loading states to this form.'),
      ],
      tips: [
        l('自定义 API Key 适用于 Chat 与 Composer；Cursor 自带 Tab 自动补全仍依赖 Cursor 自身订阅。', 'Custom API keys power Chat and Composer; Cursor Tab autocomplete still uses Cursor’s own plan.'),
      ],
      faq: [
        {
          question: l('为什么 curl 通的，Cursor 点 Verify 却报错？', 'Why does Verify fail when curl succeeds?'),
          answer: l(
            'Verify 会拿列表里第一个勾选的模型做探测。把密钥没有权限的默认模型关掉，换成从本站 GET /v1/models 查到的模型名再试即可。',
            'Verify probes the first enabled model in the list. Disable models your key lacks, add one from GET /v1/models, and retry.',
          ),
        },
      ],
      troubleshooting: [
        {
          question: l('提示 404 或地址无效', '404 or invalid URL'),
          answer: l('确认 Override OpenAI Base URL 以 /v1 结尾且无多余空格，并确认站点使用 HTTPS。', 'Verify Override OpenAI Base URL ends in /v1 with no spaces and uses HTTPS.'),
        },
      ],
    },
    {
      id: 'vscode',
      name: l('VS Code (Cline / Roo / 扩展)', 'VS Code (Cline / Roo / Extensions)'),
      vendor: 'Microsoft & Extensions',
      iconKey: 'vscode',
      openSource: true,
      summary: l(
        'VS Code 的 Claude Code / Codex 官方插件会自动共享 CC Switch 配置；Cline / Roo Code 在插件设置填 Base URL 即可。',
        'Official Claude Code / Codex VS Code extensions inherit CC Switch automatically; Cline / Roo Code take Base URL + key.',
      ),
      description: l(
        '如果你在 VS Code 中使用 Claude Code 或 Codex 官方扩展，直接在 CC Switch 配好对应 CLI 就会自动生效；如果使用 Cline、Roo Code 或 Continue 扩展，在扩展设置中填入本站 Base URL 与密钥即可。',
        'Official Claude Code and Codex VS Code extensions read your CC Switch setup automatically. For Cline, Roo Code, or Continue, enter this site’s Base URL and API key in extension settings.',
      ),
      workflows: ['ide', 'extension'],
      siteGroupAdvice: l('Cline/Roo 选多模型推荐「智能路由」密钥；纯用 Claude 选 Anthropic 分组', 'Smart Routing for multi-model Cline/Roo, or Anthropic group for native Claude caching'),
      protocolLabel: 'OpenAI / Anthropic Compatible',
      endpointPath: '/v1/chat/completions or /v1/messages',
      baseUrl: v1Base,
      baseUrlRule: l('OpenAI Compatible 填 <origin>/v1；Anthropic Custom Base URL 填 <origin>。', 'Use <origin>/v1 for OpenAI Compatible, or <origin> for Anthropic Custom Base URL.'),
      defaultModel: 'YOUR_MODEL_ID',
      modelPresets: ['YOUR_MODEL_ID', 'claude-sonnet-4-6', 'gpt-5.6-sol', 'gemini-2.5-pro'],
      officialDocsUrl: 'https://code.visualstudio.com/docs/copilot/overview',
      launchCommand: 'VS Code → Extensions → Cline / Roo Code',
      firstLaunchNote: l('使用 Claude 模型时，在 Cline / Roo Code 中选 Anthropic + 自定义 Base URL 可保留原生 Prompt 缓存。', 'Choose Anthropic + custom base URL in Cline/Roo for Claude models to preserve prompt caching.'),
      ccSwitch: {
        supported: false,
        oneClickInstallInApp: false,
        appIds: [],
        appLabel: l('扩展内填写（Claude/Codex 插件自动继承 CC Switch）', 'Extension settings (Claude/Codex extensions share CC Switch)'),
        sidebarName: 'VS Code',
        installNote: l('Claude Code / Codex 插件直接继承 CC Switch 配置；Cline / Roo Code 在插件设置面板填下方参数。', 'Claude Code & Codex extensions reuse CC Switch; configure Cline/Roo directly in the extension settings.'),
        endpoint: v1Base,
        protocolLabel: 'OpenAI / Anthropic',
        defaultModel: 'YOUR_MODEL_ID',
        importHint: l('在 VS Code 扩展设置粘贴下方 Base URL 与本站 API 密钥即可。', 'Paste the Base URL and your site API key into extension settings.'),
      },
      manualSteps: [
        {
          title: l('在 Cline / Roo Code 扩展设置填写本站参数', 'Enter gateway settings in Cline / Roo Code'),
          description: l(
            '打开扩展右上角设置：多模型通用选 OpenAI Compatible（填 /v1）；追求 Claude 原生缓存选 Anthropic（填裸域名）。',
            'Open extension settings: pick OpenAI Compatible (/v1) for multi-model keys, or Anthropic (bare origin) for native Claude caching.',
          ),
          genericSnippets: [
            {
              label: l('OpenAI Compatible（推荐智能路由多模型）', 'OpenAI Compatible (Smart Routing / multi-model)'),
              code: [
                'API Provider: OpenAI Compatible',
                `Base URL:     ${v1Base}`,
                'API Key:      YOUR_API_KEY',
                `Model ID:     ${openCodeModel}`,
              ].join('\n'),
            },
            {
              label: l('Anthropic 原生（支持 Prompt 缓存）', 'Anthropic Native (with Prompt Caching)'),
              code: [
                'API Provider:        Anthropic',
                `Use custom base URL: ${origin}`,
                'Anthropic API Key:   YOUR_API_KEY',
                `Model:               ${claudeModel !== 'YOUR_MODEL_ID' ? claudeModel : 'claude-sonnet-4-6'}`,
              ].join('\n'),
            },
          ],
        },
      ],
      verifyCommands: {
        unix: `curl ${v1Base}/models \\\n  -H "Authorization: Bearer YOUR_API_KEY"`,
        powershell: `Invoke-RestMethod -Uri "${v1Base}/models" -Headers @{ Authorization = "Bearer YOUR_API_KEY" }`,
        cmd: `curl ${v1Base}/models -H "Authorization: Bearer YOUR_API_KEY"`,
      },
      examplePrompts: [
        l('阅读当前打开的文件，排查潜在空指针与未处理异常。', 'Inspect the active file for null-safety issues and unhandled exceptions.'),
      ],
      tips: [
        l('在 VS Code 用 Claude Code 或 Codex 官方扩展，用 CC Switch 配好后重载窗口就能直接共享，不必重复填密钥。', 'Official Claude Code and Codex VS Code extensions inherit CC Switch configs automatically after reloading the window.'),
      ],
      faq: [
        {
          question: l('Cline / Roo Code 里的 Model ID 从哪找？', 'Where do I find valid Model IDs for Cline/Roo?'),
          answer: l('前往本站「模型广场」直接复制，或用你的密钥请求 GET /v1/models 获取准确列表。', 'Copy exact IDs from this site’s Model Plaza or GET /v1/models with your key.'),
        },
      ],
      troubleshooting: [
        {
          question: l('提示上下文或最大输出超限', 'Context window or max output error'),
          answer: l('检查扩展自定义模型配置里的 Max Output Tokens，不要超过目标模型的实际输出上限。', 'Check Max Output Tokens in the extension’s custom model settings so it stays within upstream limits.'),
        },
      ],
    },
  ]

  const keyConcepts: AgentConceptCard[] = [
    {
      title: l('单分组 vs 智能路由（怎么建 Key 最省事）', 'Single Group vs. Smart Routing'),
      description: l(
        '在本站创建 API 密钥时，单分组适合锁定单一平台和倍率；开启「智能路由」可以按优先级同时绑定多个不同平台分组——一把密钥就能反复点「导入到 CCS」配给所有 Agent，网关按协议与模型自动选路并粘住会话。',
        'Single-group keys lock to one platform and multiplier; Smart Routing binds multiple groups in order so one key can be imported into every CC Switch agent with protocol-aware routing and session stickiness.',
      ),
    },
    {
      title: l('为什么「导入到 CCS」只在明文回执里出现？', 'Where to find "Import to CCS"'),
      description: l(
        '出于安全考虑，密钥列表只展示脱敏前缀。创建密钥后的明文回执弹窗（或点击某行「查看密钥」输入登录密码本地解密后）会显示「导入到 CCS」按钮，点击即可通过 ccswitch:// 写入完整配置。',
        'Key lists only store masked prefixes. Click "Import to CCS" right in the newly created receipt—or click "Reveal Key" with your password anytime later to import that key into another agent.',
      ),
    },
    {
      title: l('自动带余额脚本 & 实时查用量', 'Built-in Balance Script & Usage Tracking'),
      description: l(
        '每次通过本站「导入到 CCS」，都会自动把余额与用量查询脚本一起写进 CC Switch（每 30 分钟刷新）；你也可以随时在本站「使用记录」查看每条请求的缓存命中率、Token 构成与真实扣费。',
        'Every "Import to CCS" link attaches a 30-minute balance query script for CC Switch. You can also inspect per-request cache hits and finalized billing under Console → Usage Records.',
      ),
    },
    {
      title: l('Base URL 口诀与自动容错', 'Base URL Rules & Auto-Folding'),
      description: l(
        '用「导入到 CCS」会自动填对所有地址。手动填时记住：Anthropic 填裸域名、OpenAI/Grok 填 /v1（这两类即使多写或漏写 /v1 网关都会自动纠正）；唯一例外是 Gemini 原生走 /v1beta，绝对不能加 /v1。',
        '"Import to CCS" fills every URL accurately. Manually: bare origin for Anthropic, /v1 for OpenAI/Grok (extra/missing /v1 is auto-folded), and bare origin only for Gemini (/v1beta).',
      ),
    },
  ]

  const tradeoffs: AgentTradeoffItem[] = [
    {
      left: l('CC Switch「应用」一键安装 + 站内一键导入', 'CC Switch Apps + Site "Import to CCS"'),
      right: l('站内「使用密钥」手动复制配置', 'Console "Use Key" Manual Config'),
      summary: l(
        '日常电脑开发首选 CC Switch：装智能体、切换本站多个密钥、看余额、管 MCP 全在一个界面；无桌面环境的远程 Linux 服务器或 CI 流水线用「使用密钥」复制环境变量/配置文件即可。',
        'Use CC Switch on macOS/Windows/Linux desktops for one-click installs, switching, MCP, and balance; use Console → "Use Key" snippets on headless servers or CI.',
      ),
    },
    {
      left: l('智能路由（一把 Key 通配多款 Agent）', 'Smart Routing Key'),
      right: l('按工具拆分单分组 Key', 'Per-Tool Single-Group Keys'),
      summary: l(
        '新手推荐一把智能路由 Key 省心通配并支持同平台故障自动兜底；需要给不同工具单独设额度上限、单独统计花费或锁定低倍率分组时，按工具拆成多把 Key。',
        'One Smart Routing key is easiest for beginners with automatic fallback; split into separate keys when you want per-agent spend caps or separate usage tracking.',
      ),
    },
    {
      left: l('终端 CLI Agent（Claude Code / Codex / OpenCode）', 'Terminal CLI Agents'),
      right: l('IDE / 插件（Cursor / Cline）', 'IDE / Extensions'),
      summary: l(
        '终端 Agent 擅长跨文件重构、自动跑测试和 Git 工作流，且全部支持 CC Switch 托管；习惯图形化边看 Diff 边改代码可选 Cursor 或 VS Code + Cline。',
        'Terminal agents excel at repo-wide edits, tests, and Git under CC Switch; Cursor and VS Code + Cline excel at visual inline diffs.',
      ),
    },
  ]

  const globalTroubleshooting: AgentAccordionItem[] = [
    {
      question: l('在「API 密钥」列表里没看到「导入到 CCS」按钮怎么办？', 'Where is the "Import to CCS" button in the API Keys table?'),
      answer: l(
        '导入需要完整密钥明文，因此按钮放在「新建密钥完成后的回执弹窗」和每行操作菜单的「查看密钥」解密弹窗中。点开回执即可看到「导入到 CCS」，且可以连续导入给多个不同应用。',
        'Importing requires the plaintext key, so the button lives inside the post-create receipt and the "Reveal Key" dialog. Open the receipt to import the same key into as many apps as you need.',
      ),
    },
    {
      question: l('点击「导入到 CCS」没反应或提示未安装 CC Switch', '"Import to CCS" does nothing or warns CC Switch is not installed'),
      answer: l(
        '深链依赖系统注册的 ccswitch:// 协议。Windows 请用 .msi 安装版（便携版需先手动启动一次）；如浏览器拦截外部协议请点击允许。也可以展开对应 Agent 指南中的「手动填写参数」在 CC Switch 里点 + 新增。',
        'Deeplinks rely on the ccswitch:// handler (use the .msi installer on Windows or launch portable once, and allow external protocol prompts). You can also add the fields manually inside CC Switch.',
      ),
    },
    {
      question: l('在 CC Switch 点了「启用」，但终端里启动 Agent 没生效或报 401', 'Activated in CC Switch, but terminal still fails or returns 401'),
      answer: l(
        '旧终端窗口里可能残留之前手动 export 过的环境变量（优先级高于配置文件）。关闭旧终端重新打开一个新窗口再运行启动命令；同时确认本站密钥已启用且余额充足。',
        'Stale shell environment variables override config files written by CC Switch. Open a fresh terminal window and verify your key is active with available balance.',
      ),
    },
    {
      question: l('403 Forbidden 或提示模型不可用（Model is not available）', '403 Forbidden or "Model is not available"'),
      answer: l(
        '403 通常是余额不足、密钥额度/时间窗限额触发、来源 IP 不在白名单或分组权限未开；模型不可用说明所选模型不在该密钥绑定的分组里。在「导入到 CCS」弹窗里直接从下拉列表选本密钥拉出来的模型最稳妥。',
        '403 points to balance, quota/window caps, IP allow-list, or group permissions; model unavailable means the bound group lacks that model. Pick a model from the auto-fetched dropdown in "Import to CCS".',
      ),
    },
    {
      question: l('在 CC Switch「应用」页安装某个智能体失败怎么办？', 'What if installing an agent inside CC Switch → Apps fails?'),
      answer: l(
        '检查网络代理与本地是否已安装 Node.js（部分工具如 Codex / Gemini CLI / Copilot CLI 依赖 npm）。安装前可在终端执行 node -v 确认环境正常，再回到 CC Switch 重试安装。',
        'Check your network proxy and ensure Node.js (npm) is installed (`node -v`), then retry Install inside CC Switch → Apps.',
      ),
    },
  ]

  return {
    origin,
    v1BaseUrl: v1Base,
    ccSwitchUrl,
    ccSwitchRepoUrl: CC_SWITCH_REPO_URL,
    ccSwitchApps: CC_SWITCH_APPS,
    siteWorkflowSteps,
    agents,
    keyConcepts,
    tradeoffs,
    globalTroubleshooting,
  }
}

export function resolveAgentGuide(agents: AgentGuide[], agentId: unknown): AgentGuide | null {
  if (typeof agentId !== 'string') return null
  const normalized = agentId.trim().toLowerCase()
  if (!normalized) return null
  return agents.find((agent) => agent.id === normalized) ?? null
}

const HOME_AGENT_ORDER = ['claude-code', 'codex', 'gemini-cli', 'grok-build', 'opencode', 'ccswitch-ecosystem'] as const

export function getHomeAgentHighlights(locale: string, originValue: string): HomeAgentHighlight[] {
  const catalog = getAgentSetupCatalog(locale, originValue)
  const zh = locale.toLowerCase().startsWith('zh')
  const byId = new Map(catalog.agents.map((agent) => [agent.id, agent]))
  return HOME_AGENT_ORDER.flatMap((id) => {
    const agent = byId.get(id)
    if (!agent) return []
    return [
      {
        id: agent.id,
        name: agent.id === 'ccswitch-ecosystem' ? (zh ? 'Hermes / Pi / DSH 等 7 款' : 'Hermes / Pi / DSH & 7 more') : agent.name,
        vendor: agent.vendor,
        iconKey: agent.iconKey,
        platform: agent.platform,
        summary: agent.summary,
        siteGroupAdvice: agent.siteGroupAdvice,
        badge: agent.ccSwitch.supported
          ? zh ? 'CCS 一键装+导' : 'CCS 1-click'
          : zh ? 'IDE 直连' : 'IDE built-in',
        supportsCcSwitch: agent.ccSwitch.supported,
      },
    ]
  })
}
