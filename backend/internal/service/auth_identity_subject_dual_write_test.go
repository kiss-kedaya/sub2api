//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func restoreAuthSubjectPlaintextDualWrite(t *testing.T, enabled bool) {
	t.Helper()
	ConfigureAuthSubjectPlaintextDualWrite(enabled)
	t.Cleanup(func() { ConfigureAuthSubjectPlaintextDualWrite(false) })
}

// 阶段 B 默认：pending 会话只落 hash/prefix，provider_subject 保持 NULL，
// 完成流程不再需要从库里解出 subject 原文。
func TestCreatePendingSessionHashOnlyByDefault(t *testing.T) {
	restoreAuthSubjectPlaintextDualWrite(t, false)
	svc, _ := newAuthPendingIdentityServiceTestClient(t)
	ctx := context.Background()

	const subject = "linuxdo-pending-hash-only-123456"
	session, err := svc.CreatePendingSession(ctx, CreatePendingAuthSessionInput{
		Intent: "login",
		Identity: PendingAuthIdentityKey{
			ProviderType:    "linuxdo",
			ProviderKey:     "linuxdo-main",
			ProviderSubject: subject,
		},
	})
	require.NoError(t, err)
	require.Nil(t, session.ProviderSubject, "pending session must not persist subject material by default")
	require.NotNil(t, session.ProviderSubjectHash)
	require.Equal(t, HashAuthIdentitySubject("linuxdo", subject), *session.ProviderSubjectHash)
	require.NotNil(t, session.ProviderSubjectPrefix)
	require.Equal(t, AuthSubjectPrefixFromHash(HashAuthIdentitySubject("linuxdo", subject)), *session.ProviderSubjectPrefix)
}

// 过渡期开关打开：保持旧行为，subject 封存后写入 provider_subject 列，
// 旧兼容路径仍能解出原文。
func TestCreatePendingSessionSealsSubjectWhenDualWriteEnabled(t *testing.T) {
	restoreAuthSubjectPlaintextDualWrite(t, true)
	svc, _ := newAuthPendingIdentityServiceTestClient(t)
	ctx := context.Background()

	const subject = "wechat-pending-sealed-123456"
	session, err := svc.CreatePendingSession(ctx, CreatePendingAuthSessionInput{
		Intent: "login",
		Identity: PendingAuthIdentityKey{
			ProviderType:    "wechat",
			ProviderKey:     "wechat-open",
			ProviderSubject: subject,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, session.ProviderSubject)
	require.True(t, IsAuthSubjectSealed(*session.ProviderSubject))
	require.Equal(t, subject, PendingAuthSubjectPlaintext(*session.ProviderSubject))
	require.NotNil(t, session.ProviderSubjectHash)
	require.Equal(t, HashAuthIdentitySubject("wechat", subject), *session.ProviderSubjectHash)
}
