package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// fakePush records what the services tried to push.
type fakePush struct {
	users  []int64
	admins int
	titles []string
}

func (f *fakePush) PushToUser(_ context.Context, userID int64, title, _, _ string) error {
	f.users = append(f.users, userID)
	f.titles = append(f.titles, title)
	return nil
}

func (f *fakePush) PushToAdmins(_ context.Context, title, _, _ string) error {
	f.admins++
	f.titles = append(f.titles, title)
	return nil
}

// A low-balance user with push enabled must get exactly one push naming the
// balance and threshold; the phone message is the operator-visible contract.
func TestPushUserBalanceLowSendsToTheUser(t *testing.T) {
	push := &fakePush{}
	svc := &BalanceNotifyService{pushNotifier: push}
	svc.pushUserBalanceLow(&User{ID: 42}, 3.5, 10)

	if len(push.users) != 1 || push.users[0] != 42 {
		t.Fatalf("pushed users = %v, want [42]", push.users)
	}
	if len(push.titles) != 1 || push.titles[0] != "余额不足" {
		t.Fatalf("titles = %v", push.titles)
	}
}

// Without a notifier the path is a no-op, so forks that never configure
// push keep working exactly as before.
func TestPushUserBalanceLowNilNotifierIsNoOp(t *testing.T) {
	svc := &BalanceNotifyService{}
	svc.pushUserBalanceLow(&User{ID: 1}, 1, 2)
}

// Pausing an upstream account must alert the admins exactly once per pause.
func TestCNBalancePushAdminsOnPause(t *testing.T) {
	push := &fakePush{}
	svc := &CNProviderBalanceCheckService{push: push, cfg: &config.Config{}}
	svc.pushAdmins(
		&Account{ID: 7, Platform: PlatformKimi},
		&CNProviderBalanceResult{Balance: 1.23, Currency: "CNY", Success: true},
		5,
	)
	if push.admins != 1 {
		t.Fatalf("admin pushes = %d, want 1", push.admins)
	}
	if len(push.titles) != 1 || push.titles[0] != "上游余额不足" {
		t.Fatalf("titles = %v", push.titles)
	}
}

func TestCNBalancePushAdminsNilSafe(t *testing.T) {
	svc := &CNProviderBalanceCheckService{cfg: &config.Config{}}
	svc.pushAdmins(&Account{ID: 1, Platform: PlatformKimi}, &CNProviderBalanceResult{}, 1)
	svc.pushAdmins(nil, nil, 0)
}
