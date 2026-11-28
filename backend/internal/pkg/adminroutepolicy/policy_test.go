//go:build unit

package adminroutepolicy

import "testing"

// 优惠码是「返回即秘密」的资源：任何方法（含 GET）都必须是
// step_up_required。这是「只读所以不用 2FA」口径被废弃后的硬约束。
// 卡密读模型已改为掩码（见 TestRedeemCodeReadsAreMaskedAndSecretFree），
// 但卡密写操作仍是「返回/改变可兑价值」的动作，必须 step-up。
// 代理与上游渠道不在其中：读模型已掩码，只有明文导出/密钥 reveal 与写操作要求 2FA。
func TestSecretReturningResourcesRequireStepUpForEveryMethod(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/promo-codes",
		"/api/v1/admin/promo-codes/1",
	} {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			resolved, ok := Resolve(method, path)
			if !ok {
				t.Fatalf("%s %s 未归类", method, path)
			}
			if resolved.Class != ClassStepUpRequired {
				t.Fatalf("%s %s 必须是 step_up_required，实际 %s", method, path, resolved.Class)
			}
			if resolved.Resource == ResourceNone {
				t.Fatalf("%s %s 必须标注敏感资源", method, path)
			}
		}
	}
	// 卡密写操作继续要求 step-up（生成即产出可兑价值）。
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/admin/redeem-codes/generate"},
		{"POST", "/api/v1/admin/redeem-codes/create-and-redeem"},
		{"POST", "/api/v1/admin/redeem-codes/batch-update"},
		{"DELETE", "/api/v1/admin/redeem-codes/1"},
		{"POST", "/api/v1/admin/redeem-codes/1/expire"},
	} {
		resolved, ok := Resolve(route.method, route.path)
		if !ok {
			t.Fatalf("%s %s 未归类", route.method, route.path)
		}
		if resolved.Class != ClassStepUpRequired || resolved.Resource != ResourceRedeemCodes {
			t.Fatalf("%s %s 必须要求 step-up（卡密可兑价值），实际 %s/%s", route.method, route.path, resolved.Class, resolved.Resource)
		}
	}
}

// 代理的读与写必须分开判定：列表/详情/统计/关联账号的密码已掩码（secret_free），
// 只有明文导出（GET /proxies/data）与写操作要求 step-up。
// 2026-11 事故：GET /admin/proxies/all 被整片要求 step-up，账号管理页加载即弹 TOTP。
func TestProxyReadsAreSecretFreeWhileExportAndWritesAreGated(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/proxies",
		"/api/v1/admin/proxies/all",
		"/api/v1/admin/proxies/1",
		"/api/v1/admin/proxies/1/stats",
		"/api/v1/admin/proxies/1/accounts",
	} {
		resolved, ok := Resolve("GET", path)
		if !ok {
			t.Fatalf("GET %s 未归类", path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason != ReasonProxyReadsRedacted {
			t.Fatalf("GET %s 应为 secret_free/%s，实际 %s/%s", path, ReasonProxyReadsRedacted, resolved.Class, resolved.Reason)
		}
	}
	exported, ok := Resolve("GET", "/api/v1/admin/proxies/data")
	if !ok || exported.Class != ClassStepUpRequired || exported.Reason != ReasonProxyPasswords {
		t.Fatalf("GET /api/v1/admin/proxies/data 必须要求 step-up，实际 %+v ok=%v", exported, ok)
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		resolved, ok := Resolve(method, "/api/v1/admin/proxies/1")
		if !ok || resolved.Class != ClassStepUpRequired {
			t.Fatalf("%s /api/v1/admin/proxies/1 必须要求 step-up，实际 %+v ok=%v", method, resolved, ok)
		}
	}
}

// 上游渠道读模型（channel DTO）不序列化 password/access_token，API Key 列表显式清空
// Key；只有写操作与 reveal POST 才接触明文密钥。页面加载不能因此弹 TOTP。
func TestUpstreamChannelReadsAreSecretFreeWhileWritesAndRevealAreGated(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/upstream-monitor/channels",
		"/api/v1/admin/upstream-monitor/channels/1",
		"/api/v1/admin/upstream-monitor/channels/1/api-keys",
		"/api/v1/admin/upstream-monitor/channels/1/api-key-groups",
		"/api/v1/admin/upstream-monitor/channels/1/balance",
		"/api/v1/admin/upstream-monitor/channels/1/subscriptions/usage",
	} {
		resolved, ok := Resolve("GET", path)
		if !ok {
			t.Fatalf("GET %s 未归类", path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason != ReasonChannelReadsRedacted {
			t.Fatalf("GET %s 应为 secret_free/%s，实际 %s/%s", path, ReasonChannelReadsRedacted, resolved.Class, resolved.Reason)
		}
	}
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/admin/upstream-monitor/channels"},
		{"PUT", "/api/v1/admin/upstream-monitor/channels/1"},
		{"DELETE", "/api/v1/admin/upstream-monitor/channels/1"},
		{"POST", "/api/v1/admin/upstream-monitor/channels/1/api-keys"},
		{"POST", "/api/v1/admin/upstream-monitor/channels/1/api-keys/1/reveal"},
	} {
		resolved, ok := Resolve(route.method, route.path)
		if !ok || resolved.Class != ClassStepUpRequired || resolved.Reason != ReasonChannelKeys {
			t.Fatalf("%s %s 必须要求 step-up（渠道密钥），实际 %+v ok=%v", route.method, route.path, resolved, ok)
		}
	}
}

// 页面加载用的批量只读聚合即使实现成 POST，也不能被 mutating 规则误判成 step-up：
// AccountsView 加载时会自动调用 today-stats/batch 与 usage/batch。
func TestReadOnlyBatchAggregatesAreSecretFree(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/accounts/today-stats/batch",
		"/api/v1/admin/accounts/usage/batch",
	} {
		resolved, ok := Resolve("POST", path)
		if !ok {
			t.Fatalf("POST %s 未归类", path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason != ReasonAccountUsageAggregate {
			t.Fatalf("POST %s 应为 secret_free/%s，实际 %s/%s", path, ReasonAccountUsageAggregate, resolved.Class, resolved.Reason)
		}
		// 同一前缀的写操作仍必须要求 step-up。
		write, ok := Resolve("POST", "/api/v1/admin/accounts/batch")
		if !ok || write.Class != ClassStepUpRequired {
			t.Fatalf("POST /api/v1/admin/accounts/batch 必须保持 step-up，实际 %+v", write)
		}
	}
}

// 凭证可读端点（上游凭证明文回读 / 导出）单独要求 step-up，即使所属资源的
// 列表接口是脱敏的。
func TestCredentialRevealingReadsRequireStepUp(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/accounts/:id/credential",
		"/api/v1/admin/accounts/data",
		"/api/v1/admin/backups/:id/download-url",
		"/api/v1/admin/backups/s3-config",
		"/api/v1/admin/users/:id/api-keys",
		"/api/v1/admin/groups/:id/api-keys",
		"/api/v1/admin/debug-dumps/:name",
	} {
		resolved, ok := Resolve("GET", path)
		if !ok {
			t.Fatalf("GET %s 未归类", path)
		}
		if resolved.Class != ClassStepUpRequired {
			t.Fatalf("GET %s 必须要求 step-up，实际 %s", path, resolved.Class)
		}
	}
}

// 改 Key 分组 = 改这把 Key 的可用分组/路由授权，和凭据回读同级要求 step-up；
// replace-group 会批量迁移用户的 Key 所属分组，同样是权限变更。
// 这两条是「管理员改用户 Key 分组必须二次验证」的后端契约，不允许退化成 secret_free。
func TestUserAPIKeyGroupWritesRequireStepUp(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{"PUT", "/api/v1/admin/api-keys/:id"},
		{"POST", "/api/v1/admin/users/:id/replace-group"},
	} {
		resolved, ok := Resolve(route.method, route.path)
		if !ok {
			t.Fatalf("%s %s 未归类", route.method, route.path)
		}
		if resolved.Class != ClassStepUpRequired {
			t.Fatalf("%s %s 必须要求 step-up，实际 %s/%s", route.method, route.path, resolved.Class, resolved.Reason)
		}
		if IsMachineAllowlisted(route.method, route.path) {
			t.Fatalf("%s %s 不得进入机器白名单", route.method, route.path)
		}
	}
}

// 资源列表若明确脱敏（不返回秘密），归类为 secret_free 并必须给出理由；
// 其写操作仍然要求 step-up。
func TestRedactedResourceReadsAreSecretFreeButWritesAreGated(t *testing.T) {
	reads := []string{
		"/api/v1/admin/accounts",
		"/api/v1/admin/accounts/1",
		"/api/v1/admin/proxies",
		"/api/v1/admin/proxies/all",
		"/api/v1/admin/proxies/1",
		"/api/v1/admin/settings",
		"/api/v1/admin/payment/orders",
		"/api/v1/admin/backups",
		"/api/v1/admin/data-management/config",
		"/api/v1/admin/plugins",
	}
	for _, path := range reads {
		resolved, ok := Resolve("GET", path)
		if !ok {
			t.Fatalf("GET %s 未归类", path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason == "" {
			t.Fatalf("GET %s 应为带理由的 secret_free，实际 %s/%s", path, resolved.Class, resolved.Reason)
		}
	}
	for _, path := range []string{
		"/api/v1/admin/accounts",
		"/api/v1/admin/accounts/1",
		"/api/v1/admin/settings",
		"/api/v1/admin/payment/providers",
		"/api/v1/admin/backups",
		"/api/v1/admin/data-management/backups",
		"/api/v1/admin/plugins/upload",
	} {
		resolved, ok := Resolve("POST", path)
		if !ok {
			t.Fatalf("POST %s 未归类", path)
		}
		if resolved.Class != ClassStepUpRequired {
			t.Fatalf("POST %s 必须要求 step-up，实际 %s", path, resolved.Class)
		}
	}
}

// 白名单只包含无敏感数据的接口；未归类路由在运行时按 step-up fail-closed。
func TestMachineAllowlistAndUnknownRouteSemantics(t *testing.T) {
	if !IsMachineAllowlisted("GET", "/api/v1/admin/dashboard/stats") {
		t.Fatal("只读统计聚合应在机器白名单")
	}
	if IsMachineAllowlisted("GET", "/api/v1/admin/redeem-codes") {
		t.Fatal("卡密列表绝不能进机器白名单")
	}
	if !IsStepUpRequired("GET", "/api/v1/admin/zzz-unknown-route") {
		t.Fatal("未归类路由必须 fail-closed 视为 step-up required")
	}
	if IsStepUpRequired("GET", "/api/v1/admin/dashboard/stats") {
		t.Fatal("白名单路由不应要求 step-up")
	}
}

// 路由分类必须有唯一、稳定的来源；敏感前缀必须优先于 secret_free 家族命中。
func TestRegistryRulesAreConsistent(t *testing.T) {
	for _, rule := range sensitiveResources {
		if rule.res.Class != ClassStepUpRequired || rule.res.Resource == ResourceNone {
			t.Fatalf("敏感资源规则不完整: %s", rule.prefix)
		}
		resolved, ok := Resolve("GET", rule.prefix+"/x")
		if !ok || resolved.Class != ClassStepUpRequired {
			t.Fatalf("敏感前缀必须优先匹配 step_up_required: %s", rule.prefix)
		}
	}
	for _, rule := range sensitiveMutatingResources {
		if rule.res.Class != ClassStepUpRequired || rule.res.Resource == ResourceNone {
			t.Fatalf("敏感写资源规则不完整: %s", rule.prefix)
		}
		resolved, ok := Resolve("POST", rule.prefix)
		if !ok || resolved.Class != ClassStepUpRequired {
			t.Fatalf("敏感写前缀必须匹配 step_up_required: %s", rule.prefix)
		}
	}
	for _, family := range secretFreeFamilies {
		if family.res.Class != ClassSecretFree || family.res.Reason == "" {
			t.Fatalf("secret_free 家族缺少理由: %s", family.prefix)
		}
	}
	for _, entry := range machineAllowlisted {
		if entry.res.Class != ClassMachineAllowlisted || entry.res.Reason == "" {
			t.Fatalf("白名单条目不完整: %s %s", entry.method, entry.path)
		}
		if _, sensitive := SensitiveResourceFor(entry.path); sensitive {
			t.Fatalf("白名单条目指向敏感资源: %s %s", entry.method, entry.path)
		}
	}
}

// 路径前缀按段边界匹配，避免 /backups 吞掉 /backupsomething。
func TestPathPrefixMatchesOnSegmentBoundary(t *testing.T) {
	if _, ok := Resolve("GET", "/api/v1/admin/backupsomething"); ok {
		t.Fatal("非管理面注册路由不应被猜测归类")
	}
	if _, ok := Resolve("GET", "/api/v1/adminx/backups"); ok {
		t.Fatal("非 /api/v1/admin 前缀不应归类")
	}
}

// 只读 GET 一旦要求 step-up，必须能说出「为什么」：规则必须显式登记在
// stepUpReadRuleJustifications 里。新增只读门控却忘了写理由，这里直接失败。
// 2026-11 事故的教训是「资源名听着敏感」不算理由，必须是具体的秘密字段。
func TestReadStepUpRulesAreJustified(t *testing.T) {
	justified := 0
	for _, entry := range stepUpExact {
		if entry.method != "GET" {
			continue
		}
		reason, ok := StepUpReadRuleJustification(entry.res.Rule)
		if !ok || reason == "" {
			t.Fatalf("GET %s 要求 step-up 但未在 stepUpReadRuleJustifications 说明理由（rule=%s）",
				entry.path, entry.res.Rule)
		}
		justified++
	}
	for _, rule := range sensitiveResources {
		reason, ok := StepUpReadRuleJustification(rule.res.Rule)
		if !ok || reason == "" {
			t.Fatalf("敏感读前缀 %s 覆盖 GET 但未说明 step-up 理由（rule=%s）", rule.prefix, rule.res.Rule)
		}
		justified++
	}
	if justified == 0 {
		t.Fatal("没有登记任何只读 step-up 理由，检查注册表是否被清空")
	}
	// 代理读已掩码，不允许再给它登记「读需要 step-up」的理由。
	if _, ok := StepUpReadRuleJustification("free:proxies"); ok {
		t.Fatal("free:proxies 是 secret_free 家族，不应出现在只读 step-up 理由表里")
	}
}

// 建号可携带初始余额与授权分组：未挂 step-up 时等于凭空发钱/授权。
// 注册表必须把 POST /api/v1/admin/users 归为 step_up_required，
// 且不能被 free:users 家族吞掉。
func TestUserCreationRequiresStepUp(t *testing.T) {
	resolved, ok := Resolve("POST", "/api/v1/admin/users")
	if !ok {
		t.Fatal("POST /api/v1/admin/users 未归类")
	}
	if resolved.Class != ClassStepUpRequired {
		t.Fatalf("POST /api/v1/admin/users 必须要求 step-up，实际 %s/%s", resolved.Class, resolved.Reason)
	}
	if resolved.Reason != ReasonRevenueOperations {
		t.Fatalf("建号理由应为 %s（余额/授权写入），实际 %s", ReasonRevenueOperations, resolved.Reason)
	}
	if resolved.Resource == ResourceNone {
		t.Fatal("POST /api/v1/admin/users 必须标注敏感资源")
	}
	// 其它用户管理读接口保持 secret_free，避免页面加载弹 TOTP。
	read, ok := Resolve("GET", "/api/v1/admin/users")
	if !ok || read.Class != ClassSecretFree {
		t.Fatalf("GET /api/v1/admin/users 应保持 secret_free，实际 %+v ok=%v", read, ok)
	}
}

// secretFreeExact 是只读 POST 批量查询与非敏感探测动作的豁免清单：必须真的
// secret_free、理由非空，且不能借豁免把敏感写操作放过去。
func TestSecretFreeExactEntriesExposeNoSecrets(t *testing.T) {
	if len(secretFreeExact) == 0 {
		t.Fatal("secretFreeExact 不能为空：页面加载的批量只读聚合需要在这里登记")
	}
	for _, entry := range secretFreeExact {
		if entry.method != "POST" {
			t.Fatalf("secretFreeExact 只用于 POST 形式的只读批量查询，实际 %s %s", entry.method, entry.path)
		}
		if entry.res.Class != ClassSecretFree || entry.res.Reason == "" {
			t.Fatalf("secretFreeExact 条目必须带理由的 secret_free: %s", entry.path)
		}
	}
}

// 上游账号「测试」是连通性/模型可用性探测：用库内凭据打上游，只回测试状态，
// 不回传凭据。管理端「测试设置」会按模型逐个点测试，要求 2FA 会让批量测试不可用。
// 边界：凭据回读、导出与账号写操作必须继续保持 step-up。
func TestAccountConnectivityTestIsSecretFreeWhileCredentialAccessStaysGated(t *testing.T) {
	resolved, ok := Resolve("POST", "/api/v1/admin/accounts/:id/test")
	if !ok {
		t.Fatal("POST /api/v1/admin/accounts/:id/test 未归类（运行时按 step-up fail-closed）")
	}
	if resolved.Class != ClassSecretFree || resolved.Reason != ReasonAccountConnectivityTest {
		t.Fatalf("账号测试应为 secret_free/%s，实际 %s/%s", ReasonAccountConnectivityTest, resolved.Class, resolved.Reason)
	}
	if resolved.Resource != ResourceNone {
		t.Fatalf("账号测试不持有敏感资源，实际 %s", resolved.Resource)
	}
	if IsStepUpRequired("POST", "/api/v1/admin/accounts/:id/test") {
		t.Fatal("账号测试不得要求 step-up")
	}

	// 只放开「测试」：凭据读取/导出与账号写操作仍必须 step-up。
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/accounts/:id/credential"},
		{"GET", "/api/v1/admin/accounts/data"},
		{"POST", "/api/v1/admin/accounts"},
		{"POST", "/api/v1/admin/accounts/batch"},
		{"PUT", "/api/v1/admin/accounts/:id"},
		{"DELETE", "/api/v1/admin/accounts/1"},
	} {
		if !IsStepUpRequired(route.method, route.path) {
			t.Fatalf("%s %s 必须继续保持 step-up（本次只放开连通性测试）", route.method, route.path)
		}
	}
}

// 上游模型目录同步/预览只搬模型元数据：同步把模型能力快照写入 accounts.extra
// （模型 ID/上下文/模态等公开元数据），预览不落库；两条都不得回凭据、不得改
// 账号配置/权限/资金。这是「模型白名单 → 同步上游模型被 2FA 误杀」的回归断言。
func TestUpstreamModelCatalogSyncIsSecretFreeWhileAccountSecretsStayGated(t *testing.T) {
	for _, tc := range []struct {
		path   string
		reason Reason
	}{
		{"/api/v1/admin/accounts/:id/models/sync-upstream", ReasonUpstreamModelCatalogSync},
		{"/api/v1/admin/accounts/models/sync-upstream-preview", ReasonUpstreamModelCatalogPreview},
	} {
		resolved, ok := Resolve("POST", tc.path)
		if !ok {
			t.Fatalf("POST %s 未归类（运行时按 step-up fail-closed）", tc.path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason != tc.reason {
			t.Fatalf("POST %s 应为 secret_free/%s，实际 %s/%s", tc.path, tc.reason, resolved.Class, resolved.Reason)
		}
		if resolved.Resource != ResourceNone {
			t.Fatalf("POST %s 不持有敏感资源，实际 %s", tc.path, resolved.Resource)
		}
		if IsStepUpRequired("POST", tc.path) {
			t.Fatalf("POST %s 不应再要求 step-up", tc.path)
		}
		// 仍然只允许真人会话调用：不能顺带进机器白名单。
		if IsMachineAllowlisted("POST", tc.path) {
			t.Fatalf("POST %s 不得进入机器白名单（机器凭据无法完成 2FA）", tc.path)
		}
	}

	// 同族的账号写操作/凭据回读/导出继续保持 step-up。
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/accounts/:id/credential"},
		{"GET", "/api/v1/admin/accounts/data"},
		{"PUT", "/api/v1/admin/accounts/:id"},
		{"POST", "/api/v1/admin/accounts/:id/refresh"},
		{"POST", "/api/v1/admin/accounts/:id/model-whitelist-auto-sync/run"},
	} {
		if !IsStepUpRequired(route.method, route.path) {
			t.Fatalf("%s %s 必须继续保持 step-up", route.method, route.path)
		}
	}
}

// 卡密读模型已硬化为「永远只回掩码」：列表/详情/统计不再要求 step-up（页面
// 加载与对账查询不该弹 TOTP），但批量导出、写操作、机器凭据访问仍受限制。
// 这是 2026-11 2FA 触发矩阵复核里最大的一处「只读 GET 被误归类」修正。
func TestRedeemCodeReadsAreMaskedAndSecretFree(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/redeem-codes",
		"/api/v1/admin/redeem-codes/:id",
		"/api/v1/admin/redeem-codes/stats",
	} {
		resolved, ok := Resolve("GET", path)
		if !ok {
			t.Fatalf("GET %s 未归类", path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason != ReasonRedeemCodeReadsMasked {
			t.Fatalf("GET %s 应为 secret_free/%s，实际 %s/%s", path, ReasonRedeemCodeReadsMasked, resolved.Class, resolved.Reason)
		}
		if IsStepUpRequired("GET", path) {
			t.Fatalf("GET %s 不应再要求 step-up", path)
		}
		if IsMachineAllowlisted("GET", path) {
			t.Fatalf("GET %s 不得进入机器白名单（成批业务元数据仍要求人只会话）", path)
		}
	}
	// 批量 CSV 导出仍按导出类动作保留 step-up。
	exported, ok := Resolve("GET", "/api/v1/admin/redeem-codes/export")
	if !ok || exported.Class != ClassStepUpRequired || exported.Reason != ReasonRedeemCodeExport {
		t.Fatalf("GET /api/v1/admin/redeem-codes/export 必须要求 step-up（批量导出），实际 %+v ok=%v", exported, ok)
	}
}

// 优惠码使用记录不含 code 本身，属于只读低风险；优惠码列表/详情仍回明文可兑
// 值，必须保持 step-up。这条精确豁免由 maskedReadExact 在敏感前缀之前命中。
func TestPromoCodeUsageReadIsSecretFreeWhileCodeReadsStayGated(t *testing.T) {
	usages, ok := Resolve("GET", "/api/v1/admin/promo-codes/:id/usages")
	if !ok || usages.Class != ClassSecretFree || usages.Reason != ReasonPromoUsageReadsNoCode {
		t.Fatalf("GET /api/v1/admin/promo-codes/:id/usages 应为 secret_free/%s，实际 %+v ok=%v", ReasonPromoUsageReadsNoCode, usages, ok)
	}
	for _, path := range []string{"/api/v1/admin/promo-codes", "/api/v1/admin/promo-codes/:id"} {
		resolved, ok := Resolve("GET", path)
		if !ok || resolved.Class != ClassStepUpRequired {
			t.Fatalf("GET %s 必须保持 step-up（返回明文优惠码），实际 %+v ok=%v", path, resolved, ok)
		}
	}
}

// 本轮放开的一批「探测/预览/聚合」动作：必须是 secret_free、理由非空、不要求
// step-up。任何一条回退成 step-up 都会让对应 UI 按钮重新弹 TOTP。
func TestLowRiskAdminProbesAreSecretFree(t *testing.T) {
	cases := []struct {
		method string
		path   string
		reason Reason
	}{
		{"POST", "/api/v1/admin/proxies/:id/test", ReasonProxyConnectivityTest},
		{"POST", "/api/v1/admin/proxies/:id/quality-check", ReasonProxyConnectivityTest},
		{"POST", "/api/v1/admin/accounts/check-mixed-channel", ReasonAccountBindingPreflight},
		{"POST", "/api/v1/admin/accounts/:id/schedulable-timers/preview", ReasonAccountTimerPreview},
		{"POST", "/api/v1/admin/accounts/:id/ollama-cloud-usage/refresh", ReasonAccountUsageAggregate},
		// 「模型限制 → 模型白名单」的两个同步按钮：只拉上游模型元数据/一次预览，
		// 同步仅写 accounts.extra[upstream_models] 模型能力快照（公开元数据）。
		{"POST", "/api/v1/admin/accounts/:id/models/sync-upstream", ReasonUpstreamModelCatalogSync},
		{"POST", "/api/v1/admin/accounts/models/sync-upstream-preview", ReasonUpstreamModelCatalogPreview},
		{"POST", "/api/v1/admin/upstream-monitor/channels/:id/announcements/sync", ReasonChannelAnnouncementSync},
		{"POST", "/api/v1/admin/settings/test-smtp", ReasonSMTPConnectivityTest},
		{"POST", "/api/v1/admin/settings/web-search-emulation/test", ReasonWebSearchEmulationProbe},
		{"POST", "/api/v1/admin/settings/ip-risk/check", ReasonIPRiskPreflightCheck},
		// 邮件模板预览：内存渲染未保存的模板，不写库/不出站/不碰密钥。
		{"POST", "/api/v1/admin/settings/email-template-preview", ReasonEmailTemplatePreview},
	}
	for _, tc := range cases {
		resolved, ok := Resolve(tc.method, tc.path)
		if !ok {
			t.Fatalf("%s %s 未归类", tc.method, tc.path)
		}
		if resolved.Class != ClassSecretFree || resolved.Reason != tc.reason {
			t.Fatalf("%s %s 应为 secret_free/%s，实际 %s/%s", tc.method, tc.path, tc.reason, resolved.Class, resolved.Reason)
		}
		if resolved.Resource != ResourceNone {
			t.Fatalf("%s %s 已放开却仍标注敏感资源 %s", tc.method, tc.path, resolved.Resource)
		}
		if IsStepUpRequired(tc.method, tc.path) {
			t.Fatalf("%s %s 不应再要求 step-up", tc.method, tc.path)
		}
		if IsMachineAllowlisted(tc.method, tc.path) {
			t.Fatalf("%s %s 不得进入机器白名单", tc.method, tc.path)
		}
	}
}

// 放开探测动作不得顺带放开同族的写操作、凭据回读、导出、资金与安全配置。
// 这是「放开」批次的边界，任何越界都会在 CI 变红。
func TestLowRiskAdminProbesDoNotCrossSensitiveBoundaries(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		// 凭据回读 / 导出 / debug dump / S3 凭证
		{"GET", "/api/v1/admin/accounts/:id/credential"},
		{"GET", "/api/v1/admin/accounts/data"},
		{"GET", "/api/v1/admin/proxies/data"},
		{"GET", "/api/v1/admin/backups/s3-config"},
		{"GET", "/api/v1/admin/debug-dumps/:name"},
		{"GET", "/api/v1/admin/users/:id/api-keys"},
		{"GET", "/api/v1/admin/groups/:id/api-keys"},
		// 账号写操作与「刷新类写操作」：refresh 会换上游 token，auto-sync 会改模型映射
		{"PUT", "/api/v1/admin/accounts/:id"},
		{"POST", "/api/v1/admin/accounts"},
		{"POST", "/api/v1/admin/accounts/batch"},
		{"POST", "/api/v1/admin/accounts/:id/refresh"},
		{"POST", "/api/v1/admin/accounts/:id/model-whitelist-auto-sync/run"},
		// CRS 同步/预览会用请求体里的 base_url + 账号密码做真实出站登录，并返回
		// CRS 账号元数据：属于「管理员可指向任意地址的外部系统读取」，保持 step-up。
		{"POST", "/api/v1/admin/accounts/sync/crs"},
		{"POST", "/api/v1/admin/accounts/sync/crs/preview"},
		// 代理写操作
		{"POST", "/api/v1/admin/proxies"},
		{"PUT", "/api/v1/admin/proxies/:id"},
		// 资金 / 权限 / 安全配置
		{"POST", "/api/v1/admin/users/:id/balance"},
		{"POST", "/api/v1/admin/users"},
		{"POST", "/api/v1/admin/subscriptions/assign"},
		{"PUT", "/api/v1/admin/settings"},
		{"POST", "/api/v1/admin/settings/send-test-email"},
		{"POST", "/api/v1/admin/settings/admin-api-keys"},
		{"PUT", "/api/v1/admin/payment/config"},
		{"POST", "/api/v1/admin/payment/orders/:id/refund"},
		{"POST", "/api/v1/admin/system/update"},
		{"POST", "/api/v1/admin/redeem-codes/generate"},
		{"GET", "/api/v1/admin/redeem-codes/export"},
	} {
		if !IsStepUpRequired(route.method, route.path) {
			t.Fatalf("%s %s 属于敏感边界，必须保持 step-up", route.method, route.path)
		}
	}
}

// maskedReadExact 的形状约束：只放 GET 只读、必须 secret_free 且带理由；
// 且这些路由不得进入机器白名单（成批业务元数据只允许人只会话）。
func TestMaskedReadExactEntriesShape(t *testing.T) {
	if len(maskedReadExact) == 0 {
		t.Fatal("maskedReadExact 不能为空")
	}
	for _, entry := range maskedReadExact {
		if entry.method != "GET" {
			t.Fatalf("maskedReadExact 只用于只读 GET，实际 %s %s", entry.method, entry.path)
		}
		if entry.res.Class != ClassSecretFree || entry.res.Reason == "" || entry.res.Rule == "" {
			t.Fatalf("maskedReadExact 条目必须带理由/规则的 secret_free: %s", entry.path)
		}
		if IsMachineAllowlisted(entry.method, entry.path) {
			t.Fatalf("maskedReadExact 条目不得进入机器白名单: %s", entry.path)
		}
	}
}

// 风控审计 / 提示词审计 / 渠道监控与模板 / 渠道定价 / 安全处罚的写操作全部要求 step-up 2FA，
// 防止通过修改外部探测/审计地址偷取已存储的 API Key 或未过 2FA 篡改风控/定价策略；
// 只读 GET 保持 secret_free，避免页面加载弹 TOTP。
func TestSixHardenedAdminRouteFamiliesRequireStepUpForMutationsOnly(t *testing.T) {
	families := []struct {
		prefix   string
		readPath string
	}{
		{"/api/v1/admin/risk-control", "/api/v1/admin/risk-control/config"},
		{"/api/v1/admin/prompt-audit", "/api/v1/admin/prompt-audit/config"},
		{"/api/v1/admin/channel-monitors", "/api/v1/admin/channel-monitors"},
		{"/api/v1/admin/channel-monitor-templates", "/api/v1/admin/channel-monitor-templates"},
		{"/api/v1/admin/channels", "/api/v1/admin/channels"},
		{"/api/v1/admin/security-penalties", "/api/v1/admin/security-penalties/config"},
	}
	for _, f := range families {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			for _, path := range []string{f.prefix, f.prefix + "/1"} {
				resolved, ok := Resolve(method, path)
				if !ok {
					t.Fatalf("%s %s 未归类", method, path)
				}
				if resolved.Class != ClassStepUpRequired {
					t.Fatalf("%s %s 必须要求 step-up，实际 %s/%s", method, path, resolved.Class, resolved.Reason)
				}
				if resolved.Resource == ResourceNone {
					t.Fatalf("%s %s 必须标注敏感资源", method, path)
				}
				if IsMachineAllowlisted(method, path) {
					t.Fatalf("%s %s 不得进入机器白名单", method, path)
				}
			}
		}
		read, ok := Resolve("GET", f.readPath)
		if !ok || read.Class != ClassSecretFree {
			t.Fatalf("GET %s 应保持 secret_free，实际 %+v ok=%v", f.readPath, read, ok)
		}
		if IsStepUpRequired("GET", f.readPath) {
			t.Fatalf("GET %s 不应要求 step-up", f.readPath)
		}
	}
}
