package cryptopay

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type walletOperationPush struct {
	calls       int
	title, body string
	err         error
}

func (p *walletOperationPush) PushToAdmins(_ context.Context, title, body, _ string) error {
	p.calls++
	p.title, p.body = title, body
	return p.err
}

func TestWalletOperationNotificationsAreFixedMetadataAndFailClosed(t *testing.T) {
	for _, action := range []string{"sweep", "destination_change", "export_mnemonics", "export_private_key"} {
		for _, phase := range []string{"requested", "verified"} {
			s := &Service{}
			push := &walletOperationPush{}
			s.SetPushNotifier(push)
			require.NoError(t, s.NotifyWalletOperation(context.Background(), action, phase))
			require.Equal(t, 1, push.calls)
			require.Contains(t, push.title, "钱包敏感操作")
			require.Contains(t, push.body, "不代表操作已")
			push.err = errors.New("offline")
			require.Error(t, s.NotifyWalletOperation(context.Background(), action, phase))
		}
	}
	s := &Service{}
	require.Error(t, s.NotifyWalletOperation(context.Background(), "sweep", "requested"))
	push := &walletOperationPush{}
	s.SetPushNotifier(push)
	require.Error(t, s.NotifyWalletOperation(context.Background(), "private-value", "requested"))
	require.Zero(t, push.calls)
	require.Error(t, s.NotifyWalletOperation(context.Background(), "sweep", "completed"))
	require.Zero(t, push.calls)
}
