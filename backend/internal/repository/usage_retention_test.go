//go:build unit

package repository

import "testing"

// 诊断类表的保留窗口统一 7 天。写死断言是刻意的：这两张表按 GB/天 增长，
// 谁把窗口改大都会立刻体现在磁盘上，测试先拦住。
func TestDiagnosticRetentionWindowsAreSevenDays(t *testing.T) {
	if connectionTraceRetentionDays != 7 {
		t.Fatalf("request_connection_traces retention = %d days, want 7", connectionTraceRetentionDays)
	}
	if userTrafficEventRetentionDays != 7 {
		t.Fatalf("user_traffic_events retention = %d days, want 7", userTrafficEventRetentionDays)
	}
	if connectionTracePrunePasses <= 0 || userTrafficEventPrunePasses <= 0 {
		t.Fatal("prune passes must allow the hourly loop to outpace ingestion")
	}
}
