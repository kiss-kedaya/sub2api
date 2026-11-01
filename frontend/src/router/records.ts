import type { RouteRecordRaw } from 'vue-router'

// Sys areas: sub-pages are sibling routes sharing one view (meta.section picks the sub-page).
const mihomoView = () => import('@/views/admin/MihomoView.vue')
const requestCaptureView = () => import('@/views/admin/RequestCaptureView.vue')
const cryptoView = () => import('@/views/admin/CryptoView.vue')
const riskControlView = () => import('@/views/admin/RiskControlView.vue')
// Old one-page URLs with ?tab=<id> land on the matching sub-page.
const tabRedirect = (targets: Record<string, string>) => (to: { query: Record<string, unknown> }) => {
  const name = targets[String(to.query.tab ?? '')]
  if (!name) return true
  const { tab: _tab, ...query } = to.query
  return { name, query: query as Record<string, string> }
}

export const routes: RouteRecordRaw[] = [
  {path:'/admin/ops-center',name:'AdminOpsCenter',component:()=>import('@/views/admin/OpsCenterView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'运营中心'}},
  {path:'/admin/model-comparison',name:'AdminModelComparison',component:()=>import('@/views/admin/ModelComparisonView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'模型比价'}},
  {path:'/admin/security-penalties',name:'AdminSecurityPenalties',component:()=>import('@/views/admin/SecurityPenaltiesView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'分层安全限制'}},
  {path:'/security',name:'SecurityRecords',component:()=>import('@/views/user/SecurityRecordsView.vue'),meta:{requiresAuth:true,title:'安全记录'}},
  {path:'/admin/serverless',name:'AdminServerless',component:()=>import('@/views/admin/ServerlessView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'地区路由'}},
  {path:'/admin/excel-grants',name:'AdminExcelGrants',component:()=>import('@/views/admin/ExcelGrantsView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'Excel 协议授权'}},
  {path:'/admin/ops-automation',name:'AdminOpsAutomation',component:()=>import('@/views/admin/OpsAutomationView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'账号自动化'}},
  {path:'/admin/credential-guard',name:'AdminCredentialGuard',component:()=>import('@/views/admin/CredentialGuardView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'凭证巡检'}},
  // Account notifications: overview plus sub-pages. The four routes share one view, so switching keeps the loaded data.
  // Old links with ?tab=records / ?tab=settings land on the matching sub-page.
  {path:'/admin/account-ops',name:'AdminAccountOps',component:()=>import('@/views/admin/AccountOpsView.vue'),props:{section:'overview'},meta:{requiresAuth:true,requiresAdmin:true,title:'账号通知'},beforeEnter:(to)=>to.query.tab==='records'?{path:'/admin/account-ops/records',replace:true}:to.query.tab==='settings'?{path:'/admin/account-ops/rules',replace:true}:true},
  {path:'/admin/account-ops/channels',name:'AdminAccountOpsChannels',component:()=>import('@/views/admin/AccountOpsView.vue'),props:{section:'channels'},meta:{requiresAuth:true,requiresAdmin:true,title:'账号通知'}},
  {path:'/admin/account-ops/rules',name:'AdminAccountOpsRules',component:()=>import('@/views/admin/AccountOpsView.vue'),props:{section:'rules'},meta:{requiresAuth:true,requiresAdmin:true,title:'账号通知'}},
  {path:'/admin/account-ops/records',name:'AdminAccountOpsRecords',component:()=>import('@/views/admin/AccountOpsView.vue'),props:{section:'records'},meta:{requiresAuth:true,requiresAdmin:true,title:'账号通知'}},
  {path:'/admin/channel-operations',name:'AdminChannelOperations',component:()=>import('@/views/admin/ChannelOperationsView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'渠道运维'}},
  {path:'/admin/mihomo',name:'AdminMihomo',component:mihomoView,beforeEnter:tabRedirect({nodes:'AdminMihomoNodes',dynamic:'AdminMihomoDynamic',kernel:'AdminMihomoKernel'}),meta:{requiresAuth:true,requiresAdmin:true,title:'代理出口',section:'subscriptions'}},
  {path:'/admin/mihomo/nodes',name:'AdminMihomoNodes',component:mihomoView,meta:{requiresAuth:true,requiresAdmin:true,title:'代理出口',section:'nodes'}},
  {path:'/admin/mihomo/dynamic',name:'AdminMihomoDynamic',component:mihomoView,meta:{requiresAuth:true,requiresAdmin:true,title:'代理出口',section:'dynamic'}},
  {path:'/admin/mihomo/kernel',name:'AdminMihomoKernel',component:mihomoView,meta:{requiresAuth:true,requiresAdmin:true,title:'代理出口',section:'kernel'}},
  {path:'/admin/group-cache-tests',name:'AdminGroupCacheTests',component:()=>import('@/views/admin/GroupCacheTestView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'分组缓存测试'}},
  {path:'/admin/request-captures',name:'AdminRequestCaptures',component:requestCaptureView,beforeEnter:tabRedirect({settings:'AdminRequestCaptureSettings'}),meta:{requiresAuth:true,requiresAdmin:true,title:'请求采集',section:'tasks'}},
  {path:'/admin/request-captures/settings',name:'AdminRequestCaptureSettings',component:requestCaptureView,meta:{requiresAuth:true,requiresAdmin:true,title:'请求采集',section:'settings'}},
  {path:'/admin/connections',name:'AdminConnections',component:()=>import('@/views/admin/ConnectionDiagnosticsView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'连接诊断'}},
  {path:'/admin/traffic',name:'AdminTraffic',component:()=>import('@/views/TrafficView.vue'),props:{admin:true},meta:{requiresAuth:true,requiresAdmin:true,title:'用户流量'}},
  {path:'/traffic',name:'UserTraffic',component:()=>import('@/views/TrafficView.vue'),meta:{requiresAuth:true,requiresAdmin:false,title:'流量记录'}},
  {path:'/admin/ip-risk',name:'AdminIPRisk',component:()=>import('@/views/admin/IPRiskView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'IP风控'}},
  {path:'/admin/ip-audit',name:'AdminIPAudit',component:()=>import('@/features/ip-audit/IPAuditView.vue'),meta:{requiresAuth:true,requiresAdmin:true,title:'IP审计'}},

  // ==================== Setup Routes ====================
  {
    path: '/setup',
    name: 'Setup',
    component: () => import('@/views/setup/SetupWizardView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Setup'
    }
  },

  // ==================== Public Routes ====================
  {
    path: '/docs',
    name: 'Docs',
    component: () => import('@/views/public/DocsView.vue'),
    meta: { requiresAuth: false, title: 'API documentation', titleKey: 'docs.title' }
  },
  {
    path: '/home',
    name: 'Home',
    component: () => import('@/views/HomeView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Home'
    }
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/auth/LoginView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Login',
      titleKey: 'home.login'
    }
  },
  {
    path: '/register',
    name: 'Register',
    component: () => import('@/views/auth/RegisterView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Register',
      titleKey: 'auth.createAccount'
    }
  },
  {
    path: '/email-verify',
    name: 'EmailVerify',
    component: () => import('@/views/auth/EmailVerifyView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Verify Email'
    }
  },
  {
    path: '/auth/callback',
    name: 'OAuthCallback',
    alias: '/auth/oauth/callback',
    component: () => import('@/views/auth/OAuthCallbackView.vue'),
    meta: {
      requiresAuth: false,
      title: 'OAuth Callback',
      titleKey: 'auth.oauthCallbackPageTitle'
    }
  },
  {
    path: '/auth/linuxdo/callback',
    name: 'LinuxDoOAuthCallback',
    component: () => import('@/views/auth/LinuxDoCallbackView.vue'),
    meta: {
      requiresAuth: false,
      title: 'LinuxDo OAuth Callback',
      titleKey: 'auth.linuxdoCallbackPageTitle'
    }
  },
  {
    path: '/auth/wechat/callback',
    name: 'WeChatOAuthCallback',
    component: () => import('@/views/auth/WechatCallbackView.vue'),
    meta: {
      requiresAuth: false,
      title: 'WeChat OAuth Callback',
      titleKey: 'auth.wechatCallbackPageTitle'
    }
  },
  {
    path: '/auth/wechat/payment/callback',
    name: 'WeChatPaymentOAuthCallback',
    component: () => import('@/views/auth/WechatPaymentCallbackView.vue'),
    meta: {
      requiresAuth: false,
      title: 'WeChat Payment Callback',
      titleKey: 'auth.wechatPaymentCallbackPageTitle'
    }
  },
  {
    path: '/auth/dingtalk/callback',
    name: 'DingTalkOAuthCallback',
    component: () => import('@/views/auth/DingTalkCallbackView.vue'),
    meta: {
      requiresAuth: false,
      title: 'DingTalk OAuth Callback',
      titleKey: 'auth.dingtalkCallbackPageTitle'
    }
  },
  {
    path: '/auth/dingtalk/email-completion',
    name: 'dingtalk-email-completion',
    component: () => import('@/views/auth/DingTalkEmailCompletionView.vue'),
    meta: {
      requiresAuth: false,
      title: 'DingTalk Email Completion'
    }
  },
  {
    path: '/auth/oidc/callback',
    name: 'OIDCOAuthCallback',
    component: () => import('@/views/auth/OidcCallbackView.vue'),
    meta: {
      requiresAuth: false,
      title: 'OIDC OAuth Callback',
      titleKey: 'auth.oidcCallbackPageTitle'
    }
  },
  {
    path: '/forgot-password',
    name: 'ForgotPassword',
    component: () => import('@/views/auth/ForgotPasswordView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Forgot Password',
      titleKey: 'auth.forgotPasswordTitle'
    }
  },
  {
    path: '/reset-password',
    name: 'ResetPassword',
    component: () => import('@/views/auth/ResetPasswordView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Reset Password'
    }
  },
  {
    path: '/key-usage',
    name: 'KeyUsage',
    component: () => import('@/views/KeyUsageView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Key Usage',
    }
  },
  {
    path: '/legal',
    redirect: '/legal/privacy'
  },
  {
    path: '/legal/:documentId',
    name: 'LegalDocument',
    component: () => import('@/views/public/LegalDocumentView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Legal Document'
    }
  },
  {
    path: '/model-plaza',
    name: 'ModelPlaza',
    component: () => import('@/views/ModelPlazaView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Model Plaza',
      titleKey: 'modelPlaza.title'
    }
  },

  // ==================== User Routes ====================
  {
    path: '/',
    redirect: '/home'
  },
  {
    path: '/dashboard',
    name: 'Dashboard',
    component: () => import('@/views/user/DashboardView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Dashboard',
      titleKey: 'dashboard.title',
      descriptionKey: 'dashboard.welcomeMessage'
    }
  },
  {
    path: '/keys',
    name: 'Keys',
    component: () => import('@/views/user/KeysView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'API Keys',
      titleKey: 'keys.title',
      descriptionKey: 'keys.description'
    }
  },
  {
    path: '/batch-image',
    name: 'BatchImageGuide',
    alias: '/docs/batch-image',
    component: () => import('@/views/user/BatchImageGuideView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Batch Image Guide',
      titleKey: 'batchImageGuide.title',
      descriptionKey: 'batchImageGuide.description'
    }
  },
  {
    path: '/infinite-canvas',
    name: 'InfiniteCanvas',
    component: () => import('@/views/user/InfiniteCanvasView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Infinite Canvas',
      titleKey: 'infiniteCanvas.title',
      descriptionKey: 'infiniteCanvas.description'
    }
  },
  {
    path: '/usage',
    name: 'Usage',
    component: () => import('@/views/user/UsageView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Usage Records',
      titleKey: 'usage.title',
      descriptionKey: 'usage.description'
    }
  },
  {
    path: '/redeem',
    name: 'Redeem',
    component: () => import('@/views/user/RedeemView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Redeem Code',
      titleKey: 'redeem.title',
      descriptionKey: 'redeem.description'
    }
  },
  {
    path: '/affiliate',
    name: 'Affiliate',
    component: () => import('@/views/user/AffiliateView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Affiliate',
      titleKey: 'affiliate.title',
      descriptionKey: 'affiliate.description'
    }
  },
  {
    path: '/available-channels',
    name: 'UserAvailableChannels',
    component: () => import('@/views/user/AvailableChannelsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Available Channels',
      titleKey: 'availableChannels.title',
      descriptionKey: 'availableChannels.description'
    }
  },
  {
    path: '/tickets/:id(\\d+)?',
    name: 'UserTickets',
    component: () => import('@/views/TicketsView.vue'),
    props: { admin: false },
    meta: { requiresAuth: true, requiresAdmin: false, title: 'My tickets', titleKey: 'tickets.title' }
  },
  {
    path: '/admin/tickets/:id(\\d+)?',
    name: 'AdminTickets',
    component: () => import('@/views/TicketsView.vue'),
    props: { admin: true },
    meta: { requiresAuth: true, requiresAdmin: true, title: 'Support inbox', titleKey: 'tickets.adminTitle' }
  },
  {
    path: '/profile',
    name: 'Profile',
    component: () => import('@/views/user/ProfileView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Profile',
      titleKey: 'profile.title',
      descriptionKey: 'profile.description'
    }
  },
  {
    path: '/subscriptions',
    name: 'Subscriptions',
    component: () => import('@/views/user/SubscriptionsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'My Subscriptions',
      titleKey: 'userSubscriptions.title',
      descriptionKey: 'userSubscriptions.description'
    }
  },
  {
    path: '/purchase',
    name: 'RechargeCenter',
    component: () => import('@/views/user/PaymentView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Recharge Center',
      titleKey: 'nav.buySubscription',
      descriptionKey: 'purchase.description',
      requiresPayment: true
    }
  },
  {
    path: '/deposit',
    name: 'CryptoDeposit',
    component: () => import('@/views/user/DepositView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Crypto Deposit',
      titleKey: 'nav.cryptoDeposit',
      descriptionKey: 'deposit.description',
      requiresCrypto: true
    }
  },
  {
    path: '/orders',
    name: 'OrderList',
    component: () => import('@/views/user/UserOrdersView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'My Orders',
      titleKey: 'nav.myOrders',
      requiresPayment: true
    }
  },
  {
    path: '/payment/qrcode',
    name: 'PaymentQRCode',
    component: () => import('@/views/user/PaymentQRCodeView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Payment',
      titleKey: 'payment.qr.scanToPay',
      requiresPayment: true
    }
  },
  {
    path: '/payment/result',
    name: 'PaymentResult',
    component: () => import('@/views/user/PaymentResultView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Payment Result',
      titleKey: 'payment.result.success',
      requiresPayment: false
    }
  },
  {
    path: '/payment/stripe',
    name: 'StripePayment',
    component: () => import('@/views/user/StripePaymentView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Stripe Payment',
      titleKey: 'payment.stripePay',
      requiresPayment: false
    }
  },
  {
    path: '/payment/airwallex',
    name: 'AirwallexPayment',
    component: () => import('@/views/user/AirwallexPaymentView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Airwallex Payment',
      titleKey: 'payment.airwallexPay',
      requiresPayment: false
    }
  },
  {
    path: '/payment/stripe-popup',
    name: 'StripePopup',
    component: () => import('@/views/user/StripePopupView.vue'),
    meta: {
      requiresAuth: false,
      requiresAdmin: false,
      title: 'Payment',
      requiresPayment: false
    }
  },
  {
    path: '/custom/:id',
    name: 'CustomPage',
    component: () => import('@/views/user/CustomPageView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Custom Page',
      titleKey: 'customPage.title',
    }
  },

  // ==================== Admin Routes ====================
  {
    path: '/admin',
    redirect: '/admin/dashboard'
  },
  {
    path: '/admin/dashboard',
    name: 'AdminDashboard',
    component: () => import('@/views/admin/DashboardView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Admin Dashboard',
      titleKey: 'admin.dashboard.title',
      descriptionKey: 'admin.dashboard.description'
    }
  },
  {
    path: '/admin/ops',
    name: 'AdminOps',
    component: () => import('@/views/admin/ops/OpsDashboard.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Ops Monitoring',
      titleKey: 'admin.ops.title',
      descriptionKey: 'admin.ops.description'
    }
  },
  {
    path: '/admin/audit-logs',
    name: 'AdminAuditLogs',
    component: () => import('@/views/admin/AuditLogView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Audit Logs',
      titleKey: 'admin.audit.title',
      descriptionKey: 'admin.audit.description'
    }
  },
  {
    path: '/admin/users',
    name: 'AdminUsers',
    component: () => import('@/views/admin/UsersView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'User Management',
      titleKey: 'admin.users.title',
      descriptionKey: 'admin.users.description'
    }
  },
  {
    path: '/admin/groups',
    name: 'AdminGroups',
    component: () => import('@/views/admin/GroupsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Group Management',
      titleKey: 'admin.groups.title',
      descriptionKey: 'admin.groups.description'
    }
  },
  {
    path: '/admin/channels',
    redirect: '/admin/channels/pricing'
  },
  {
    path: '/admin/channels/pricing',
    name: 'AdminChannels',
    component: () => import('@/views/admin/ChannelsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Channel Management',
      titleKey: 'admin.channels.title',
      descriptionKey: 'admin.channels.description'
    }
  },
  {
    path: '/admin/channels/monitor',
    name: 'AdminChannelMonitor',
    component: () => import('@/views/admin/ChannelMonitorView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Channel Monitor',
      titleKey: 'admin.channelMonitor.title',
      descriptionKey: 'admin.channelMonitor.description'
    }
  },
  {
    path: '/monitor',
    name: 'ChannelStatus',
    component: () => import('@/views/user/ChannelStatusView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Channel Status',
      titleKey: 'nav.channelStatus'
    }
  },
  {
    path: '/admin/subscriptions',
    name: 'AdminSubscriptions',
    component: () => import('@/views/admin/SubscriptionsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Subscription Management',
      titleKey: 'admin.subscriptions.title',
      descriptionKey: 'admin.subscriptions.description'
    }
  },
  {
    path: '/admin/accounts',
    name: 'AdminAccounts',
    component: () => import('@/views/admin/AccountsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Account Management',
      titleKey: 'admin.accounts.title',
      descriptionKey: 'admin.accounts.description'
    }
  },
  {
    path: '/admin/plugins',
    name: 'AdminPlugins',
    component: () => import('@/views/admin/PluginsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Plugin Management',
      titleKey: 'admin.plugins.title',
      descriptionKey: 'admin.plugins.description'
    }
  },
  {
    path: '/admin/announcements',
    name: 'AdminAnnouncements',
    component: () => import('@/views/admin/AnnouncementsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Announcements',
      titleKey: 'admin.announcements.title',
      descriptionKey: 'admin.announcements.description'
    }
  },
  {
    path: '/admin/proxies',
    name: 'AdminProxies',
    component: () => import('@/views/admin/ProxiesView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Proxy Management',
      titleKey: 'admin.proxies.title',
      descriptionKey: 'admin.proxies.description'
    }
  },
  {
    path: '/admin/redeem',
    name: 'AdminRedeem',
    component: () => import('@/views/admin/RedeemView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Redeem Code Management',
      titleKey: 'admin.redeem.title',
      descriptionKey: 'admin.redeem.description'
    }
  },
  {
    path: '/admin/promo-codes',
    name: 'AdminPromoCodes',
    component: () => import('@/views/admin/PromoCodesView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Promo Code Management',
      titleKey: 'admin.promo.title',
      descriptionKey: 'admin.promo.description'
    }
  },
  {
    path: '/admin/crypto',
    name: 'AdminCrypto',
    component: cryptoView,
    beforeEnter: tabRedirect({ addresses: 'AdminCryptoAddresses', sweep: 'AdminCryptoSweeps', sweeps: 'AdminCryptoSweeps', deposits: 'AdminCryptoDeposits', config: 'AdminCryptoSettings', settings: 'AdminCryptoSettings' }),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Crypto Deposits',
      titleKey: 'admin.crypto.title',
      descriptionKey: 'admin.crypto.description',
      section: 'overview'
    }
  },
  {
    path: '/admin/crypto/addresses',
    name: 'AdminCryptoAddresses',
    component: cryptoView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Crypto Deposits',
      titleKey: 'admin.crypto.title',
      descriptionKey: 'admin.crypto.description',
      section: 'addresses'
    }
  },
  {
    path: '/admin/crypto/sweeps',
    name: 'AdminCryptoSweeps',
    component: cryptoView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Crypto Deposits',
      titleKey: 'admin.crypto.title',
      descriptionKey: 'admin.crypto.description',
      section: 'sweep'
    }
  },
  {
    path: '/admin/crypto/deposits',
    name: 'AdminCryptoDeposits',
    component: cryptoView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Crypto Deposits',
      titleKey: 'admin.crypto.title',
      descriptionKey: 'admin.crypto.description',
      section: 'deposits'
    }
  },
  {
    path: '/admin/crypto/settings',
    name: 'AdminCryptoSettings',
    component: cryptoView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Crypto Deposits',
      titleKey: 'admin.crypto.title',
      descriptionKey: 'admin.crypto.description',
      section: 'config'
    }
  },
  {
    path: '/admin/settings',
    name: 'AdminSettings',
    component: () => import('@/views/admin/SettingsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'System Settings',
      titleKey: 'admin.settings.title',
      descriptionKey: 'admin.settings.description'
    }
  },
  {
    path: '/admin/risk-control',
    name: 'AdminRiskControl',
    component: riskControlView,
    beforeEnter: tabRedirect({ records: 'AdminRiskControlRecords', logs: 'AdminRiskControlRecords', keys: 'AdminRiskControlKeys', settings: 'AdminRiskControlSettings' }),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Risk Control',
      titleKey: 'admin.riskControl.title',
      descriptionKey: 'admin.riskControl.description',
      requiresRiskControl: true,
      section: 'overview'
    }
  },
  {
    path: '/admin/risk-control/records',
    name: 'AdminRiskControlRecords',
    component: riskControlView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Risk Control',
      titleKey: 'admin.riskControl.title',
      descriptionKey: 'admin.riskControl.description',
      requiresRiskControl: true,
      section: 'records'
    }
  },
  {
    path: '/admin/risk-control/keys',
    name: 'AdminRiskControlKeys',
    component: riskControlView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Risk Control',
      titleKey: 'admin.riskControl.title',
      descriptionKey: 'admin.riskControl.description',
      requiresRiskControl: true,
      section: 'keys'
    }
  },
  {
    path: '/admin/risk-control/settings',
    name: 'AdminRiskControlSettings',
    component: riskControlView,
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Risk Control',
      titleKey: 'admin.riskControl.title',
      descriptionKey: 'admin.riskControl.description',
      requiresRiskControl: true,
      section: 'settings'
    }
  },
  {
    path: '/admin/prompt-audit',
    name: 'AdminPromptAudit',
    component: () => import('@/features/prompt-audit/PromptAuditView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Prompt Audit',
      titleKey: 'admin.promptAudit.title',
      descriptionKey: 'admin.promptAudit.description',
      requiresRiskControl: true
    }
  },
  {
    path: '/admin/usage',
    name: 'AdminUsage',
    component: () => import('@/views/admin/UsageView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Usage Records',
      titleKey: 'admin.usage.title',
      descriptionKey: 'admin.usage.description'
    }
  },
  {
    path: '/admin/affiliates',
    redirect: '/admin/affiliates/invites'
  },
  {
    path: '/admin/affiliates/invites',
    name: 'AdminAffiliateInvites',
    component: () => import('@/views/admin/affiliates/AdminAffiliateInvitesView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Affiliate Invite Records',
      titleKey: 'nav.affiliateInviteRecords',
      descriptionKey: 'admin.affiliates.invitesDescription'
    }
  },
  {
    path: '/admin/affiliates/rebates',
    name: 'AdminAffiliateRebates',
    component: () => import('@/views/admin/affiliates/AdminAffiliateRebatesView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Affiliate Rebate Records',
      titleKey: 'nav.affiliateRebateRecords',
      descriptionKey: 'admin.affiliates.rebatesDescription'
    }
  },
  {
    path: '/admin/affiliates/transfers',
    name: 'AdminAffiliateTransfers',
    component: () => import('@/views/admin/affiliates/AdminAffiliateTransfersView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Affiliate Transfer Records',
      titleKey: 'nav.affiliateTransferRecords',
      descriptionKey: 'admin.affiliates.transfersDescription'
    }
  },


  // ==================== Payment Admin Routes ====================
  {
    path: '/admin/orders/dashboard',
    name: 'AdminPaymentDashboard',
    component: () => import('@/views/admin/orders/AdminPaymentDashboardView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Payment Dashboard',
      titleKey: 'nav.paymentDashboard',
      requiresPayment: true
    }
  },
  {
    path: '/admin/orders',
    name: 'AdminOrders',
    component: () => import('@/views/admin/orders/AdminOrdersView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Order Management',
      titleKey: 'nav.orderManagement',
      requiresPayment: true
    }
  },
  {
    path: '/admin/orders/plans',
    name: 'AdminPaymentPlans',
    component: () => import('@/views/admin/orders/AdminPaymentPlansView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Subscription Plans',
      titleKey: 'nav.paymentPlans',
      requiresPayment: true
    }
  },

  // ==================== 404 Not Found ====================
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: () => import('@/views/NotFoundView.vue'),
    meta: {
      title: '404 Not Found'
    }
  }
]
