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

// enableAuthSubjectPlaintextDualWriteForTest 打开过渡期明文双写（旧行为），
// 测试结束后恢复默认关闭。
func enableAuthSubjectPlaintextDualWriteForTest(t *testing.T) {
	t.Helper()
	service.ConfigureAuthSubjectPlaintextDualWrite(true)
	t.Cleanup(func() { service.ConfigureAuthSubjectPlaintextDualWrite(false) })
}

// disableAuthSubjectPlaintextDualWriteForTest 固定为默认行为：只写 keyed 盲索引。
func disableAuthSubjectPlaintextDualWriteForTest(t *testing.T) {
	t.Helper()
	service.ConfigureAuthSubjectPlaintextDualWrite(false)
	t.Cleanup(func() { service.ConfigureAuthSubjectPlaintextDualWrite(false) })
}

// 写入路径必须同时落 hash + 前缀；否则清空明文后无法再匹配 / 展示。
func TestCreateAuthIdentityWritesHashAndPrefix(t *testing.T) {
	enableAuthSubjectPlaintextDualWriteForTest(t)
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

// 阶段 B 默认行为：明文双写关闭时，新建 identity 只落 hash/prefix，
// provider_subject 必须是 NULL（不再制造可逆的 subject 明文）。
func TestCreateAuthIdentityWritesHashOnlyWhenDualWriteDisabled(t *testing.T) {
	disableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "hash-only-create@example.com")

	const subject = "github-subject-hash-only-123456"
	result, err := repo.CreateAuthIdentity(ctx, CreateAuthIdentityInput{
		UserID: user.ID,
		Canonical: AuthIdentityKey{
			ProviderType:    "github",
			ProviderKey:     "github",
			ProviderSubject: subject,
		},
		Metadata: map[string]any{"source": "test"},
	})
	require.NoError(t, err)
	require.Nil(t, result.Identity.ProviderSubject, "plaintext subject must stay NULL by default")
	require.NotNil(t, result.Identity.ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("github", subject), *result.Identity.ProviderSubjectHash)
	require.NotNil(t, result.Identity.ProviderSubjectPrefix)
	require.Equal(t, service.AuthSubjectPrefixFromHash(service.HashAuthIdentitySubject("github", subject)), *result.Identity.ProviderSubjectPrefix)

	stored, err := client.AuthIdentity.Get(ctx, result.Identity.ID)
	require.NoError(t, err)
	require.Nil(t, stored.ProviderSubject)

	// 明文没落库，登录仍必须靠 hash 命中（带空格也走同一规范化）。
	lookup, err := repo.GetUserByCanonicalIdentity(ctx, AuthIdentityKey{
		ProviderType:    "github",
		ProviderKey:     "github",
		ProviderSubject: "  " + subject + "  ",
	})
	require.NoError(t, err)
	require.NotNil(t, lookup)
	require.Equal(t, user.ID, lookup.User.ID)
	require.Equal(t, result.Identity.ID, lookup.Identity.ID)
}

// 阶段 B 默认行为：新建渠道只落 channel_subject_hash/prefix，
// 渠道明文列保持 NULL，且去重/查询仍按 hash 工作。
func TestBindAuthIdentityToUserWritesChannelHashOnlyWhenDualWriteDisabled(t *testing.T) {
	disableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "channel-hash-only@example.com")

	const (
		providerSubject = "union-channel-hash-only-123456"
		channelSubject  = "openid-channel-hash-only-123456"
	)
	input := BindAuthIdentityInput{
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
	}
	result, err := repo.BindAuthIdentityToUser(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result.Channel)
	require.Nil(t, result.Channel.ChannelSubject, "channel plaintext must stay NULL by default")
	require.NotNil(t, result.Channel.ChannelSubjectHash)
	require.Equal(t, service.HashAuthChannelSubject("wechat", channelSubject), *result.Channel.ChannelSubjectHash)
	require.NotNil(t, result.Channel.ChannelSubjectPrefix)

	// 重复绑定同一渠道 subject：按 hash 去重，不新增行。
	second, err := repo.BindAuthIdentityToUser(ctx, input)
	require.NoError(t, err)
	require.Equal(t, result.Channel.ID, second.Channel.ID)

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

	// 数据库里确实没有渠道明文。
	stored, err := client.AuthIdentityChannel.Get(ctx, result.Channel.ID)
	require.NoError(t, err)
	require.Nil(t, stored.ChannelSubject)
}

// 阶段 B：更新 subject 时关闭双写必须显式清空明文列，
// 否则改绑/合并后的旧 subject 会继续留在库里。
func TestApplyAuthIdentitySubjectUpdateClearsPlaintextWhenDualWriteDisabled(t *testing.T) {
	enableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "update-clear@example.com")

	const oldSubject = "github-subject-before-update"
	created, err := repo.CreateAuthIdentity(ctx, CreateAuthIdentityInput{
		UserID: user.ID,
		Canonical: AuthIdentityKey{
			ProviderType:    "github",
			ProviderKey:     "github",
			ProviderSubject: oldSubject,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, created.Identity.ProviderSubject, "precondition: legacy row keeps plaintext")

	// 模拟阶段 B 默认配置。
	service.ConfigureAuthSubjectPlaintextDualWrite(false)

	const newSubject = "github-subject-after-update"
	update := client.AuthIdentity.UpdateOneID(created.Identity.ID)
	update = service.ApplyAuthIdentitySubjectUpdate(update, "github", newSubject)
	updated, err := update.Save(ctx)
	require.NoError(t, err)
	require.Nil(t, updated.ProviderSubject, "update must clear plaintext while dual write is disabled")
	require.NotNil(t, updated.ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("github", newSubject), *updated.ProviderSubjectHash)

	lookup, err := repo.GetUserByCanonicalIdentity(ctx, AuthIdentityKey{
		ProviderType:    "github",
		ProviderKey:     "github",
		ProviderSubject: newSubject,
	})
	require.NoError(t, err)
	require.Equal(t, user.ID, lookup.User.ID)
}

// 阶段 B：渠道 subject 更新同样要清空明文列。
func TestApplyAuthChannelSubjectUpdateClearsPlaintextWhenDualWriteDisabled(t *testing.T) {
	enableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	user := newAuthSubjectTestUser(t, repo, "channel-update-clear@example.com")

	const (
		providerSubject = "union-channel-update-123456"
		oldChannel      = "openid-channel-before-update"
	)
	created, err := repo.CreateAuthIdentity(ctx, CreateAuthIdentityInput{
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
			ChannelSubject: oldChannel,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, created.Channel.ChannelSubject, "precondition: legacy row keeps channel plaintext")

	service.ConfigureAuthSubjectPlaintextDualWrite(false)

	const newChannel = "openid-channel-after-update"
	update := client.AuthIdentityChannel.UpdateOneID(created.Channel.ID)
	update = service.ApplyAuthChannelSubjectUpdate(update, "wechat", newChannel)
	updated, err := update.Save(ctx)
	require.NoError(t, err)
	require.Nil(t, updated.ChannelSubject, "update must clear channel plaintext while dual write is disabled")
	require.NotNil(t, updated.ChannelSubjectHash)
	require.Equal(t, service.HashAuthChannelSubject("wechat", newChannel), *updated.ChannelSubjectHash)

	lookup, err := repo.GetUserByChannelIdentity(ctx, AuthIdentityChannelKey{
		ProviderType:   "wechat",
		ProviderKey:    "wechat-main",
		Channel:        "open",
		ChannelAppID:   "wx-app",
		ChannelSubject: newChannel,
	})
	require.NoError(t, err)
	require.Equal(t, user.ID, lookup.User.ID)
}

// 回归 #682：历史明文 email 身份行（provider_subject_hash IS NULL）在明文双写关闭时，
// 更新资料 / 重置密码调用 repo.Update 不得再插入第二条 (NULL, hash) 行触发 ent not singular，
// 而是原地回填 hash/prefix（并按双写关闭语义清理明文列）。
func TestUpdateUserBackfillsLegacyPlaintextEmailIdentityWithoutDuplicate(t *testing.T) {
	disableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()

	const email = "legacy-profile@example.com"
	createdUser, err := client.User.Create().
		SetEmail(email).
		SetUsername("legacy-before").
		SetPasswordHash("old-hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	legacyIdentity, err := client.AuthIdentity.Create().
		SetUserID(createdUser.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(email).
		SetMetadata(map[string]any{"source": "legacy_migration"}).
		Save(ctx)
	require.NoError(t, err)
	require.Nil(t, legacyIdentity.ProviderSubjectHash)

	// 1. 模拟更新资料（UpdateProfile -> repo.Update with Username）
	u := &service.User{
		ID:       createdUser.ID,
		Email:    email,
		Username: "legacy-after",
	}
	require.NoError(t, repo.Update(ctx, u, service.UserUpdateFields{Username: true}))

	identities, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			service.AuthIdentitySubjectLookupPredicate("email", email),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, identities, 1, "历史明文行必须原地回填，不得产生重复行")
	require.Equal(t, legacyIdentity.ID, identities[0].ID)
	require.Equal(t, createdUser.ID, identities[0].UserID)
	require.NotNil(t, identities[0].ProviderSubjectHash)
	expectedHash := service.HashAuthIdentitySubject("email", email)
	require.Equal(t, expectedHash, *identities[0].ProviderSubjectHash)
	require.NotNil(t, identities[0].ProviderSubjectPrefix)
	require.Equal(t, service.AuthSubjectPrefixFromHash(expectedHash), *identities[0].ProviderSubjectPrefix)
	require.Nil(t, identities[0].ProviderSubject, "双写关闭时回填应按 helper 约定清空明文列")

	// 2. 模拟重置密码（ResetPassword -> repo.Update with PasswordHash），幂等再次更新不报错。
	u.PasswordHash = "new-hash"
	require.NoError(t, repo.Update(ctx, u, service.UserUpdateFields{PasswordHash: true}))

	count, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			service.AuthIdentitySubjectLookupPredicate("email", email),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestUpdateUserBackfillsLegacyPlaintextEmailIdentityPreservesPlaintextWhenDualWriteEnabled(t *testing.T) {
	enableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()

	const email = "legacy-dualwrite@example.com"
	createdUser, err := client.User.Create().
		SetEmail(email).
		SetUsername("legacy-dw-before").
		SetPasswordHash("old-hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	legacyIdentity, err := client.AuthIdentity.Create().
		SetUserID(createdUser.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(email).
		SetMetadata(map[string]any{"source": "legacy_migration"}).
		Save(ctx)
	require.NoError(t, err)

	u := &service.User{
		ID:       createdUser.ID,
		Email:    email,
		Username: "legacy-dw-after",
	}
	require.NoError(t, repo.Update(ctx, u, service.UserUpdateFields{Username: true}))

	identities, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			service.AuthIdentitySubjectLookupPredicate("email", email),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, identities, 1)
	require.Equal(t, legacyIdentity.ID, identities[0].ID)
	require.NotNil(t, identities[0].ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("email", email), *identities[0].ProviderSubjectHash)
	require.NotNil(t, identities[0].ProviderSubject)
	require.Equal(t, email, *identities[0].ProviderSubject)
}

func TestEnsureEmailAuthIdentityRejectsOwnedByAnotherUserWithoutDuplicate(t *testing.T) {
	disableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()

	owner := newAuthSubjectTestUser(t, repo, "owner-placeholder@example.com")
	other := newAuthSubjectTestUser(t, repo, "other-placeholder@example.com")

	// 场景 1：他人的历史明文行（hash=NULL）
	const legacyEmail = "taken-legacy@example.com"
	legacyRow, err := client.AuthIdentity.Create().
		SetUserID(owner.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(legacyEmail).
		SetMetadata(map[string]any{"source": "legacy"}).
		Save(ctx)
	require.NoError(t, err)

	err = ensureEmailAuthIdentityWithClient(ctx, client, other.ID, legacyEmail, "user_repo_update")
	require.ErrorIs(t, err, service.ErrEmailExists)

	// 绝不能插入重复行，也不得修改原主人的记录。
	rows, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			service.AuthIdentitySubjectLookupPredicate("email", legacyEmail),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, legacyRow.ID, rows[0].ID)
	require.Equal(t, owner.ID, rows[0].UserID)
	require.Nil(t, rows[0].ProviderSubjectHash)

	// 场景 2：他人的 hash-only 行
	const hashedEmail = "taken-hashed@example.com"
	require.NoError(t, ensureEmailAuthIdentityWithClient(ctx, client, owner.ID, hashedEmail, "user_repo_create"))
	err = ensureEmailAuthIdentityWithClient(ctx, client, other.ID, hashedEmail, "user_repo_update")
	require.ErrorIs(t, err, service.ErrEmailExists)
}

func TestEnsureEmailAuthIdentityHandlesEmptyHashAndPreexistingDuplicatesForSameUser(t *testing.T) {
	disableAuthSubjectPlaintextDualWriteForTest(t)
	repo, client := newUserEntRepo(t)
	ctx := context.Background()

	user := newAuthSubjectTestUser(t, repo, "edge-owner@example.com")

	// 场景 1：provider_subject_hash 为空串 "" 的历史行
	const emptyHashEmail = "empty-hash@example.com"
	emptyHashRow, err := client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(emptyHashEmail).
		SetProviderSubjectHash("").
		SetMetadata(map[string]any{"source": "legacy"}).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, ensureEmailAuthIdentityWithClient(ctx, client, user.ID, emptyHashEmail, "user_repo_update"))
	reloaded, err := client.AuthIdentity.Get(ctx, emptyHashRow.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ProviderSubjectHash)
	require.Equal(t, service.HashAuthIdentitySubject("email", emptyHashEmail), *reloaded.ProviderSubjectHash)

	// 场景 2：历史异常已存在两条同用户行（一条明文 hash=NULL，一条 hash-only），不得因 not singular 报错。
	const dupEmail = "preexisting-dup@example.com"
	_, err = client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(dupEmail).
		SetMetadata(map[string]any{"source": "legacy"}).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubjectHash(service.HashAuthIdentitySubject("email", dupEmail)).
		SetMetadata(map[string]any{"source": "new"}).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, ensureEmailAuthIdentityWithClient(ctx, client, user.ID, dupEmail, "user_repo_update"))
}
