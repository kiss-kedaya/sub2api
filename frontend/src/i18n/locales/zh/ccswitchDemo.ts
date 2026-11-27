export interface CcSwitchDemoMessages {
  tabs: {
    provider: string
    proxy: string
    stats: string
  }
  window: {
    addProvider: string
    more: string
    edit: string
    collapseSidebar: string
    expandSidebar: string
    pages: {
      common: {
        retry: string
        cancel: string
        edit: string
        delete: string
        undo: string
        undone: string
        refresh: string
        justNow: string
        minutesAgo: string
        hoursAgo: string
        daysAgo: string
        listSeparator: string
      }
      matrix: {
        ompTitle: string
        ompHelp: string
        ompDocs: string
        cellOn: string
        cellOff: string
        popCount: string
        enableAll: string
        enableRest: string
        disableAll: string
        scopeAll: string
        scopeSearch: string
        toastEnabled: string
        toastDisabled: string
      }
      mcp: {
        helpTitle: string
        help: string
        importFromApps: string
        add: string
        moreActions: string
        resync: string
        copyAll: string
        searchPlaceholder: string
        count: string
        countFiltered: string
        overwriteNote: string
        columnName: string
        notEnabled: string
        copyJson: string
        openDocs: string
        enableEverywhere: string
        disableEverywhere: string
        noMatch: string
        clearSearch: string
        noun: string
        toastWritten: string
        toastResynced: string
        toastRowAllOn: string
        toastRowAllOff: string
        toastCopiedOne: string
        toastDeleted: string
      }
      skills: {
        helpTitle: string
        help: string
        headerUpdates: string
        headerUpdatesShow: string
        headerUpdatesClear: string
        add: string
        addDiscover: string
        addZip: string
        addImport: string
        moreActions: string
        checkUpdates: string
        checking: string
        restore: string
        repos: string
        storage: string
        viewInstalled: string
        viewDiscover: string
        searchPlaceholder: string
        selectAll: string
        filterAll: string
        filterUpdates: string
        filterNone: string
        sourceButton: string
        sourceLocal: string
        updateAllCount: string
        selected: string
        enableTo: string
        disable: string
        update: string
        uninstall: string
        updateAvailable: string
        openDocs: string
        openDir: string
        copiedName: string
        noun: string
        bannerUnmanaged: string
        reviewImport: string
        ignore: string
        noMatch: string
        noFilterMatch: string
        clearFilters: string
        installTo: string
        discoverSearch: string
        sourceRepos: string
        reposAll: string
        onlyUninstalled: string
        reload: string
        install: string
        installing: string
        installed: string
        installs: string
        toastInstalled: string
        noUpdates: string
        updatingProgress: string
        confirmUpdateAll: string
        confirmUpdateBody: string
        confirmUpdateButton: string
      }
      prompts: {
        helpTitle: string
        help: string
        helpHermesTitle: string
        helpHermes: string
        add: string
        moreActions: string
        importCurrent: string
        importDuplicate: string
        openFolder: string
        copyFilePath: string
        appSelect: string
        appNoteDesktop: string
        appNoteOpenclaw: string
        targetFile: string
        fileEmpty: string
        searchPlaceholder: string
        noSearchResults: string
        clearSearch: string
        enabled: string
        enable: string
        disable: string
        copyToApps: string
        copyContent: string
        deleteBlocked: string
        toastEnabled: string
        toastDisabled: string
        toastDeleted: string
        toastDeletedSub: string
        copiedContent: string
        copiedPath: string
      }
      sessions: {
        helpTitle: string
        help: string
        moreActions: string
        refreshList: string
        selectMany: string
        preferredTerminal: string
        whereMenu: string
        allApps: string
        noSessions: string
        searchPlaceholder: string
        groupByTime: string
        groupByProject: string
        unknownDirectory: string
        groupMeta: string
        today: string
        yesterday: string
        thisWeek: string
        earlier: string
        lastPrefix: string
        archived: string
        resumeIn: string
        copyResumeCommand: string
        copySessionId: string
        copySourcePath: string
        emptySearch: string
        clearSearch: string
        listRefreshed: string
        resumeCommandCopied: string
        launchedIn: string
        sessionIdCopied: string
        sourcePathCopied: string
      }
      reader: {
        back: string
        prev: string
        next: string
        moreResume: string
        copyCdResume: string
        changeTerminal: string
        codexResumeNote: string
        copyAsMarkdown: string
        exportMarkdown: string
        reload: string
        find: string
        you: string
        timelineSummary: string
        summaryCommands: string
        summaryFiles: string
        summaryErrors: string
        filterAll: string
        filterChat: string
        filterChanges: string
        outlineTitle: string
        outlineSteps: string
        questionsCount: string
        toolsCount: string
        noChanges: string
        expandTimeline: string
        noResumeHermes: string
      }
      auth: {
        helpTitle: string
        help: string
        beta: string
        addAccount: string
        groupHelpTitle: string
        copilotHelp: string
        chatgptHelp: string
        xaiHelp: string
        default: string
        needsReauth: string
        reauth: string
        setDefault: string
        removeAccount: string
        signedInOn: string
        usedBy: string
        xaiReauthNote: string
        empty: string
        quotaLeft: string
        usedUp: string
        refresh: string
        updating: string
        premium: string
        fiveHour: string
        weekly: string
        claudeHelp: string
        kimiHelp: string
        agyHelp: string
        claudeEmpty: string
        kimiEmpty: string
        agyEmpty: string
        addedOn: string
        kimiCurrent: string
        kimiUseAccount: string
        kimiPrimary: string
        kimiRegion: string
        kimiMainland: string
        kimiGlobal: string
        kimiSignIn: string
        kimiDismiss: string
        kimiNoSubscription: string
        agyCurrent: string
        agySwitchTo: string
        agySwitched: string
        geminiFiveHour: string
        geminiWeekly: string
        claudeGptWeekly: string
        browserNote: string
        reopenPage: string
        resetLabel: string
        resetValue: string
        resetExpiresOn: string
        resetTitle: string
        resetTimes: string
        resetInTime: string
        resetShowAll: string
        creditsLabel: string
        creditsUsd: string
        pendingAdd: string
        pendingReauth: string
        enterCode: string
        copyCode: string
        openPage: string
        pendingNote: string
        codeCopied: string
        toastAdded: string
        toastReauthed: string
        deploymentType: string
        enterpriseDomain: string
        enterprisePlaceholder: string
        loginWithGitHub: string
        refreshAll: string
        openBrowserTitle: string
        openBrowserBody: string
        copyLink: string
        linkCopied: string
        linkNote: string
        linkCodeNote: string
        dshHelp: string
        dshEmpty: string
        dshShared: string
        dshSignOut: string
        dshBalance: string
        dshBonus: string
        workbuddyHelp: string
        workbuddyEmpty: string
        workbuddyAddedOn: string
        workbuddyCurrent: string
        workbuddySwitchTo: string
        workbuddySwitched: string
        workbuddyCredits: string
        workbuddyLeftOf: string
      }
      apps: {
        helpTitle: string
        help: string
        lastChecked: string
        checkUpdates: string
        checking: string
        updateAll: string
        moreActions: string
        diagnose: string
        manualCommands: string
        sectionInstalled: string
        sectionNotInstalled: string
        columnVersion: string
        columnVisible: string
        visibleHelp: string
        desktopHelp: string
        workbuddyHelp: string
        desktopConnected: string
        newVersion: string
        notInstalled: string
        install: string
        update: string
        otherInstalls: string
        installsFound: string
        showInSidebar: string
        installHintScript: string
        installHintNpm: string
        installedToast: string
        updatedToast: string
      }
      settings: {
        back: string
        title: string
        sections: {
          general: string
          appConfig: string
          routing: string
          network: string
          data: string
          about: string
        }
        onlyGeneral: string
        appearance: string
        language: string
        theme: string
        themeSystem: string
        themeLight: string
        themeDark: string
        sidebarAndHeader: string
        visibleApps: string
        goToApps: string
        showProfileSwitcher: string
        showProfileSwitcherHelp: string
        updates: string
        checkToolUpdates: string
        checkToolUpdatesHelp: string
        windowAndTerminal: string
        launchOnStartup: string
        silentStartup: string
        silentStartupHelp: string
        minimizeToTray: string
        minimizeToTrayHelp: string
        preferredTerminal: string
        terminalHelp: string
      }
    }
    nav: {
      mcp: string
      skills: string
      prompts: string
      sessions: string
      auth: string
      usage: string
      apps: string
      settings: string
      todayCost: string
    }
    modes: {
      direct: string
      route: string
      stack: string
    }
    modeHelp: {
      title: string
      direct: string
      route: string
      stack: string
    }
    activate: {
      viewing: string
      route: string
      stack: string
      direct: string
    }
    status: {
      directLead: string
      routeLead: string
      stackLead: string
      stackValue: string
    }
    chip: {
      official: string
      default: string
      models: string
      direct: string
      directWhenDirect: string
      needsRoute: string
      siteRecommended: string
      siteOneClickImport: string
      siteImported: string
    }
    cardStatus: {
      inUse: string
      routing: string
      currentDefault: string
      defaultAfterSwitch: string
    }
    action: {
      switch: string
      routeHere: string
      setDefault: string
      add: string
      remove: string
      enable: string
      exitAndUse: string
      openTerminal: string
    }
    section: {
      stackDefault: string
      stackDefaultAfterSwitch: string
      added: string
      available: string
      sharedListNote: string
      sharedListHelp: string
    }
    reason: {
      noRoute: string
      officialStack: string
      noStack: string
      needsRoute: string
      openaiChat: string
      desktopMapping: string
      workbuddyOnlyOne: string
    }
    quota: {
      tierLeft: string
      tierShort: string
      balance: string
      tiers: {
        fiveHour: string
        weekly: string
        fable: string
      }
      shortTiers: {
        fiveHour: string
        weekly: string
        fable: string
      }
    }
    usage: {
      title: string
      all: string
      help: string
      justSynced: string
      syncing: string
      syncTip: string
      settings: string
      totalCost: string
      totalRequests: string
      realTokens: string
      cacheHitRate: string
      timeRange: string
      presets: {
        today: string
        '1d': string
        '7d': string
        '14d': string
        '30d': string
        all: string
      }
      customRange: string
      customRangeHint: string
      startTime: string
      endTime: string
      liveEndTime: string
      liveEndNow: string
      prevMonth: string
      nextMonth: string
      cancel: string
      confirm: string
      trendTitle: string
      hourly: string
      daily: string
      heatmapTitle: string
      heatmapSubtitle: string
      less: string
      more: string
      metrics: {
        cost: string
        requests: string
        tokens: string
        hitRate: string
      }
      moreMetrics: string
      realTokensHelpTitle: string
      realTokensHelp: string
      hitRateHelpTitle: string
      hitRateHelp: string
      freshInput: string
      output: string
      cacheWrite: string
      cacheRead: string
      providerFilter: string
      modelFilter: string
      statusCode: string
      logColumns: string
      tabs: {
        logs: string
        providers: string
        models: string
      }
      columns: {
        time: string
        app: string
        provider: string
        model: string
        freshInput: string
        output: string
        cacheRead: string
        cost: string
        speed: string
        firstToken: string
        requests: string
        tokens: string
        successRate: string
        speedHelpTitle: string
        speedHelp: string
      }
      weekdays: string[]
      months: string[]
    }
    editor: {
      titleAdd: string
      titleEdit: string
      nameLabel: string
      endpointLabel: string
      apiKeyLabel: string
      modelLabel: string
      keyReadyHint: string
      keyPendingHint: string
      save: string
      cancel: string
      savedToast: string
    }
    guided: {
      step0Badge: string
      step0Text: string
      step0ActionInstall: string
      step0ActionToggle: string
      step1Badge: string
      step1Text: string
      step1Action: string
      step2Badge: string
      step2Text: string
      step2Action: string
      step3Badge: string
      step3Text: string
      step3Action: string
      step3DoneText: string
      step3DoneToast: string
      dismiss: string
      reopen: string
      spotlightInstall: string
      spotlightToggle: string
      spotlightCreateKey: string
      spotlightImport: string
      spotlightActivate: string
    }
  }
}

export const ccswitchDemoZh: CcSwitchDemoMessages = {
  tabs: {
    provider: '供应商切换',
    proxy: '路由与聚合',
    stats: '用量统计',
  },
  window: {
    addProvider: '添加供应商',
    more: '更多',
    edit: '编辑',
    collapseSidebar: '收起侧栏',
    expandSidebar: '展开侧栏',
    pages: {
      common: {
        retry: '重试',
        cancel: '取消',
        edit: '编辑',
        delete: '删除',
        undo: '撤销',
        undone: '已撤销',
        refresh: '刷新',
        justNow: '刚刚',
        minutesAgo: '{count} 分钟前',
        hoursAgo: '{count} 小时前',
        daysAgo: '{count} 天前',
        listSeparator: '、',
      },
      matrix: {
        ompTitle: 'Oh My Pi 的实际加载范围',
        ompHelp:
          'Oh My Pi 除了 ~/.omp/agent 自己的配置，还会从其他 Agent 的目录发现 MCP 服务器、技能、命令、钩子、规则和设置（如 Claude 的 ~/.claude、Codex 的 ~/.codex 等）。要让 Oh My Pi 的实际生效内容与 CC Switch 界面一致，请在 ~/.omp/agent/config.yml 用 disabledProviders 关闭不需要的发现来源（id 与文档一致，如 claude、codex、gemini、opencode、github、agents）。',
        ompDocs: '怎样让 Oh My Pi 不读取其他 Agent 的配置',
        cellOn: '{name} · {app}：已启用',
        cellOff: '{name} · {app}：未启用',
        popCount: '已在 {on} / {total} 个 {noun} 上启用',
        enableAll: '全部启用',
        enableRest: '启用其余 {count} 个',
        disableAll: '全部关闭',
        scopeAll: '只作用于当前列表里的 {count} 个',
        scopeSearch: '只作用于搜索出的 {count} 个',
        toastEnabled: '已在 {app} 启用 {count} 个 {noun}',
        toastDisabled: '已在 {app} 关闭 {count} 个 {noun}',
      },
      mcp: {
        helpTitle: 'MCP 怎么同步',
        help: '以这里为准：写进你勾选的应用的配置文件，切换供应商时按这里重写。Claude Desktop、OpenClaw 不支持，所以表格里没有这两列；Pi 需要 1.0 及以上，且不支持 SSE。',
        importFromApps: '从应用导入',
        add: '添加 MCP',
        moreActions: 'MCP 的更多操作',
        resync: '按这里的开关重写各应用配置',
        copyAll: '复制全部为 mcpServers JSON',
        searchPlaceholder: '搜索名称、命令或地址',
        count: '{count} 个 MCP',
        countFiltered: '{shown} / {count} 个 MCP',
        overwriteNote: '直接改应用配置里的同名项会被覆盖',
        columnName: '名称',
        notEnabled: '未启用',
        copyJson: '复制为 JSON',
        openDocs: '打开文档',
        enableEverywhere: '在所有应用启用',
        disableEverywhere: '全部关闭',
        noMatch: '没有名称、命令或地址匹配“{query}”的 MCP',
        clearSearch: '清除搜索',
        noun: 'MCP',
        toastWritten: '已写入 {app}',
        toastResynced: '已按这里的开关重新写入 {count} 个应用的配置',
        toastRowAllOn: '{id} 已在全部 {count} 个应用启用',
        toastRowAllOff: '{id} 已在所有应用关闭',
        toastCopiedOne: '已复制 {id} 的 JSON（含环境变量和请求头的值）',
        toastDeleted: '已删除 {id}',
      },
      skills: {
        helpTitle: 'Skills 怎么同步',
        help: '主副本放在 CC Switch 的目录里，再同步到你勾选的应用。Claude Desktop、OpenClaw 不支持 Skills，所以表格里没有这两列。',
        headerUpdates: '{count} 个可更新',
        headerUpdatesShow: '只看可更新的 Skill',
        headerUpdatesClear: '显示全部 Skill',
        add: '添加 Skill',
        addDiscover: '在发现里浏览',
        addZip: '从 ZIP 安装',
        addImport: '导入本机已有',
        moreActions: 'Skills 的更多操作',
        checkUpdates: '检查更新',
        checking: '检查中...',
        restore: '从备份恢复',
        repos: '仓库管理',
        storage: '存储与同步',
        viewInstalled: '已安装 {count}',
        viewDiscover: '发现',
        searchPlaceholder: '搜索名称、目录、描述或仓库',
        selectAll: '全选当前列表',
        filterAll: '全部',
        filterUpdates: '可更新',
        filterNone: '未在任何应用启用',
        sourceButton: '来源：{source}',
        sourceLocal: '本地',
        updateAllCount: '全部更新（{count} 个）',
        selected: '已选 {count}',
        enableTo: '启用到',
        disable: '停用',
        update: '更新',
        uninstall: '卸载',
        updateAvailable: '可更新',
        openDocs: '打开文档',
        openDir: '打开目录',
        copiedName: '已复制 {name}',
        noun: 'Skill',
        bannerUnmanaged: '本机有 {count} 个 Skill 不在 CC Switch 管理中。',
        reviewImport: '查看并导入',
        ignore: '忽略',
        noMatch: '没有匹配“{query}”的 Skill',
        noFilterMatch: '没有符合筛选条件的 Skill',
        clearFilters: '清除筛选',
        installTo: '安装到：',
        discoverSearch: '搜索 Skill 或仓库',
        sourceRepos: '仓库 · {count}',
        reposAll: '仓库：全部',
        onlyUninstalled: '只看未安装',
        reload: '重新读取仓库',
        install: '安装',
        installing: '安装中',
        installed: '已安装 · {count} 个应用',
        installs: '{count} 次安装',
        toastInstalled: '已安装 {name}，并在 {apps} 启用',
        noUpdates: '所有技能已是最新版本',
        updatingProgress: '正在更新 {done}/{total}…',
        confirmUpdateAll: '更新 {count} 个 Skill？',
        confirmUpdateBody: '更新会用仓库里的内容替换本地目录。本地改过的地方只保留在备份里（{path}）。',
        confirmUpdateButton: '更新 {count} 个',
      },
      prompts: {
        helpTitle: '提示词怎么生效',
        help: '每个应用同时只启用一条，它就是目标文件的内容。在别处改了文件，下次打开本页时会收进启用中的那条；文件里有本页没有的内容，启用前先存成「原始提示词」。',
        helpHermesTitle: 'SOUL.md 怎么生效',
        helpHermes:
          'SOUL.md 是 Hermes 的人设，同时只启用一条。在别处改了文件，下次打开本页时会收进启用中的那条；文件里有本页没有的内容，启用前先存成「原始提示词」。',
        add: '添加提示词',
        moreActions: '提示词的更多操作',
        importCurrent: '导入当前的 {file}',
        importDuplicate: '文件内容已经在列表里：「{name}」',
        openFolder: '打开所在文件夹',
        copyFilePath: '复制文件路径',
        appSelect: '选择应用',
        appNoteDesktop: 'Claude Desktop 用的是 Claude Code 的提示词。',
        appNoteOpenclaw: 'OpenClaw 在它自己页面的「工作区」里管理。',
        targetFile: '目标文件',
        fileEmpty: '空文件',
        searchPlaceholder: '搜索名称、说明或正文',
        noSearchResults: '没有名称、说明或正文匹配“{query}”的提示词',
        clearSearch: '清除搜索',
        enabled: '已启用',
        enable: '启用',
        disable: '停用',
        copyToApps: '复制到其他应用',
        copyContent: '复制正文',
        deleteBlocked: '启用中的不能删除，先停用',
        toastEnabled: '已启用「{name}」，已覆盖 {path}',
        toastDisabled: '已停用「{name}」，{path} 已清空',
        toastDeleted: '已删除「{name}」',
        toastDeletedSub: '只从 CC Switch 里删除，{path} 没有改动',
        copiedContent: '已复制「{name}」的正文',
        copiedPath: '已复制 {path}',
      },
      sessions: {
        helpTitle: '会话从哪里来',
        help: 'CC Switch 读取各个应用保存在本机的会话记录，位置见右上角 ⋯ 里的「会话记录在哪里」。Claude Desktop 没有单独的会话记录。',
        moreActions: '会话的更多操作',
        refreshList: '刷新列表',
        selectMany: '选择多个',
        preferredTerminal: '首选终端',
        whereMenu: '会话记录在哪里',
        allApps: '全部应用',
        noSessions: '无会话',
        searchPlaceholder: '搜索标题、目录、首末消息或会话 ID',
        groupByTime: '按时间',
        groupByProject: '按项目',
        unknownDirectory: '未知目录',
        groupMeta: '{count} 个会话 · {time}',
        today: '今天',
        yesterday: '昨天',
        thisWeek: '本周',
        earlier: '更早',
        lastPrefix: '最后：{text}',
        archived: '已归档',
        resumeIn: '在 {terminal} 中恢复',
        copyResumeCommand: '复制恢复命令',
        copySessionId: '复制会话 ID',
        copySourcePath: '复制源文件路径',
        emptySearch: '没有标题、目录或首末消息匹配“{query}”的会话',
        clearSearch: '清除搜索',
        listRefreshed: '已刷新会话列表',
        resumeCommandCopied: '已复制恢复命令',
        launchedIn: '已在 {terminal} 中运行恢复命令',
        sessionIdCopied: '已复制会话 ID',
        sourcePathCopied: '已复制源文件路径',
      },
      reader: {
        back: '返回',
        prev: '上一个',
        next: '下一个',
        moreResume: '更多恢复方式',
        copyCdResume: '复制「进入目录并恢复」命令',
        changeTerminal: '更换首选终端',
        codexResumeNote: '切换过供应商后，旧会话可能无法继续：Codex 按供应商分开保存历史。',
        copyAsMarkdown: '复制整段为 Markdown',
        exportMarkdown: '导出为 Markdown 文件',
        reload: '重新读取',
        find: '查找',
        you: '你',
        timelineSummary: '执行过程 · {steps} 步',
        summaryCommands: '{count} 个命令',
        summaryFiles: '改了 {count} 个文件',
        summaryErrors: '{count} 个失败',
        filterAll: '全部',
        filterChat: '对话',
        filterChanges: '改动',
        outlineTitle: '对话目录',
        outlineSteps: '{count} 步',
        questionsCount: '{count} 次提问',
        toolsCount: '{count} 次工具调用',
        noChanges: '这个会话没有文件改动',
        expandTimeline: '展开执行过程',
        noResumeHermes: '暂不支持从命令行恢复 Hermes 会话',
      },
      auth: {
        helpTitle: '授权中心是什么',
        help: '把 GitHub Copilot、ChatGPT、xAI 订阅当作供应商用。凭据由路由服务注入，相关供应商要在路由模式下使用（Codex 官方登录绑定除外）。Antigravity 账号用来切换 agy 的官方登录。',
        beta: 'Beta',
        addAccount: '添加账号',
        groupHelpTitle: '{service} 账号怎么用',
        copilotHelp: '用于 Claude Code、Claude Desktop 的 GitHub Copilot 预设。没指定账号的供应商用「默认」账号。',
        chatgptHelp: '用于 Claude Code、Claude Desktop 的 ChatGPT 预设，以及 Codex 官方登录绑定。没指定账号的供应商用「默认」账号。',
        xaiHelp: '用于 Claude Code、Claude Desktop、Codex 的 xAI 预设。没指定账号的供应商用「默认」账号。',
        default: '默认',
        needsReauth: '需要重新登录',
        reauth: '重新登录',
        setDefault: '设为默认',
        removeAccount: '删除账号',
        signedInOn: '{date}登录',
        usedBy: '{count} 个供应商在用',
        xaiReauthNote: '登录凭据已失效，用到它的供应商无法使用',
        empty: '还没有登录 {service} 账号。',
        quotaLeft: '剩余 {value}%',
        usedUp: '已用完',
        refresh: '刷新额度',
        updating: '更新中…',
        premium: '高级请求',
        fiveHour: '5 小时',
        weekly: '每周',
        claudeHelp:
          'Claude Code 的官方订阅账号（Pro / Max / Team）。在 Claude 官方卡上选一个账号，切到这张卡后，Claude Code 的登录会实时换成这个号；想同时用几个号，从卡片「打开终端」。登录令牌只留在本机，不上传、不刷新、不登出。第一次切换会自动收进 Claude Code 现在登录的号；切号后 Remote Control 会断开，提示缓存要重建。',
        kimiHelp:
          '可添加多个 Kimi 编程套餐账号，每个号在 Kimi Code 里有自己的供应商和模型。「使用此账号」会把 Kimi Code 的默认模型换成这个号的，之后新开的会话都用它。Kimi Code 自己登着的号会自动出现在这里，联网搜索和网页抓取始终用它；在这里删掉它只会移出列表，不会让 Kimi Code 退出登录。',
        agyHelp:
          'Antigravity CLI（agy）的官方登录。agy 同时只能登录一个号，「切换」会把 agy 的登录换成选中的账号，之后新开的 agy 都用它。agy 里原来登着的号会自动收进这里，切走也不会丢。',
        claudeEmpty: '还没有添加 Claude 账号。要在多个订阅账号之间切换时在这里登录。',
        kimiEmpty: '还没有 Kimi 账号。Kimi Code 里已登录的号会自动出现在这里。',
        agyEmpty: '还没有 Antigravity 账号。agy 里已登录的号会自动出现在这里。',
        addedOn: '{date}添加',
        kimiCurrent: 'Kimi 在用',
        kimiUseAccount: '使用此账号',
        kimiPrimary: 'Kimi Code 自己的登录',
        kimiRegion: '登录区域',
        kimiMainland: '国内版',
        kimiGlobal: '海外版',
        kimiSignIn: '登录 Kimi',
        kimiDismiss: '移出列表',
        kimiNoSubscription: '未订阅',
        agyCurrent: 'agy 在用',
        agySwitchTo: '切换 agy 到这个账号',
        agySwitched: '已切换，新开的 agy 会使用这个账号。',
        geminiFiveHour: 'Gemini 5 小时',
        geminiWeekly: 'Gemini 每周',
        claudeGptWeekly: 'Claude/GPT 每周',
        browserNote: '已在浏览器打开 {service} 的登录页，登录并同意授权后这里会自动更新。',
        reopenPage: '重新打开登录页',
        resetLabel: '重置',
        resetValue: '剩余 {count} 次',
        resetExpiresOn: '{date}到期',
        resetTitle: '存下的限额重置',
        resetTimes: '{count} 次',
        resetInTime: '{time}后',
        resetShowAll: '查看 {count} 次重置各自的到期时间',
        creditsLabel: 'Credits',
        creditsUsd: '约 {usd}',
        pendingAdd: '添加 {service} 账号 · 等待授权中…',
        pendingReauth: '重新登录 {login} · 等待授权中…',
        enterCode: '在浏览器中输入验证码：',
        copyCode: '复制验证码',
        openPage: '打开授权页面',
        pendingNote: '已在浏览器打开授权页面，完成后这里会自动更新。注意 {service} 的使用条款。',
        codeCopied: '验证码已复制。',
        toastAdded: '已添加 {service} 账号。',
        toastReauthed: '{login} 已重新登录。',
        deploymentType: 'GitHub 部署类型',
        enterpriseDomain: '企业域名',
        enterprisePlaceholder: '例如：company.ghe.com',
        loginWithGitHub: '使用 GitHub 登录',
        refreshAll: '全部刷新',
        openBrowserTitle: '登录时自动打开浏览器',
        openBrowserBody: '关掉后只复制登录链接，由你自己选浏览器打开，适合用独立的浏览器环境管理多个账号。',
        copyLink: '复制登录链接',
        linkCopied: '登录链接已复制。',
        linkNote: '登录链接已复制。在你要用的浏览器里打开，登录并同意授权后这里会自动更新。',
        linkCodeNote: '登录链接已复制。在你要用的浏览器里打开并输入验证码，完成后这里会自动更新。注意 {service} 的使用条款。',
        dshHelp:
          'DeepSeek 账号用浏览器登录（OAuth），可以在这里登录，也可以在 DeepSeek Harness 里登录。登录信息保存在 $DSH_HOME/.credentials.yaml（默认 ~/.dsh/.credentials.yaml），DeepSeek Harness 的 Web 和 Desktop 共用：任一处登录或退出，两端同时生效，运行中的 DeepSeek Harness 会自动重新读取。账号路由的模型用量在用量页单独显示为「DeepSeek 账号（OAuth）」。',
        dshEmpty: '未登录 DeepSeek 账号',
        dshShared: 'Web 与 Desktop 共用',
        dshSignOut: '退出登录',
        dshBalance: '余额 {amount}',
        dshBonus: '赠金 {amount}',
        workbuddyHelp:
          'WorkBuddy 桌面端的官方登录。CC Switch 不能替 WorkBuddy 登录，只能收录它当前登着的账号（整组登录文件的快照）。要切换账号，在账号的「…」里选「切换 WorkBuddy 到这个账号」，或在供应商列表里添加绑定了它的「WorkBuddy 官方登录」卡（同一时间只有一张在「已添加」）。快照只在这台电脑上有效，不会随云同步。',
        workbuddyEmpty: '还没有 WorkBuddy 账号。先在 WorkBuddy 里登录，再收录当前登录的账号。',
        workbuddyAddedOn: '{date}收录',
        workbuddyCurrent: 'WorkBuddy 在用',
        workbuddySwitchTo: '切换 WorkBuddy 到这个账号',
        workbuddySwitched: '已切换到「{name}」，下次打开 WorkBuddy 会使用这个账号。',
        workbuddyCredits: '积分',
        workbuddyLeftOf: '剩余 {remaining} / {total}',
      },
      apps: {
        helpTitle: '应用',
        help: '在这里安装、升级命令行应用，并选择在侧栏显示哪些。',
        lastChecked: '上次检查 {time}',
        checkUpdates: '检查更新',
        checking: '检查中…',
        updateAll: '全部升级（{count}）',
        moreActions: '更多操作',
        diagnose: '诊断安装冲突',
        manualCommands: '手动安装命令',
        sectionInstalled: '已安装 · {count}',
        sectionNotInstalled: '未安装 · {count}',
        columnVersion: '版本',
        columnVisible: '显示',
        visibleHelp:
          '关掉后，这个应用不再出现在侧栏、托盘菜单、MCP / Skills 的应用列，以及提示词和会话的应用下拉里。只是不显示：它的供应商、MCP 等配置都还在，它自己的配置文件也不会被改动。',
        desktopHelp: 'Claude Desktop 是桌面应用，不能在这里安装或升级；CC Switch 通过它的配置文件接入。',
        workbuddyHelp: 'WorkBuddy 是桌面应用，不能在这里安装或升级，它会自己更新；CC Switch 通过它的配置文件（~/.workbuddy/models.json）接入。',
        desktopConnected: '已接入',
        newVersion: '有新版本 {version}',
        notInstalled: '未安装',
        install: '安装',
        update: '升级',
        otherInstalls: '另有 {count} 处安装',
        installsFound: '检测到 {count} 处安装',
        showInSidebar: '在侧栏显示 {name}',
        installHintScript: '安装时运行官方安装脚本，失败时改用 npm',
        installHintNpm: '通过 npm 安装',
        installedToast: '已安装 {app} 并显示在左侧栏',
        updatedToast: '已将 {app} 升级到 {version}',
      },
      settings: {
        back: '返回',
        title: '设置',
        sections: {
          general: '通用',
          appConfig: '应用配置',
          routing: '本地路由',
          network: '网络',
          data: '数据',
          about: '关于',
        },
        onlyGeneral: '演示里只展示「通用」这一页',
        appearance: '外观',
        language: '界面语言',
        theme: '明暗',
        themeSystem: '跟随系统',
        themeLight: '月之亮面',
        themeDark: '月之暗面',
        sidebarAndHeader: '侧栏与页头',
        visibleApps: '在侧栏显示哪些应用',
        goToApps: '前往「应用」',
        showProfileSwitcher: '显示项目切换',
        showProfileSwitcherHelp: '在主页面顶部显示项目切换入口',
        updates: '各应用的新版本',
        checkToolUpdates: '启动时检查各应用的新版本',
        checkToolUpdatesHelp:
          '打开后，每次启动 CC Switch 会查一次已安装的命令行应用有没有新版本，查到了就在侧栏「应用」上显示一个圆点。不会自动升级。',
        windowAndTerminal: '窗口与终端',
        launchOnStartup: '开机自启',
        silentStartup: '静默启动',
        silentStartupHelp: '程序启动时不显示主窗口，仅在系统托盘运行',
        minimizeToTray: '关闭时最小化到托盘',
        minimizeToTrayHelp:
          '打开时，点关闭按钮只隐藏窗口，CC Switch 留在系统托盘里继续运行（轻量模式在托盘菜单里）；关闭时点关闭按钮直接退出。',
        preferredTerminal: '首选终端',
        terminalHelp: '点供应商卡片上的「打开终端」时用哪个终端。选的终端不可用时，改用系统默认终端。',
      },
    },
    nav: {
      mcp: 'MCP',
      skills: 'Skills',
      prompts: '提示词',
      sessions: '会话',
      auth: '授权中心',
      usage: '用量统计',
      apps: '应用',
      settings: '设置',
      todayCost: '今日 {cost}',
    },
    modes: {
      direct: '直连',
      route: '路由',
      stack: '聚合',
    },
    modeHelp: {
      title: '连接模式的区别',
      direct: '直连：CC Switch 不经手任何请求，客户端直接连所选供应商，体验最原生。',
      route: '路由：请求经本机 CC Switch 转发，可热切换供应商，并支持故障转移、格式转换等高级功能。',
      stack: '聚合：多家供应商的模型同列在 /model，在客户端内即可无感切换。',
    },
    activate: {
      viewing: '你在查看「{view}」，{app} 目前是{active}。',
      route: '开始路由',
      stack: '切换到聚合模式',
      direct: '回到直连',
    },
    status: {
      directLead: '直连生效中 · 直接连接',
      routeLead: '路由生效中',
      stackLead: '聚合生效中 · 默认',
      stackValue: '{name} · 另有 {count} 家共 {models} 个模型',
    },
    chip: {
      official: '官方',
      default: '默认',
      models: '{count} 个模型',
      direct: '直连',
      directWhenDirect: '直连时使用',
      needsRoute: '需要路由',
      siteRecommended: '本站推荐',
      siteOneClickImport: '一键导入',
      siteImported: '已导入',
    },
    cardStatus: {
      inUse: '使用中',
      routing: '路由中',
      currentDefault: '当前默认',
      defaultAfterSwitch: '切换后默认',
    },
    action: {
      switch: '切换',
      routeHere: '路由到此',
      setDefault: '设为默认',
      add: '添加',
      remove: '移除',
      enable: '启用',
      exitAndUse: '回到直连并使用',
      openTerminal: '使用此供应商打开终端',
    },
    section: {
      stackDefault: '默认供应商',
      stackDefaultAfterSwitch: '切换到聚合模式后的默认供应商',
      added: '已添加 · {count}',
      available: '可添加 · {count}',
      sharedListNote: 'Web 和 Desktop 共用这份列表',
      sharedListHelp:
        '已添加的供应商会一起写进 DeepSeek Harness 的 Web 和 Desktop 配置（以及已初始化的 headless、acp），在 DeepSeek Harness 里选模型即可。',
    },
    reason: {
      noRoute: '官方订阅不经过路由，回到直连后可用。',
      officialStack: '官方账号只能做默认，不能加入聚合。',
      noStack: '官方订阅不能用于聚合模式。',
      needsRoute: '需要路由：{reason}，不能直连。',
      openaiChat: '使用 OpenAI Chat 接口格式',
      desktopMapping: '使用模型映射（Claude Desktop 只认 Claude 模型名）',
      workbuddyOnlyOne: 'WorkBuddy 同时只能登录一个账号，添加另一张官方卡即可切换。',
    },
    quota: {
      tierLeft: '{label}剩余 {value}%',
      tierShort: '{label} {value}%',
      balance: '余额 {value}',
      tiers: {
        fiveHour: '5 小时',
        weekly: '每周',
        fable: 'Fable',
      },
      shortTiers: {
        fiveHour: '5 小时',
        weekly: '每周',
        fable: 'Fable',
      },
    },
    usage: {
      title: '用量统计',
      all: '全部',
      help: '查看 AI 模型的使用情况和成本统计',
      justSynced: '刚刚同步',
      syncing: '同步中…',
      syncTip: '点击立即同步',
      settings: '用量设置',
      totalCost: '总成本',
      totalRequests: '总请求数',
      realTokens: '真实消耗 Tokens',
      cacheHitRate: '缓存命中率',
      timeRange: '时间范围',
      presets: {
        today: '当天',
        '1d': '近 24 小时',
        '7d': '近 7 天',
        '14d': '近 14 天',
        '30d': '近 30 天',
        all: '全部',
      },
      customRange: '日历筛选',
      customRangeHint: '支持日期与时间',
      startTime: '开始时间',
      endTime: '结束时间',
      liveEndTime: '结束时间跟随当前时刻',
      liveEndNow: '现在',
      prevMonth: '上个月',
      nextMonth: '下个月',
      cancel: '取消',
      confirm: '确定',
      trendTitle: '{metric} · {granularity}',
      hourly: '按小时',
      daily: '按天',
      heatmapTitle: '用量热力图',
      heatmapSubtitle: '最近 53 周的每日用量',
      less: '少',
      more: '多',
      metrics: {
        cost: '成本',
        requests: '请求数',
        tokens: 'Tokens',
        hitRate: '缓存命中率',
      },
      moreMetrics: '更多指标',
      realTokensHelpTitle: '真实消耗 Tokens 怎么算',
      realTokensHelp: '新增输入 + 输出 + 缓存写入 + 缓存命中。新增输入不含缓存；部分协议（如 OpenAI）不上报缓存写入，这一项可能偏低。',
      hitRateHelpTitle: '缓存命中率怎么算',
      hitRateHelp: '缓存命中 ÷（新增输入 + 缓存写入 + 缓存命中）。',
      freshInput: '新增输入',
      output: '输出',
      cacheWrite: '缓存写入',
      cacheRead: '缓存命中',
      providerFilter: '供应商',
      modelFilter: '模型',
      statusCode: '状态码',
      logColumns: '请求日志的列',
      tabs: {
        logs: '请求日志',
        providers: '供应商',
        models: '模型',
      },
      columns: {
        time: '时间',
        app: '应用',
        provider: '供应商',
        model: '模型',
        freshInput: '新增输入',
        output: '输出',
        cacheRead: '缓存命中',
        cost: '成本',
        speed: '速度',
        firstToken: '首字',
        requests: '请求数',
        tokens: 'Tokens',
        successRate: '成功率',
        speedHelpTitle: '速度怎么算',
        speedHelp:
          '每秒输出多少 token，从首字算到结束。只算经路由服务、输出不少于 100 token 的请求；思考过程不外露的模型是近似值。带 ≈ 的是没开路由时按会话日志的时间戳估算的：等首字的时间也算在内，所以偏低；只估输出不少于 200 token 的请求。',
      },
      weekdays: ['周一', '', '周三', '', '周五', '', '周日'],
      months: ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'],
    },
    editor: {
      titleAdd: '添加供应商（{app}）',
      titleEdit: '编辑供应商 · {name}',
      nameLabel: '供应商名称',
      endpointLabel: 'API 请求地址（Endpoint）',
      apiKeyLabel: 'API 密钥（API Key）',
      modelLabel: '默认模型',
      keyReadyHint: '已关联本站密钥，保存后即可直接点「▷ 切换」启用',
      keyPendingHint: '也可直接从下方「导入到 CCS」一键自动写入端点、密钥和余额查询脚本',
      save: '保存供应商',
      cancel: '取消',
      savedToast: '已保存「{name}」配置',
    },
    guided: {
      step0Badge: '第 1 步 · 安装应用',
      step0Text: '点击高亮的「安装」，再把右侧「显示」开关打开，{app} 就会出现在左侧栏。',
      step0ActionInstall: '点这里「安装」',
      step0ActionToggle: '打开「显示」开关',
      step1Badge: '第 2 步 · 准备密钥',
      step1Text: '先在本站创建一把 API 密钥（推荐开启智能路由），再来把 {site} 写入 CC Switch。',
      step1Action: '去创建 API 密钥',
      step2Badge: '第 3 步 · 导入到 CCS',
      step2Text: '已检测到密钥 {key}，点击右侧「导入到 CCS」或「+ 添加供应商」自动写入 {site}。',
      step2Action: '一键导入到 CCS',
      step3Badge: '第 4 步 · 切换启用',
      step3Text: '点击 {site} 卡片右侧的「▷ 切换」按钮，即可把 {app} 切到本站网关。',
      step3Action: '点这里切换启用',
      step3DoneText: '已切换到 {site}！新开一个终端运行启动命令即可开始使用，也可以点左侧「用量统计」查看演示。',
      step3DoneToast: '已将 {app} 切换到 {site}，新开终端即可生效',
      dismiss: '收起引导',
      reopen: '显示新手引导',
      spotlightInstall: '点这里安装',
      spotlightToggle: '点这里显示在侧栏',
      spotlightCreateKey: '先创建密钥',
      spotlightImport: '点这里导入',
      spotlightActivate: '点这里切换到 {site}',
    },
  },
}
