package dto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUsageLogFromInflightDisplayAndAccountPrivacy(t *testing.T) {
	now := time.Now()
	row := service.UsageInflightSnapshot{
		RequestID: "inflight-fixture", UserID: 2, APIKeyID: 4,
		AccountID: 8, AccountName: "private-upstream-fixture", GroupID: 16, GroupName: "fixture-group",
		StartedAt: now.Add(-time.Second),
	}
	user := UsageLogFromInflight(row, now)
	if user.Group == nil || user.Group.ID != 16 || user.Group.Name != row.GroupName {
		t.Fatalf("inflight group missing: %+v", user.Group)
	}
	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), row.AccountName) || strings.Contains(string(encoded), `"account":`) {
		t.Fatalf("user response exposes upstream details: %s", encoded)
	}
	admin := AdminUsageLogFromInflight(row, now)
	if admin.Account == nil || admin.Account.ID != 8 || admin.Account.Name != row.AccountName {
		t.Fatalf("admin inflight account missing: %+v", admin.Account)
	}
	observer, err := json.Marshal(UsageLogForObserver(admin))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(observer), row.AccountName) || strings.Contains(string(observer), row.GroupName) {
		t.Fatalf("observer response exposes names: %s", observer)
	}
}
