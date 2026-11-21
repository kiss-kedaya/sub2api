//go:build unit

package adminroutepolicy

import "testing"

// 卡密/优惠码/代理/上游渠道密钥是「返回即秘密」的资源：任何方法（含 GET）
// 都必须是 step_up_required。这是「只读所以不用 2FA」口径被废弃后的硬约束。
func TestSecretReturningResourcesRequireStepUpForEveryMethod(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/redeem-codes",
		"/api/v1/admin/redeem-codes/1",
		"/api/v1/admin/redeem-codes/stats",
		"/api/v1/admin/promo-codes",
		"/api/v1/admin/proxies",
		"/api/v1/admin/proxies/all",
		"/api/v1/admin/upstream-monitor/channels",
		"/api/v1/admin/upstream-monitor/channels/1",
		"/api/v1/admin/upstream-monitor/channels/1/api-keys",
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
