//go:build unit

package adminroutepolicy

import "testing"

// 卡密/优惠码是「返回即秘密」的资源：任何方法（含 GET）都必须是
// step_up_required。这是「只读所以不用 2FA」口径被废弃后的硬约束。
// 代理与上游渠道不在其中：读模型已掩码，只有明文导出/密钥 reveal 与写操作要求 2FA。
func TestSecretReturningResourcesRequireStepUpForEveryMethod(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/redeem-codes",
		"/api/v1/admin/redeem-codes/1",
		"/api/v1/admin/redeem-codes/stats",
		"/api/v1/admin/promo-codes",
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

// secretFreeExact 是只读 POST 批量的豁免清单：必须真的 secret_free、理由非空，
// 且不能借豁免把敏感写操作放过去。
func TestSecretFreeExactEntriesAreReadOnlyAggregates(t *testing.T) {
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
