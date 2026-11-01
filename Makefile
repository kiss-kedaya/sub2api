.PHONY: build build-backend build-frontend build-ssr verify-embed verify-ssr release-ssr test test-backend test-frontend test-frontend-critical

FRONTEND_CRITICAL_VITEST := \
	src/data/__tests__/apiDocs.spec.ts \
	src/utils/__tests__/docsUrl.spec.ts \
	src/views/public/__tests__/DocsView.spec.ts \
	src/components/layout/__tests__/docUrlSanitization.spec.ts \
	src/components/admin/usage/__tests__/UsageTable.spec.ts \
	src/views/admin/__tests__/GroupsView.returnRequestedModel.spec.ts \
	src/components/crypto/__tests__/CryptoPrivateKeyDialog.spec.ts \
	src/components/admin/account/__tests__/ScheduledTestsPanel.spec.ts \
	src/components/admin/account/__tests__/ScheduledQualityConfig.spec.ts \
	src/components/user/monitor/__tests__/ChannelMonitorV3QualityHistory.spec.ts \
	src/api/admin/__tests__/customUsage.spec.ts \
	src/components/account/__tests__/CustomUsageCell.spec.ts \
	src/components/account/__tests__/CustomUsageConfigModal.spec.ts \
	src/components/admin/account/__tests__/AccountActionMenu.customUsage.spec.ts \
	src/composables/__tests__/useCustomUsage.spec.ts \
	src/utils/__tests__/customUsage.spec.ts \
	src/utils/__tests__/accountModelTest.spec.ts \
	src/i18n/__tests__/localeKeyCompleteness.spec.ts \
	src/__tests__/xlsxWriteOnly.spec.ts \
	src/__tests__/xlsxExportRoundTrip.spec.ts \
	src/api/__tests__/client.spec.ts \
	src/api/__tests__/tokenRefresh.spec.ts \
	src/api/__tests__/channelMonitorV2.spec.ts \
	src/api/__tests__/usage.snapshot.spec.ts \
	src/api/__tests__/redeem.spec.ts \
	src/api/__tests__/keys.deepseek-tools.spec.ts \
	src/views/auth/__tests__/LinuxDoCallbackView.spec.ts \
	src/views/auth/__tests__/WechatCallbackView.spec.ts \
	src/views/user/__tests__/PaymentView.spec.ts \
	src/views/user/__tests__/PaymentResultView.spec.ts \
	src/views/user/__tests__/ChannelStatusView.mode.spec.ts \
	src/views/user/__tests__/KeysView.spec.ts \
	src/views/user/__tests__/UsageView.spec.ts \
	src/components/layout/__tests__/ConsoleSignal.spec.ts \
	src/components/layout/__tests__/ConsoleAtmosphere.spec.ts \
	src/components/modelPlaza/__tests__/PlazaFilterBar.spec.ts \
	src/components/layout/__tests__/ConsoleNavigationSearch.spec.ts \
	src/components/layout/__tests__/AppSidebar.spec.ts \
	src/components/common/__tests__/ConsoleTabs.spec.ts \
	src/components/common/__tests__/DataTable.spec.ts \
	src/components/payment/__tests__/AmountInput.spec.ts \
	src/components/admin/account/__tests__/AccountTableFilters.spec.ts \
	src/components/admin/account/__tests__/AccountBulkActionsBar.spec.ts \
	src/components/user/dashboard/__tests__/signal-dashboard.spec.ts \
	src/components/charts/__tests__/TokenUsageTrend.spec.ts \
	src/components/charts/__tests__/ModelDistributionChart.spec.ts \
	src/components/charts/__tests__/GroupDistributionChart.spec.ts \
	src/components/user/profile/__tests__/ProfileInfoCard.spec.ts \
	src/components/user/profile/__tests__/ProfileBalanceNotifyCard.spec.ts \
	src/views/user/__tests__/RedeemView.spec.ts \
	src/views/user/__tests__/UserOrdersView.filters.spec.ts \
	src/views/user/__tests__/InfiniteCanvasView.spec.ts \
	src/utils/__tests__/infiniteCanvasSession.spec.ts \
	src/components/common/__tests__/BaseDialog.ids.spec.ts \
	src/components/common/__tests__/Pagination.jump.spec.ts \
	src/components/common/__tests__/ProxySelector.testing.spec.ts \
	src/components/admin/channel/__tests__/ModelTagInput.keyboard.spec.ts \
	src/components/admin/payment/__tests__/AdminRefundDialog.balance.spec.ts \
	src/components/admin/user/__tests__/UserPlatformQuotaModal.spec.ts \
	src/components/user/profile/__tests__/Totp.errors.spec.ts \
	src/components/user/profile/__tests__/TotpDisableDialog.required.spec.ts \
	src/components/user/profile/__tests__/ProfileTotpCard.spec.ts \
	src/components/user/profile/__tests__/TotpDisableVerification.spec.ts \
	src/components/user/profile/__tests__/totp-timer-cleanup.spec.ts \
	src/api/__tests__/adminCrypto.security.spec.ts \
	src/views/admin/__tests__/CryptoView.security.spec.ts \
	src/components/crypto/__tests__/CryptoSecurityDialog.spec.ts \
	src/components/crypto/__tests__/CryptoMnemonicsDialog.spec.ts \
	src/components/user/profile/__tests__/TotpSetupModal.inputs.spec.ts \
	src/components/common/__tests__/TotpCodeInput.spec.ts \
	src/components/auth/__tests__/TotpLoginModal.spec.ts \
	src/components/auth/__tests__/TotpStepUpDialog.spec.ts \
	src/components/user/profile/__tests__/ProfileBarkCard.spec.ts \
	src/components/crypto/__tests__/CryptoWalletSecurityPanel.spec.ts \
	src/components/crypto/__tests__/CryptoGasWalletPanel.spec.ts \
	src/views/user/__tests__/DepositView.spec.ts \
	src/utils/__tests__/cryptoExplorer.spec.ts \
	src/views/admin/settings/__tests__/GatewayIPNoticeSettings.spec.ts \
	src/composables/__tests__/useClipboard.spec.ts \
	src/composables/__tests__/cryptoGrant.spec.ts \
	src/stores/__tests__/announcements.markAll.spec.ts \
	src/stores/__tests__/payment.config.spec.ts \
	src/stores/__tests__/subscriptions.clear.spec.ts \
	src/views/auth/__tests__/RegisterView.spec.ts \
	src/components/account/__tests__/AccountUsageCell.spec.ts \
	src/views/admin/__tests__/SettingsView.spec.ts \
	src/views/admin/__tests__/UsageView.spec.ts \
	src/features/channel-monitor-v2/__tests__/designSystem.structure.spec.ts \
	src/features/channel-monitor-v2/__tests__/monitorFormat.spec.ts \
	src/features/channel-monitor-v2/__tests__/monitorZoom.spec.ts

# 一键编译前后端。前端必须先生成，后端 release build 才能把 dist
# 嵌入二进制；否则服务会正常启动但根路径静默返回 404。
build: build-frontend build-backend

# 编译后端（复用 backend/Makefile）
build-backend:
	@$(MAKE) -C backend build

# 编译前端（需要已安装依赖）。构建末尾会写 backend/internal/web/dist/frontend-build.json，
# 记录版本/commit/文件哈希；后端嵌入的就是这个清单描述的产物。
build-frontend:
	@pnpm --dir frontend run build

# 校验后端将要嵌入的前端构建是否来自当前 checkout（CI 发布前同一步骤）。
verify-embed:
	@pnpm --dir frontend run verify:embed

# Nuxt/Nitro SSR 产物。Nuxt 4.6.3 要求 Node ^22.22.3 或 ^24.15（CI 用 24.22.x）。
build-ssr:
	@pnpm --dir frontend run build:ssr

verify-ssr:
	@pnpm --dir frontend run verify:ssr

# 把 Go 二进制（-tags=embed，内含 SPA 回滚页）与 SSR 产物合成一个发布目录：
# release.json 绑定同一 commit，dist/ssr/ 可直接打包上传。
release-ssr: build-frontend build-ssr build-backend verify-embed verify-ssr
	@node deploy/nuxt/release.mjs seal --ssr frontend/.output --binary backend/bin/server --spa-dist backend/internal/web/dist --out release.json
	@node deploy/nuxt/release.mjs verify --release release.json --ssr frontend/.output --binary backend/bin/server --spa-dist backend/internal/web/dist
	@node deploy/nuxt/release.mjs stage --release release.json --ssr frontend/.output --binary backend/bin/server --out dist/ssr

# 运行测试（后端 + 前端）
# Public repository guard. Run before every commit; CI runs the same script.
sensitive:
	@python tools/check_sensitive.py --all

test: test-backend test-frontend

test-backend:
	@$(MAKE) -C backend test

test-frontend:
	@pnpm --dir frontend run lint:check
	@pnpm --dir frontend run typecheck
	@$(MAKE) test-frontend-critical
	@pnpm --dir frontend exec vue-tsc -p dev/tsconfig.json --noEmit
	@pnpm --dir frontend exec vitest run --config dev/vitest.config.ts

test-frontend-critical:
	@pnpm --dir frontend exec vitest run $(FRONTEND_CRITICAL_VITEST)
