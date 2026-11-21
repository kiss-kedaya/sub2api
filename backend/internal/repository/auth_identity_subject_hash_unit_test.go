//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// newAuthSubjectTestUser 建一个可登录的 active 用户，仓储查询走 WithUser 时需要它存在。
func newAuthSubjectTestUser(t *testing.T, repo *userRepository, email string) *service.User {
	t.Helper()

	user := &service.User{
		Email:        email,
		Username:     email,
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	}
	require.NoError(t, repo.Create(context.Background(), user))
	return user
}

// 登录匹配主路径：明文列已被清空（NULL），只靠 provider_subject_hash 命中。
func TestGetUserByCanonicalIdentityMatchesHashAfterPlaintextCleared(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "hash-login@example.com")

	const subject = "github-subject-1234567890"
	identity, err := client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("github").
		SetProviderKey("github").
		SetProviderSubjectHash(service.HashAuthIdentitySubject("github", subject)).
		SetProviderSubjectPrefix(service.AuthSubjectPrefixFromHash(service.HashAuthIdentitySubject("github", subject))).
		SetMetadata(map[string]any{"source": "test"}).
		Save(ctx)
	require.NoError(t, err)
	require.Nil(t, identity.ProviderSubject, "hash-only row must not keep plaintext subject")

	lookup, err := repo.GetUserByCanonicalIdentity(ctx, AuthIdentityKey{
		ProviderType: "github",
		ProviderKey:  "github",
		// 登录入口传入的 subject 带空格也要能命中：与写入路径共享同一 TrimSpace 规范化。
		ProviderSubject: "  " + subject + "  ",
	})
	require.NoError(t, err)
	require.NotNil(t, lookup)
	require.NotNil(t, lookup.User)
	require.Equal(t, user.ID, lookup.User.ID)
	require.Equal(t, identity.ID, lookup.Identity.ID)
}

// 过渡期回退路径：历史行还没回填 hash，查询必须仍能命中明文列。
// 回填后再查一次，验证「同一行从明文命中切到 hash 命中」无感。
func TestGetUserByCanonicalIdentityFallsBackToPlaintextBeforeBackfill(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "fallback-login@example.com")

	const subject = "legacy-plaintext-subject-123456"
	identity, err := client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject(subject).
		SetMetadata(map[string]any{"source": "legacy"}).
		Save(ctx)
	_ = service.HashAuthIdentitySubject("linuxdo", subject)
	require.NoError(t, err)
	require.Nil(t, identity.ProviderSubjectHash, "legacy row starts without hash")

	lookup, err := repo.GetUserByCanonicalIdentity(ctx, AuthIdentityKey{
		ProviderType:    "linuxdo",
		ProviderKey:     "linuxdo",
		ProviderSubject: subject,
	})
	require.NoError(t, err)
	require.Equal(t, identity.ID, lookup.Identity.ID)

	// 模拟运维回填
	require.NoError(t, client.AuthIdentity.UpdateOneID(identity.ID).
		SetProviderSubjectHash(service.HashAuthIdentitySubject("linuxdo", subject)).
		SetProviderSubjectPrefix(service.AuthSubjectPrefixFromHash(service.HashAuthIdentitySubject("github", subject))).
		Exec(ctx))

	lookup, err = repo.GetUserByCanonicalIdentity(ctx, AuthIdentityKey{
		ProviderType:    "linuxdo",
		ProviderKey:     "linuxdo",
		ProviderSubject: subject,
	})
	require.NoError(t, err)
	require.Equal(t, identity.ID, lookup.Identity.ID)
}

// 写入路径必须同时落 hash + 前缀；否则清空明文后无法再匹配 / 展示。
func TestCreateAuthIdentityWritesHashAndPrefix(t *testing.T) {
	repo, _ := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "create-hash@example.com")

	const subject = "wechat-unionid-abcdef123456"
	result, err := repo.CreateAuthIdentity(ctx, CreateAuthIdentityInput{
		UserID: user.ID,
		Canonical: AuthIdentityKey{
			ProviderType:    "wechat",
			ProviderKey:     "wechat-main",
			ProviderSubject: subject,
		},
		VerifiedAt: nil,
		Metadata:   map[string]any{"source": "test"},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Identity.ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("wechat", subject), *result.Identity.ProviderSubjectHash)
	require.NotNil(t, result.Identity.ProviderSubjectPrefix)
	require.Equal(t, service.AuthSubjectPrefixFromHash(service.HashAuthIdentitySubject("wechat", subject)), *result.Identity.ProviderSubjectPrefix)
	// 过渡期明文仍要写入，供旧版本回退查询/对账。
	require.NotNil(t, result.Identity.ProviderSubject)
	require.Equal(t, subject, *result.Identity.ProviderSubject)
	// IdentityRef 用明文（过渡期）保持兼容。
	require.Equal(t, subject, result.IdentityRef().ProviderSubject)
}

// 绑定去重：同一 (provider_type, provider_key, subject) 重复绑定只保留一行，
// 绑定到别的用户则冲突。
func TestBindAuthIdentityToUserDeduplicatesByHash(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	owner := newAuthSubjectTestUser(t, repo, "bind-owner@example.com")
	other := newAuthSubjectTestUser(t, repo, "bind-other@example.com")

	const subject = "dingtalk-unionid-1234567890"
	input := BindAuthIdentityInput{
		UserID: owner.ID,
		Canonical: AuthIdentityKey{
			ProviderType:    "dingtalk",
			ProviderKey:     "dingtalk",
			ProviderSubject: subject,
		},
		Metadata: map[string]any{"source": "bind-1"},
	}

	first, err := repo.BindAuthIdentityToUser(ctx, input)
	require.NoError(t, err)
	second, err := repo.BindAuthIdentityToUser(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.Identity.ID, second.Identity.ID, "rebinding same subject must reuse the row")

	count, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("dingtalk"),
			authidentity.ProviderKeyEQ("dingtalk"),
			authidentity.ProviderSubjectHashEQ(service.HashAuthIdentitySubject("dingtalk", subject)),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	conflictInput := input
	conflictInput.UserID = other.ID
	_, err = repo.BindAuthIdentityToUser(ctx, conflictInput)
	require.ErrorIs(t, err, ErrAuthIdentityOwnershipConflict)
}

// 唯一约束：盲索引列本身必须挡住重复哈希（即使明文不同/被清空）。
func TestAuthIdentityHashColumnHasUniqueConstraint(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "unique-hash@example.com")

	hash := service.HashAuthIdentitySubject("oidc", "subject-a-1234567890")
	_, err := client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("subject-a-1234567890").
		SetProviderSubjectHash(hash).
		Save(ctx)
	require.NoError(t, err)

	// 相同 (provider_type, provider_key, hash)、不同明文：模拟历史残留行。
	// 明文唯一索引挡不住它，必须由 hash 唯一索引挡住。
	_, err = client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("legacy-row-with-same-hash").
		SetProviderSubjectHash(hash).
		Save(ctx)
	require.Error(t, err, "duplicate provider_subject_hash must violate the unique index")
}

// 渠道 subject 同样 keyed 哈希化：写入时三列都写，清空明文后按 hash 仍能命中。
func TestChannelSubjectHashWrittenAndHashOnlyLookup(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "channel-hash@example.com")

	const (
		providerSubject = "union-channel-hash-123456"
		channelSubject  = "openid-channel-hash-123456"
	)
	result, err := repo.BindAuthIdentityToUser(ctx, BindAuthIdentityInput{
		UserID: user.ID,
		Canonical: AuthIdentityKey{
			ProviderType:    "wechat",
			ProviderKey:     "wechat-main",
			ProviderSubject: providerSubject,
		},
		Channel: &AuthIdentityChannelKey{
			ProviderType:   "wechat",
			ProviderKey:    "wechat-main",
			Channel:        "open",
			ChannelAppID:   "wx-app",
			ChannelSubject: channelSubject,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Channel)
	require.NotNil(t, result.Channel.ChannelSubjectHash)
	require.Equal(t, service.HashAuthChannelSubject("wechat", channelSubject), *result.Channel.ChannelSubjectHash)
	require.NotNil(t, result.Channel.ChannelSubjectPrefix)
	require.Equal(t, service.AuthSubjectPrefixFromHash(service.HashAuthChannelSubject("wechat", channelSubject)), *result.Channel.ChannelSubjectPrefix)

	// 模拟清空明文后的渠道行：只留 hash。
	_, err = client.AuthIdentityChannel.UpdateOneID(result.Channel.ID).
		SetNillableChannelSubject(nil).
		Save(ctx)
	require.NoError(t, err)

	lookup, err := repo.GetUserByChannelIdentity(ctx, AuthIdentityChannelKey{
		ProviderType:   "wechat",
		ProviderKey:    "wechat-main",
		Channel:        "open",
		ChannelAppID:   "wx-app",
		ChannelSubject: channelSubject,
	})
	require.NoError(t, err)
	require.Equal(t, result.Channel.ID, lookup.Channel.ID)
	require.Equal(t, user.ID, lookup.User.ID)
}

// 渠道盲索引列必须挡住重复哈希（即使明文不同/被清空）。
func TestChannelHashColumnHasUniqueConstraint(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "channel-unique@example.com")

	identity, err := repo.CreateAuthIdentity(ctx, CreateAuthIdentityInput{
		UserID: user.ID,
		Canonical: AuthIdentityKey{
			ProviderType:    "wechat",
			ProviderKey:     "wechat-main",
			ProviderSubject: "union-channel-unique-123456",
		},
	})
	require.NoError(t, err)

	hashValue := service.HashAuthChannelSubject("wechat", "openid-unique-123456")
	_, err = client.AuthIdentityChannel.Create().
		SetIdentityID(identity.Identity.ID).
		SetProviderType("wechat").
		SetProviderKey("wechat-main").
		SetChannel("open").
		SetChannelAppID("wx-app").
		SetChannelSubject("openid-unique-123456").
		SetChannelSubjectHash(hashValue).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.AuthIdentityChannel.Create().
		SetIdentityID(identity.Identity.ID).
		SetProviderType("wechat").
		SetProviderKey("wechat-main").
		SetChannel("open").
		SetChannelAppID("wx-app").
		SetChannelSubject("legacy-row-with-same-hash").
		SetChannelSubjectHash(hashValue).
		Save(ctx)
	require.Error(t, err, "duplicate channel_subject_hash must violate the unique index")
}
