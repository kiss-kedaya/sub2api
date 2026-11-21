//go:build unit

package handler

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const authIdentityHashTestSubject = "github-subject-1234567890"

func newAuthIdentityHashTestUser(t *testing.T, client *dbent.Client) *dbent.User {
	t.Helper()

	user, err := client.User.Create().
		SetEmail("subject-hash-login@example.com").
		SetUsername("subject-hash-login").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(context.Background())
	require.NoError(t, err)
	return user
}

// 登录匹配主路径：明文列已被清空，findOAuthIdentityUser 只靠 hash 命中。
func TestFindOAuthIdentityUserMatchesHashWithoutPlaintext(t *testing.T) {
	authHandler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()
	user := newAuthIdentityHashTestUser(t, client)

	_, err := client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("github").
		SetProviderKey("github").
		SetProviderSubjectHash(service.HashAuthIdentitySubject("github", authIdentityHashTestSubject)).
		SetProviderSubjectPrefix(service.AuthSubjectPrefixFromHash(service.HashAuthIdentitySubject("github", authIdentityHashTestSubject))).
		SetMetadata(map[string]any{"source": "test"}).
		Save(ctx)
	require.NoError(t, err)

	found, err := authHandler.findOAuthIdentityUser(ctx, service.PendingAuthIdentityKey{
		ProviderType:    "github",
		ProviderKey:     "github",
		ProviderSubject: " " + authIdentityHashTestSubject + " ",
	})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, user.ID, found.ID)
}

// 过渡期回退：未回填 hash 的历史行仍能按明文命中。
func TestFindOAuthIdentityUserFallsBackToPlaintext(t *testing.T) {
	authHandler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()
	user := newAuthIdentityHashTestUser(t, client)

	_, err := client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("github").
		SetProviderKey("github").
		SetProviderSubject(authIdentityHashTestSubject).
		SetMetadata(map[string]any{"source": "legacy"}).
		Save(ctx)
	require.NoError(t, err)

	found, err := authHandler.findOAuthIdentityUser(ctx, service.PendingAuthIdentityKey{
		ProviderType:    "github",
		ProviderKey:     "github",
		ProviderSubject: authIdentityHashTestSubject,
	})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, user.ID, found.ID)
}

// 写路径：pending OAuth 流程新建身份时必须同时落 hash + 前缀。
func TestEnsurePendingOAuthIdentityWritesHashAndPrefix(t *testing.T) {
	_, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()
	user := newAuthIdentityHashTestUser(t, client)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	subjectPlaintext, subjectHash, subjectPrefix := pendingSessionSubjectFields("oidc", authIdentityHashTestSubject)
	identity, err := ensurePendingOAuthIdentityForUser(
		dbent.NewTxContext(ctx, tx),
		tx,
		&dbent.PendingAuthSession{
			ProviderType:           "oidc",
			ProviderKey:            "https://issuer.example",
			ProviderSubject:        subjectPlaintext,
			ProviderSubjectHash:    subjectHash,
			ProviderSubjectPrefix:  subjectPrefix,
			UpstreamIdentityClaims: map[string]any{"issuer": "https://issuer.example"},
		},
		user.ID,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	require.NotNil(t, identity.ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("oidc", authIdentityHashTestSubject), *identity.ProviderSubjectHash)
	require.NotNil(t, identity.ProviderSubjectPrefix)
	require.Equal(t, service.AuthSubjectPrefixFromHash(service.HashAuthIdentitySubject("oidc", authIdentityHashTestSubject)), *identity.ProviderSubjectPrefix)
}

// 阶段 B 默认：pending 流程派生身份时明文列必须保持 NULL，
// 只靠 keyed hash + 前缀落库（hash 命中路径不受影响）。
func TestEnsurePendingOAuthIdentitySkipsPlaintextWhenDualWriteDisabled(t *testing.T) {
	_, client := newOAuthPendingFlowTestHandler(t, false)
	// 该 helper 为覆盖阶段 A 旧流程默认打开了过渡开关，这里显式回到阶段 B 默认。
	service.ConfigureAuthSubjectPlaintextDualWrite(false)
	t.Cleanup(func() { service.ConfigureAuthSubjectPlaintextDualWrite(false) })

	ctx := context.Background()
	user := newAuthIdentityHashTestUser(t, client)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	subjectPlaintext, subjectHash, subjectPrefix := pendingSessionSubjectFields("oidc", authIdentityHashTestSubject)
	identity, err := ensurePendingOAuthIdentityForUser(
		dbent.NewTxContext(ctx, tx),
		tx,
		&dbent.PendingAuthSession{
			ProviderType:           "oidc",
			ProviderKey:            "https://issuer.example",
			ProviderSubject:        subjectPlaintext,
			ProviderSubjectHash:    subjectHash,
			ProviderSubjectPrefix:  subjectPrefix,
			UpstreamIdentityClaims: map[string]any{"issuer": "https://issuer.example"},
		},
		user.ID,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	require.NotNil(t, identity.ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("oidc", authIdentityHashTestSubject), *identity.ProviderSubjectHash)
	require.Nil(t, identity.ProviderSubject, "phase B must not persist subject plaintext")
}
