package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type EmailOAuthIdentityInput struct {
	ProviderType     string
	ProviderKey      string
	ProviderSubject  string
	Email            string
	EmailVerified    bool
	Username         string
	DisplayName      string
	AvatarURL        string
	UpstreamMetadata map[string]any
}

func (s *AuthService) LoginOrRegisterVerifiedEmailOAuth(ctx context.Context, input EmailOAuthIdentityInput) (*TokenPair, *User, error) {
	return s.loginOrRegisterVerifiedEmailOAuth(ctx, input, "", "", "")
}

// loginRequiredFactors 返回已有账号登录必须完成的全部二次验证因子（TOTP / Passkey / 两者）。
// 唯一真相源为 TotpService.LoginRequiredFactors；策略读取失败一律 fail-closed。
func (s *AuthService) loginRequiredFactors(ctx context.Context, user *User) ([]Factor, error) {
	if s == nil || user == nil {
		return nil, ErrTwoFactorPolicyUnavailable
	}
	if s.loginTwoFactorPolicy != nil {
		factors, err := s.loginTwoFactorPolicy.LoginRequiredFactors(ctx, user)
		if err != nil {
			return nil, ErrTwoFactorPolicyUnavailable
		}
		return factors, nil
	}
	if s.settingService == nil {
		if user.TotpEnabled {
			return []Factor{FactorTOTP}, nil
		}
		return nil, nil
	}
	if s.settingService.LoginRequiresTOTP(ctx, user) {
		return []Factor{FactorTOTP}, nil
	}
	return nil, nil
}

func (s *AuthService) LoginOrRegisterVerifiedEmailOAuthWithInvitation(
	ctx context.Context,
	input EmailOAuthIdentityInput,
	invitationCode string,
	affiliateCode string,
) (*TokenPair, *User, error) {
	return s.loginOrRegisterVerifiedEmailOAuth(ctx, input, invitationCode, affiliateCode, "")
}

func (s *AuthService) LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(
	ctx context.Context,
	input EmailOAuthIdentityInput,
	invitationCode string,
	affiliateCode string,
	promoCode string,
) (*TokenPair, *User, error) {
	return s.loginOrRegisterVerifiedEmailOAuth(ctx, input, invitationCode, affiliateCode, promoCode)
}

func (s *AuthService) loginOrRegisterVerifiedEmailOAuth(
	ctx context.Context,
	input EmailOAuthIdentityInput,
	invitationCode string,
	affiliateCode string,
	promoCode string,
) (*TokenPair, *User, error) {
	if s == nil || s.userRepo == nil || s.entClient == nil {
		return nil, nil, ErrServiceUnavailable
	}

	providerType := normalizeOAuthSignupSource(input.ProviderType)
	if providerType != "github" && providerType != "google" && providerType != "oidc" {
		return nil, nil, infraerrors.BadRequest("OAUTH_PROVIDER_INVALID", "oauth provider is invalid")
	}
	providerKey := strings.TrimSpace(input.ProviderKey)
	if providerKey == "" {
		providerKey = providerType
	}
	providerSubject := strings.TrimSpace(input.ProviderSubject)
	if providerSubject == "" {
		return nil, nil, infraerrors.BadRequest("OAUTH_SUBJECT_MISSING", "oauth subject is missing")
	}
	if !input.EmailVerified {
		return nil, nil, infraerrors.Forbidden("OAUTH_EMAIL_NOT_VERIFIED", "oauth email is not verified")
	}

	email := strings.TrimSpace(strings.ToLower(input.Email))
	if email == "" || len(email) > 255 {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if isReservedEmail(email) {
		return nil, nil, ErrEmailReserved
	}
	// 精确黑名单只拦新建账号（见 createEmailOAuthUser），已有账号能否登录由账号状态决定。
	if err := s.validateRegistrationEmailSuffixPolicy(ctx, email); err != nil {
		return nil, nil, err
	}

	identityUser, err := s.findEmailOAuthIdentityOwner(ctx, providerType, providerKey, providerSubject)
	if err != nil {
		return nil, nil, err
	}
	// 已绑定的第三方身份以「绑定关系」为准：身份绑定到哪个用户，就登录哪个用户。
	// 这里不能再拿用户当前的账户邮箱去比对 provider 返回的邮箱——用户有权修改自己的
	// 账户邮箱（例如管理员改邮箱），那样会把本人锁在登录之外。
	// provider 侧邮箱变更（换邮箱/邮箱被改）只记录漂移，不影响登录。
	if identityUser != nil {
		boundEmail := s.lookupOAuthIdentityBoundEmail(ctx, providerType, providerKey, providerSubject)
		if boundEmail != "" && !strings.EqualFold(boundEmail, email) {
			logger.LegacyPrintf("service.auth",
				"[Auth] oauth identity email drift: provider=%s user_id=%d bound_email_differs=true (binding wins)",
				providerType, identityUser.ID)
		}
	}

	user := identityUser
	created := false
	if user == nil {
		user, err = s.userRepo.GetByEmail(ctx, email)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				user, err = s.createEmailOAuthUser(ctx, email, input.Username, providerType, invitationCode, affiliateCode)
				if err != nil {
					return nil, nil, err
				}
				created = true
			} else {
				logger.LegacyPrintf("service.auth", "[Auth] Database error during %s oauth login: %v", providerType, err)
				return nil, nil, ErrServiceUnavailable
			}
		}
	}

	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}
	// 登录二次验证必须在任何写库操作（绑定 auth_identities、修改用户名、发放首次绑定默认赠额）之前判定：
	// 已有账号只要绑定了任一登录 2FA 因子（TOTP / Passkey / 两者），立即返回 ErrLoginTOTPRequired，
	// 由 handler 转入 pending 2FA 登录会话，仅在全部因子通过后才真正落库绑定与签发会话。
	if !created {
		factors, factorErr := s.loginRequiredFactors(ctx, user)
		if factorErr != nil {
			return nil, nil, factorErr
		}
		if len(factors) > 0 {
			return nil, user, ErrLoginTOTPRequired
		}
	}
	if err := s.ensureEmailOAuthIdentity(ctx, user.ID, EmailOAuthIdentityInput{
		ProviderType:     providerType,
		ProviderKey:      providerKey,
		ProviderSubject:  providerSubject,
		Email:            email,
		EmailVerified:    input.EmailVerified,
		Username:         input.Username,
		DisplayName:      input.DisplayName,
		AvatarURL:        input.AvatarURL,
		UpstreamMetadata: input.UpstreamMetadata,
	}); err != nil {
		return nil, nil, err
	}

	if sanitizedUsername := SanitizeUsername(input.Username); user.Username == "" && sanitizedUsername != "" {
		user.Username = sanitizedUsername
		if err := s.userRepo.Update(ctx, user, UserUpdateFields{Username: true}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to update username after %s oauth login: %v", providerType, err)
		}
	}
	if !created {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, user.ID, providerType); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to apply %s first bind defaults: %v", providerType, err)
		}
	} else {
		user = s.applyOAuthSignupPromoCode(ctx, user, promoCode)
	}
	s.RecordSuccessfulLogin(ctx, user.ID)

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

func (s *AuthService) createEmailOAuthUser(ctx context.Context, email, username, providerType, invitationCode, affiliateCode string) (*User, error) {
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		return nil, ErrRegDisabled
	}
	// 与邮箱注册同一套策略：精确黑名单、别名去重/禁止、邮箱域名白名单与域名限量。
	// 此前这里只查精确黑名单，第三方 OAuth（GitHub/Google/OIDC 已验证邮箱）可以
	// 绕过 registration_email_alias_block_enabled 与域名限量批量注册。
	if err := s.validateRegistrationEmailQuota(ctx, email); err != nil {
		return nil, err
	}
	invitationRedeemCode, err := s.validateOAuthRegistrationInvitation(ctx, invitationCode)
	if err != nil {
		if errors.Is(err, ErrInvitationCodeRequired) {
			return nil, ErrOAuthInvitationRequired
		}
		return nil, err
	}

	randomPassword, err := randomHexString(32)
	if err != nil {
		return nil, ErrServiceUnavailable
	}
	hashedPassword, err := s.HashPassword(randomPassword)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	grantPlan := s.resolveSignupGrantPlan(ctx, providerType)
	var defaultRPMLimit int
	if s.settingService != nil {
		defaultRPMLimit = s.settingService.GetDefaultUserRPMLimit(ctx)
	}
	user := &User{
		Email:        email,
		Username:     SanitizeUsername(username),
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		RPMLimit:     defaultRPMLimit,
		Status:       StatusActive,
		SignupSource: providerType,
	}
	if err := s.createOAuthRegisteredUser(ctx, user); err != nil {
		if errors.Is(err, ErrEmailExists) {
			existing, loadErr := s.userRepo.GetByEmail(ctx, email)
			if loadErr != nil {
				return nil, ErrServiceUnavailable
			}
			return existing, nil
		}
		return nil, ErrServiceUnavailable
	}
	s.postAuthUserBootstrap(ctx, user, providerType, false)
	s.assignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
	// snapshot user × platform quota（fail-open）
	_ = s.snapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
	s.bindOAuthAffiliate(ctx, user.ID, affiliateCode)
	if invitationRedeemCode != nil {
		if err := s.useOAuthRegistrationInvitation(ctx, invitationRedeemCode.ID, user.ID); err != nil {
			_ = s.RollbackOAuthEmailAccountCreation(ctx, user.ID, invitationCode)
			return nil, ErrInvitationCodeInvalid
		}
	}
	return user, nil
}

func (s *AuthService) findEmailOAuthIdentityOwner(ctx context.Context, providerType, providerKey, providerSubject string) (*User, error) {
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyEQ(providerKey),
			AuthIdentitySubjectLookupPredicate(providerType, providerSubject),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	user, err := s.userRepo.GetByID(ctx, identity.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, nil
		}
		return nil, ErrServiceUnavailable
	}
	return user, nil
}

// lookupOAuthIdentityBoundEmail 读取已绑定身份在绑定/上次登录时记录的 provider 邮箱。
// 仅用于记录「provider 邮箱已变更」的漂移日志，不参与登录放行判断。
func (s *AuthService) lookupOAuthIdentityBoundEmail(ctx context.Context, providerType, providerKey, providerSubject string) string {
	if s == nil || s.entClient == nil {
		return ""
	}
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyEQ(providerKey),
			authidentity.ProviderSubjectEQ(providerSubject),
		).
		Only(ctx)
	if err != nil || identity == nil {
		return ""
	}
	if email, ok := identity.Metadata["email"].(string); ok {
		return strings.TrimSpace(strings.ToLower(email))
	}
	return ""
}

func (s *AuthService) ensureEmailOAuthIdentity(ctx context.Context, userID int64, input EmailOAuthIdentityInput) error {
	metadata := map[string]any{
		"email":          strings.TrimSpace(strings.ToLower(input.Email)),
		"email_verified": input.EmailVerified,
	}
	for key, value := range input.UpstreamMetadata {
		metadata[key] = value
	}
	if strings.TrimSpace(input.Username) != "" {
		metadata["username"] = strings.TrimSpace(input.Username)
	}
	if strings.TrimSpace(input.DisplayName) != "" {
		metadata["display_name"] = strings.TrimSpace(input.DisplayName)
	}
	if strings.TrimSpace(input.AvatarURL) != "" {
		metadata["avatar_url"] = strings.TrimSpace(input.AvatarURL)
	}

	providerType := normalizeOAuthSignupSource(input.ProviderType)
	providerKey := strings.TrimSpace(input.ProviderKey)
	providerSubject := strings.TrimSpace(input.ProviderSubject)
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(providerType),
			authidentity.ProviderKeyEQ(providerKey),
			AuthIdentitySubjectLookupPredicate(providerType, providerSubject),
		).
		Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	if identity != nil {
		if identity.UserID != userID {
			return infraerrors.Conflict("AUTH_IDENTITY_OWNERSHIP_CONFLICT", "auth identity already belongs to another user")
		}
		_, err = s.entClient.AuthIdentity.UpdateOneID(identity.ID).
			SetMetadata(SanitizeAuthIdentityMetadata(metadata)).
			Save(ctx)
		return err
	}
	create := s.entClient.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType(providerType).
		SetProviderKey(providerKey).
		SetMetadata(SanitizeAuthIdentityMetadata(metadata))
	create = ApplyAuthIdentitySubjectCreate(create, providerType, providerSubject)
	_, err = create.Save(ctx)
	return err
}
