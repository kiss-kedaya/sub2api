package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	passkeySessionRegistration = "registration"
	passkeySessionLogin        = "login"
	passkeySessionTTL          = 5 * time.Minute
	defaultPasskeyName         = "Passkey"
	maxPasskeyNameLength       = 100
)

// WebAuthn 错误码按「能不能给用户一个可执行的解释」拆分：
//   - PASSKEY_CHALLENGE_EXPIRED：Redis 里没有这次 ceremony 的会话（过期 / 已用过 / 落到
//     了不共享 session 存储的实例），用户重新点一次即可；
//   - PASSKEY_CHALLENGE_MISMATCH：响应里的 challenge 与会话记录不一致；
//   - PASSKEY_ORIGIN_MISMATCH / PASSKEY_RP_ID_MISMATCH：站点域名或 RP ID 与
//     webauthn.rp_id / webauthn.rp_origins 配置不一致（部署问题，需要运维处理）；
//   - PASSKEY_VERIFICATION_FAILED：其余签名 / 认证器数据校验失败。
//
// 这些错误一律 400（请求里的 ceremony 数据无效），刻意不用 401：前端 api client 把
// 401 当作「会话过期」，会先刷新 token、重放请求，甚至清空登录态跳登录页。
// challenge 是一次性的，重放必然失败，401 只会把「认证器/站点配置问题」变成
// 「被踢下线」，用户看不到真正原因。
var (
	ErrPasskeysDisabled = infraerrors.Forbidden("PASSKEY_DISABLED", "passkey authentication is not enabled")
	ErrPasskeyNotFound  = infraerrors.NotFound("PASSKEY_NOT_FOUND", "passkey not found")
	ErrPasskeyExists    = infraerrors.Conflict("PASSKEY_ALREADY_EXISTS", "this passkey is already registered")
	// ErrPasskeySession 表示 session_token 格式非法、流程类型或用户与 JWT 不匹配。
	ErrPasskeySession = infraerrors.BadRequest("PASSKEY_SESSION_INVALID", "passkey session is invalid")
	// ErrPasskeySessionExpired 表示这次 ceremony 的临时会话已过期或已经被消费（一次性）。
	ErrPasskeySessionExpired = infraerrors.BadRequest(
		"PASSKEY_CHALLENGE_EXPIRED", "passkey challenge has expired or was already used; please try again")
	// ErrPasskeyChallengeMismatch 表示认证器响应的 challenge 与会话记录不一致。
	ErrPasskeyChallengeMismatch = infraerrors.BadRequest(
		"PASSKEY_CHALLENGE_MISMATCH", "passkey challenge does not match the pending registration")
	// ErrPasskeyOriginMismatch 表示浏览器上报的 origin 不在 webauthn.rp_origins 里。
	ErrPasskeyOriginMismatch = infraerrors.BadRequest(
		"PASSKEY_ORIGIN_MISMATCH", "this site origin is not allowed by the WebAuthn relying party configuration")
	// ErrPasskeyRPIDMismatch 表示认证器绑定的 RP ID 与 webauthn.rp_id 不一致。
	ErrPasskeyRPIDMismatch = infraerrors.BadRequest(
		"PASSKEY_RP_ID_MISMATCH", "this passkey is bound to a different relying party ID")
	// ErrPasskeyVerify 是其余 WebAuthn 校验失败（签名、认证器数据等）的兜底错误。
	ErrPasskeyVerify = infraerrors.BadRequest("PASSKEY_VERIFICATION_FAILED", "passkey verification failed")
)

// PasskeyCredentialRecord is the persistence representation used by the
// WebAuthn service. Credential contains the complete WebAuthn credential record
// so future library versions can continue to validate and update it.
type PasskeyCredentialRecord struct {
	ID         int64
	UserID     int64
	UserHandle []byte
	Name       string
	Credential webauthn.Credential
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type PasskeyRepository interface {
	EnsureUserHandle(ctx context.Context, userID int64, candidate []byte) ([]byte, error)
	GetUserHandle(ctx context.Context, userID int64) ([]byte, error)
	GetByCredentialID(ctx context.Context, credentialID []byte) (*PasskeyCredentialRecord, error)
	ListByUserID(ctx context.Context, userID int64) ([]PasskeyCredentialRecord, error)
	Create(ctx context.Context, record *PasskeyCredentialRecord) (*PasskeyCredentialRecord, error)
	UpdateCredential(ctx context.Context, userID int64, credential *webauthn.Credential, usedAt time.Time) error
	Rename(ctx context.Context, userID, credentialID int64, name string) error
	Delete(ctx context.Context, userID, credentialID int64) error
}

type PasskeySession struct {
	Kind     string               `json:"kind"`
	UserID   int64                `json:"user_id,omitempty"`
	WebAuthn webauthn.SessionData `json:"webauthn"`
}

type PasskeySessionStore interface {
	Store(ctx context.Context, session *PasskeySession, ttl time.Duration) (string, error)
	Consume(ctx context.Context, token string) (*PasskeySession, error)
}

type PasskeyCredentialSummary struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Backup     bool       `json:"backup"`
}

type passkeyUser struct {
	account     *User
	handle      []byte
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte {
	return u.handle
}

func (u *passkeyUser) WebAuthnName() string {
	return u.account.Email
}

func (u *passkeyUser) WebAuthnDisplayName() string {
	if name := strings.TrimSpace(u.account.Username); name != "" {
		return name
	}
	return u.account.Email
}

func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

type PasskeyService struct {
	enabled  bool
	webAuthn *webauthn.WebAuthn
	repo     PasskeyRepository
	sessions PasskeySessionStore
	userRepo UserRepository
}

func NewPasskeyService(
	cfg *config.Config,
	repo PasskeyRepository,
	sessions PasskeySessionStore,
	userRepo UserRepository,
) (*PasskeyService, error) {
	s := &PasskeyService{
		repo:     repo,
		sessions: sessions,
		userRepo: userRepo,
	}
	if cfg == nil || !cfg.WebAuthn.Enabled {
		return s, nil
	}

	instance, err := webauthn.New(&webauthn.Config{
		RPDisplayName: cfg.WebAuthn.RPDisplayName,
		RPID:          cfg.WebAuthn.RPID,
		RPOrigins:     cfg.WebAuthn.RPOrigins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize WebAuthn: %w", err)
	}
	s.enabled = true
	s.webAuthn = instance
	return s, nil
}

func (s *PasskeyService) Enabled() bool {
	return s != nil && s.enabled && s.webAuthn != nil
}

func (s *PasskeyService) requireEnabled() error {
	if !s.Enabled() {
		return ErrPasskeysDisabled
	}
	return nil
}

// verifyPasskeyPassword gates credential enrollment and revocation with the
// account password so a hijacked session cannot silently add or remove
// passkeys. The password is used instead of TOTP step-up so the guard also
// works on deployments without a TOTP encryption key configured.
func verifyPasskeyPassword(user *User, password string) error {
	if password == "" {
		return ErrPasswordRequired
	}
	if user == nil || !user.CheckPassword(password) {
		return ErrPasswordIncorrect
	}
	return nil
}

// classifyPasskeyVerificationError 把 go-webauthn 的协议错误映射成可区分的机器码，
// 让前端能提示「challenge 过期 / 站点域名不匹配 / 校验失败」，也让运维仅凭 400 响应
// 里的 reason 就能判断是部署配置问题（origin/RP ID）还是认证器响应问题。
// 原始错误只写 warn 日志：里面只有 challenge/RP 哈希等一次性材料，不含凭据。
func classifyPasskeyVerificationError(flow string, userID int64, err error) error {
	if err == nil {
		return nil
	}
	slog.Warn("passkey verification failed", "flow", flow, "user_id", userID, "error", err)

	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) {
		detail := strings.ToLower(protocolErr.Details + " " + protocolErr.DevInfo)
		switch {
		case strings.Contains(detail, "origin"):
			return ErrPasskeyOriginMismatch
		case strings.Contains(detail, "rp hash mismatch"), strings.Contains(detail, "rpidhash"):
			return ErrPasskeyRPIDMismatch
		case strings.Contains(detail, "session has expired"):
			return ErrPasskeySessionExpired
		case strings.Contains(detail, "challenge"):
			return ErrPasskeyChallengeMismatch
		}
	}
	return ErrPasskeyVerify
}

func (s *PasskeyService) BeginRegistration(
	ctx context.Context,
	userID int64,
	password string,
) (creation *protocol.CredentialCreation, sessionToken string, err error) {
	if err = s.requireEnabled(); err != nil {
		return nil, "", err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if !user.IsActive() {
		return nil, "", ErrUserNotActive
	}
	if err = verifyPasskeyPassword(user, password); err != nil {
		return nil, "", err
	}

	candidate := make([]byte, 32)
	if _, err = rand.Read(candidate); err != nil {
		return nil, "", fmt.Errorf("generate passkey user handle: %w", err)
	}
	handle, err := s.repo.EnsureUserHandle(ctx, userID, candidate)
	if err != nil {
		return nil, "", err
	}
	waUser, err := s.loadWebAuthnUser(ctx, user, handle)
	if err != nil {
		return nil, "", err
	}

	creation, session, err := s.webAuthn.BeginRegistration(
		waUser,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(waUser.credentials).CredentialDescriptors()),
		webauthn.WithExtensions(protocol.AuthenticationExtensions{"credProps": true}),
	)
	if err != nil {
		return nil, "", fmt.Errorf("begin passkey registration: %w", err)
	}
	sessionToken, err = s.sessions.Store(ctx, &PasskeySession{
		Kind:     passkeySessionRegistration,
		UserID:   userID,
		WebAuthn: *session,
	}, passkeySessionTTL)
	if err != nil {
		return nil, "", err
	}
	return creation, sessionToken, nil
}

func (s *PasskeyService) FinishRegistration(
	ctx context.Context,
	userID int64,
	sessionToken, name string,
	request *http.Request,
) (*PasskeyCredentialSummary, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	session, err := s.sessions.Consume(ctx, sessionToken)
	if err != nil {
		return nil, err
	}
	if session == nil || session.Kind != passkeySessionRegistration || session.UserID != userID {
		return nil, ErrPasskeySession
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	handle, err := s.repo.GetUserHandle(ctx, userID)
	if err != nil {
		return nil, err
	}
	waUser, err := s.loadWebAuthnUser(ctx, user, handle)
	if err != nil {
		return nil, err
	}
	credential, err := s.webAuthn.FinishRegistration(waUser, session.WebAuthn, request)
	if err != nil {
		return nil, classifyPasskeyVerificationError(passkeySessionRegistration, userID, err)
	}

	record, err := s.repo.Create(ctx, &PasskeyCredentialRecord{
		UserID:     userID,
		UserHandle: handle,
		Name:       normalizePasskeyName(name),
		Credential: *credential,
	})
	if err != nil {
		return nil, err
	}
	return passkeySummary(record), nil
}

func (s *PasskeyService) BeginLogin(
	ctx context.Context,
) (assertion *protocol.CredentialAssertion, sessionToken string, err error) {
	if err = s.requireEnabled(); err != nil {
		return nil, "", err
	}
	assertion, session, err := s.webAuthn.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", fmt.Errorf("begin passkey login: %w", err)
	}
	sessionToken, err = s.sessions.Store(ctx, &PasskeySession{
		Kind:     passkeySessionLogin,
		WebAuthn: *session,
	}, passkeySessionTTL)
	if err != nil {
		return nil, "", err
	}
	return assertion, sessionToken, nil
}

func (s *PasskeyService) FinishLogin(
	ctx context.Context,
	sessionToken string,
	request *http.Request,
) (*User, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	session, err := s.sessions.Consume(ctx, sessionToken)
	if err != nil {
		return nil, err
	}
	if session == nil || session.Kind != passkeySessionLogin {
		return nil, ErrPasskeySession
	}

	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		record, lookupErr := s.repo.GetByCredentialID(ctx, rawID)
		if lookupErr != nil || record == nil || !bytes.Equal(record.UserHandle, userHandle) {
			return nil, ErrPasskeyVerify
		}
		account, lookupErr := s.userRepo.GetByID(ctx, record.UserID)
		if lookupErr != nil || account == nil || !account.IsActive() {
			return nil, ErrPasskeyVerify
		}
		return s.loadWebAuthnUser(ctx, account, record.UserHandle)
	}

	validatedUser, credential, err := s.webAuthn.FinishPasskeyLogin(handler, session.WebAuthn, request)
	if err != nil {
		return nil, classifyPasskeyVerificationError(passkeySessionLogin, 0, err)
	}
	waUser, ok := validatedUser.(*passkeyUser)
	if !ok || waUser.account == nil {
		return nil, ErrPasskeyVerify
	}
	if err = s.repo.UpdateCredential(ctx, waUser.account.ID, credential, time.Now().UTC()); err != nil {
		return nil, err
	}
	return waUser.account, nil
}

func (s *PasskeyService) List(ctx context.Context, userID int64) ([]PasskeyCredentialSummary, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	records, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]PasskeyCredentialSummary, 0, len(records))
	for i := range records {
		result = append(result, *passkeySummary(&records[i]))
	}
	return result, nil
}

func (s *PasskeyService) Rename(ctx context.Context, userID, credentialID int64, name string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	return s.repo.Rename(ctx, userID, credentialID, normalizePasskeyName(name))
}

func (s *PasskeyService) Delete(ctx context.Context, userID, credentialID int64, password string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err = verifyPasskeyPassword(user, password); err != nil {
		return err
	}
	return s.repo.Delete(ctx, userID, credentialID)
}

func (s *PasskeyService) loadWebAuthnUser(
	ctx context.Context,
	user *User,
	handle []byte,
) (*passkeyUser, error) {
	records, err := s.repo.ListByUserID(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	credentials := make([]webauthn.Credential, 0, len(records))
	for i := range records {
		credentials = append(credentials, records[i].Credential)
	}
	return &passkeyUser{account: user, handle: handle, credentials: credentials}, nil
}

func normalizePasskeyName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultPasskeyName
	}
	runes := []rune(name)
	if len(runes) > maxPasskeyNameLength {
		name = string(runes[:maxPasskeyNameLength])
	}
	return name
}

func passkeySummary(record *PasskeyCredentialRecord) *PasskeyCredentialSummary {
	return &PasskeyCredentialSummary{
		ID:         record.ID,
		Name:       record.Name,
		CreatedAt:  record.CreatedAt,
		LastUsedAt: record.LastUsedAt,
		Backup:     record.Credential.Flags.BackupState,
	}
}

var _ webauthn.User = (*passkeyUser)(nil)
