package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// BindEmailIdentity verifies and binds a local email/password identity to the
// current user, or replaces the existing bound primary email.
func (s *AuthService) BindEmailIdentity(
	ctx context.Context,
	userID int64,
	email string,
	verifyCode string,
	password string,
) (*User, error) {
	if s == nil {
		return nil, ErrServiceUnavailable
	}

	normalizedEmail, err := normalizeEmailForIdentityBinding(email)
	if err != nil {
		return nil, err
	}
	if isReservedEmail(normalizedEmail) {
		return nil, ErrEmailReserved
	}
	if strings.TrimSpace(password) == "" {
		return nil, ErrPasswordRequired
	}
	if err := s.VerifyOAuthEmailCode(ctx, normalizedEmail, verifyCode); err != nil {
		return nil, err
	}
	if err := s.validateRegistrationEmailPolicy(ctx, normalizedEmail); err != nil {
		return nil, err
	}

	currentUser, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	firstRealEmailBind := !hasBindableEmailIdentitySubject(currentUser.Email)
	if firstRealEmailBind && len(password) < 6 {
		return nil, infraerrors.BadRequest("PASSWORD_TOO_SHORT", "password must be at least 6 characters")
	}
	if !firstRealEmailBind && !s.CheckPassword(password, currentUser.PasswordHash) {
		return nil, ErrPasswordIncorrect
	}

	if err := s.ensureEmailIdentityAvailableForUser(ctx, currentUser, normalizedEmail); err != nil {
		return nil, err
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	if s.entClient != nil {
		if err := s.updateBoundEmailIdentityTx(ctx, currentUser, normalizedEmail, hashedPassword, firstRealEmailBind); err != nil {
			return nil, err
		}
		s.revokeEmailIdentitySessions(ctx, userID)
		return currentUser, nil
	}

	currentUser.Email = normalizedEmail
	currentUser.PasswordHash = hashedPassword
	if err := s.userRepo.Update(ctx, currentUser, UserUpdateFields{Email: true, PasswordHash: true}); err != nil {
		if errors.Is(err, ErrEmailExists) {
			return nil, ErrEmailExists
		}
		return nil, ErrServiceUnavailable
	}

	if firstRealEmailBind {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, userID, "email"); err != nil {
			return nil, fmt.Errorf("apply email first bind defaults: %w", err)
		}
	}

	s.revokeEmailIdentitySessions(ctx, userID)
	return currentUser, nil
}

// SendEmailIdentityBindCode sends a verification code for authenticated email binding flows.
func (s *AuthService) SendEmailIdentityBindCode(ctx context.Context, userID int64, email string, locale ...string) error {
	if s == nil {
		return ErrServiceUnavailable
	}

	normalizedEmail, err := normalizeEmailForIdentityBinding(email)
	if err != nil {
		return err
	}
	if isReservedEmail(normalizedEmail) {
		return ErrEmailReserved
	}
	if err := s.validateRegistrationEmailPolicy(ctx, normalizedEmail); err != nil {
		return err
	}
	if s.emailService == nil {
		return ErrServiceUnavailable
	}
	currentUser, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrUserNotFound
		}
		return ErrServiceUnavailable
	}

	if err := s.ensureEmailIdentityAvailableForUser(ctx, currentUser, normalizedEmail); err != nil {
		return err
	}

	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}
	return s.emailService.SendVerifyCode(ctx, normalizedEmail, siteName, firstEmailLocale(locale))
}

// ensureEmailIdentityAvailableForUser 在发码 / 提交换绑前做快速查重。
// 精确地址或 provider alias 若已指向其他用户的收件箱则直接拒绝；
// 当前用户自己的收件箱允许继续，便于其更换自身的 alias 变体。
func (s *AuthService) ensureEmailIdentityAvailableForUser(
	ctx context.Context,
	currentUser *User,
	email string,
) error {
	if currentUser == nil {
		return ErrUserNotFound
	}

	existingUser, err := s.userRepo.GetByEmail(ctx, email)
	switch {
	case err == nil:
		if existingUser == nil || existingUser.ID == currentUser.ID {
			break
		}
		return ErrEmailExists
	case errors.Is(err, ErrUserNotFound):
		// Continue to alias lookup below.
	default:
		return ErrServiceUnavailable
	}

	if NormalizeEmailForAliasDedup(currentUser.Email) == NormalizeEmailForAliasDedup(email) {
		return nil
	}

	aliasExists, err := s.userRepo.ExistsByEmailAlias(ctx, email)
	if err != nil {
		return ErrServiceUnavailable
	}
	if aliasExists {
		return ErrEmailExists
	}
	return nil
}

func normalizeEmailForIdentityBinding(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || len(normalized) > 255 {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(normalized); err != nil {
		return "", infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	return normalized, nil
}

func hasBindableEmailIdentitySubject(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return normalized != "" && !isReservedEmail(normalized)
}

// emailIdentityAliasGuardRepository 是主邮箱替换所需的事务内原子仓储能力，
// 用于关闭服务层前置查重与实际写入之间的并发窗口。
type emailIdentityAliasGuardRepository interface {
	UpdateEmailWithAliasGuard(ctx context.Context, userID int64, email string, passwordHash string) error
}

func (s *AuthService) updateBoundEmailIdentityTx(
	ctx context.Context,
	currentUser *User,
	email string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return s.updateBoundEmailIdentityWithClient(ctx, tx.Client(), currentUser, email, hashedPassword, applyFirstBindDefaults)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return ErrServiceUnavailable
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := s.updateBoundEmailIdentityWithClient(txCtx, tx.Client(), currentUser, email, hashedPassword, applyFirstBindDefaults); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return ErrServiceUnavailable
	}
	return nil
}

func (s *AuthService) updateBoundEmailIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	currentUser *User,
	email string,
	hashedPassword string,
	applyFirstBindDefaults bool,
) error {
	if client == nil || currentUser == nil || currentUser.ID <= 0 {
		return ErrServiceUnavailable
	}

	guard, ok := s.userRepo.(emailIdentityAliasGuardRepository)
	if !ok {
		return ErrServiceUnavailable
	}
	if err := guard.UpdateEmailWithAliasGuard(ctx, currentUser.ID, email, hashedPassword); err != nil {
		return err
	}

	oldEmail := currentUser.Email

	if err := replaceBoundEmailAuthIdentityWithClient(ctx, client, currentUser.ID, oldEmail, email, "auth_service_email_bind"); err != nil {
		if errors.Is(err, ErrEmailExists) {
			return ErrEmailExists
		}
		return ErrServiceUnavailable
	}

	if applyFirstBindDefaults {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, currentUser.ID, "email"); err != nil {
			return fmt.Errorf("apply email first bind defaults: %w", err)
		}
	}

	updatedUser, err := client.User.Get(ctx, currentUser.ID)
	if err != nil {
		return ErrServiceUnavailable
	}
	currentUser.Email = updatedUser.Email
	currentUser.PasswordHash = updatedUser.PasswordHash
	currentUser.Balance = updatedUser.Balance
	currentUser.Concurrency = updatedUser.Concurrency
	currentUser.UpdatedAt = updatedUser.UpdatedAt
	return nil
}

func (s *AuthService) revokeEmailIdentitySessions(ctx context.Context, userID int64) {
	if err := s.RevokeAllUserSessions(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke refresh sessions after email identity bind for user %d: %v", userID, err)
	}
}

func replaceBoundEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	oldEmail string,
	newEmail string,
	source string,
) error {
	newSubject := normalizeBoundEmailAuthIdentitySubject(newEmail)
	if err := ensureBoundEmailAuthIdentityWithClient(ctx, client, userID, newSubject, source); err != nil {
		return err
	}

	oldSubject := normalizeBoundEmailAuthIdentitySubject(oldEmail)
	if oldSubject == "" || oldSubject == newSubject {
		return nil
	}

	_, err := client.AuthIdentity.Delete().
		Where(
			authidentity.UserIDEQ(userID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			AuthIdentitySubjectLookupPredicate("email", oldSubject),
		).
		Exec(ctx)
	return err
}

// EnsureEmailAuthIdentityWithClient 确保 (email, email, subject) 身份行存在且归属 userID。
// 先按 hash/明文回退条件查询已有行：
//   - 任一匹配行归属其他用户时返回 ErrEmailExists；
//   - 同用户的历史明文行（provider_subject_hash 为空/NULL）原地 UPDATE 回填盲索引，
//     避免明文双写关闭时再插入一条 (NULL, hash) 行导致 ent: auth_identity not singular；
//   - 不存在匹配行时才执行 INSERT ... ON CONFLICT (hash) DO NOTHING，并复查并发写入后的归属与回填。
func EnsureEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	subject string,
	source string,
) error {
	if client == nil || userID <= 0 {
		return nil
	}
	subject = strings.ToLower(strings.TrimSpace(subject))
	if subject == "" {
		return nil
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "auth_service_email_bind"
	}

	queryIdentities := func() ([]*dbent.AuthIdentity, error) {
		return client.AuthIdentity.Query().
			Where(
				authidentity.ProviderTypeEQ("email"),
				authidentity.ProviderKeyEQ("email"),
				AuthIdentitySubjectLookupPredicate("email", subject),
			).
			All(ctx)
	}

	identities, err := queryIdentities()
	if err != nil {
		return err
	}
	if len(identities) > 0 {
		return reconcileExistingEmailAuthIdentities(ctx, client, userID, subject, identities)
	}

	create := client.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType("email").
		SetProviderKey("email").
		SetVerifiedAt(time.Now().UTC()).
		SetMetadata(map[string]any{"source": source})
	create = ApplyAuthIdentitySubjectCreate(create, "email", subject)
	if err := create.
		OnConflictColumns(AuthIdentitySubjectHashConflictColumns()...).
		DoNothing().
		Exec(ctx); err != nil {
		// 旧实例在滚动窗口写入的明文行没有 hash：明文唯一索引冲突不视为失败，
		// 交给下面的查询走 hash/明文回退并复查归属。
		if !isSQLNoRowsError(err) && !dbent.IsConstraintError(err) {
			return err
		}
	}

	identities, err = queryIdentities()
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		return nil
	}
	return reconcileExistingEmailAuthIdentities(ctx, client, userID, subject, identities)
}

func reconcileExistingEmailAuthIdentities(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	subject string,
	identities []*dbent.AuthIdentity,
) error {
	for _, identity := range identities {
		if identity.UserID != userID {
			return ErrEmailExists
		}
	}

	expectedHash, expectedPrefix, _ := AuthSubjectColumns("email", subject, HashAuthIdentitySubject)
	for _, identity := range identities {
		if identity.ProviderSubjectHash != nil && strings.TrimSpace(*identity.ProviderSubjectHash) == expectedHash {
			if identity.ProviderSubjectPrefix == nil || strings.TrimSpace(*identity.ProviderSubjectPrefix) == "" {
				if _, err := client.AuthIdentity.UpdateOneID(identity.ID).
					SetProviderSubjectPrefix(expectedPrefix).
					Save(ctx); err != nil {
					return err
				}
			}
			return nil
		}
	}

	// 历史明文行缺少 provider_subject_hash（或与当前盲索引不一致）：原地回填，不新增重复行。
	target := identities[0]
	update := client.AuthIdentity.UpdateOneID(target.ID)
	update = ApplyAuthIdentitySubjectUpdate(update, "email", subject)
	if _, err := update.Save(ctx); err != nil {
		return err
	}
	return nil
}

func ensureBoundEmailAuthIdentityWithClient(
	ctx context.Context,
	client *dbent.Client,
	userID int64,
	subject string,
	source string,
) error {
	return EnsureEmailAuthIdentityWithClient(ctx, client, userID, subject, source)
}

func normalizeBoundEmailAuthIdentitySubject(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || isReservedEmail(normalized) {
		return ""
	}
	return normalized
}
