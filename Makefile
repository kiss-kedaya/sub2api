.PHONY: build build-backend build-frontend test test-backend test-frontend test-frontend-critical

FRONTEND_CRITICAL_VITEST := \
	src/i18n/__tests__/localeKeyCompleteness.spec.ts \
	src/api/__tests__/client.spec.ts \
	src/api/__tests__/tokenRefresh.spec.ts \
	src/api/__tests__/channelMonitorV2.spec.ts \
	src/api/__tests__/usage.snapshot.spec.ts \
	src/api/__tests__/redeem.spec.ts \
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
	src/components/user/profile/__tests__/TotpSetupModal.inputs.spec.ts \
	src/composables/__tests__/useClipboard.spec.ts \
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

# 编译前端（需要已安装依赖）
build-frontend:
	@pnpm --dir frontend run build

# 运行测试（后端 + 前端）
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
