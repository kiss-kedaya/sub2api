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

// Passkey 因子编排使用的 ceremony 类型（与 passwordless 登录区分）：
//   - login_2fa：密码登录第二步的 Passkey assertion（登录会话绑定用户）；
//   - step_up：已登录会话的敏感操作 step-up assertion。
//
// 两者都校验 UserID，防止 A 用户的 ceremony token 被 B 用户使用。
const (
	// PasskeySessionKindLogin2FA 密码登录双因子编排的 Passkey ceremony。
	PasskeySessionKindLogin2FA = "login_2fa"
	// PasskeySessionKindStepUp 敏感操作 step-up 的 Passkey ceremony。
	PasskeySessionKindStepUp = "step_up"
	// PasskeySessionKindKeyVerify API Key 创建 / 二次查看的三选一身份校验 ceremony。
	// 与 step_up 分开：它只签发「一次性的 Key 校验票据」，不写 5 分钟 step-up 授权，
	// 避免一次 Key 查看把账号的其它敏感操作窗口一起打开。
	PasskeySessionKindKeyVerify = "key_verify"
)

// passkeySessionKindFinalize 报告某个 ceremony 类型是否允许用于
// authenticated-user 的 assertion（登录 2FA / step-up / API Key 校验）。
func passkeySessionKindFinalize(kind string) bool {
	return kind == PasskeySessionKindLogin2FA ||
		kind == PasskeySessionKindStepUp ||
		kind == PasskeySessionKindKeyVerify
}

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
	// Decoy 标记登录首步「凭据无效」时下发的伪装 Passkey ceremony（login_shape_disguise）。
	// UserID 是 token 用户 ID 段：镜像真实账号时就是真实用户 ID，否则是 HMAC 取的占位值。
	// 伪装登录会话只会用 FinishDisguisedLogin 消费它；真实登录会话拿到它，行为与拿到「同一用户的另一个
	// 真实 ceremony」完全一致（FinishLoginForUser 不区分，没有认证器的有效签名谁也过不了校验）。
	Decoy bool `json:"decoy,omitempty"`
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
	// idSpace 缓存「现网最大用户 ID 的 2 的幂上界」，只给登录首步伪装 token 估算用户 ID 段（见 passkey_login_disguise.go）。
	idSpace disguiseIDSpaceCache
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

// BeginLoginForUser 为「已知用户」的 Passkey 因子验证生成 assertion 挑战。
//
// 用于密码登录第二步（FactorPasskey）与敏感操作 step-up：与 passwordless
// 登录不同，这里必须指定用户，且 challenge 会话绑定 (kind, userID)，防止
// ceremony token 被其他用户复用。challenge 存 Redis（PasskeySessionStore），
// 5 分钟过期、一次性消费。
func (s *PasskeyService) BeginLoginForUser(
	ctx context.Context,
	userID int64,
	kind string,
) (assertion *protocol.CredentialAssertion, sessionToken string, err error) {
	if err = s.requireEnabled(); err != nil {
		return nil, "", err
	}
	if !passkeySessionKindFinalize(kind) {
		return nil, "", ErrPasskeySession
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if user == nil || !user.IsActive() {
		return nil, "", ErrUserNotActive
	}
	handle, err := s.repo.GetUserHandle(ctx, userID)
	if err != nil {
		// 没有 user handle 说明账号从未注册过 Passkey：按未绑定处理。
		return nil, "", ErrPasskeyNotFound
	}
	waUser, err := s.loadWebAuthnUser(ctx, user, handle)
	if err != nil {
		return nil, "", err
	}
	if len(waUser.credentials) == 0 {
		return nil, "", ErrPasskeyNotFound
	}
	assertion, session, err := s.webAuthn.BeginLogin(
		waUser,
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", fmt.Errorf("begin passkey assertion: %w", err)
	}
	sessionToken, err = s.sessions.Store(ctx, &PasskeySession{
		Kind:     kind,
		UserID:   userID,
		WebAuthn: *session,
	}, passkeySessionTTL)
	if err != nil {
		return nil, "", err
	}
	return assertion, sessionToken, nil
}

// FinishLoginForUser 校验「已知用户」的 Passkey assertion。
// 成功返回账号并更新凭据的 last_used/counter；kind/userID 与会话不符返回
// ErrPasskeySession（fail-closed），签名/challenge/origin 失败沿用 #558 的
// classifyPasskeyVerificationError，返回可区分的 PASSKEY_* 错误码。
func (s *PasskeyService) FinishLoginForUser(
	ctx context.Context,
	userID int64,
	kind string,
	sessionToken string,
	request *http.Request,
) (*User, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if !passkeySessionKindFinalize(kind) {
		return nil, ErrPasskeySession
	}
	session, err := s.sessions.Consume(ctx, sessionToken)
	if err != nil {
		return nil, err
	}
	if session == nil || session.Kind != kind || session.UserID != userID {
		return nil, ErrPasskeySession
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive() {
		return nil, ErrUserNotActive
	}
	handle, err := s.repo.GetUserHandle(ctx, userID)
	if err != nil {
		return nil, ErrPasskeyVerify
	}
	waUser, err := s.loadWebAuthnUser(ctx, user, handle)
	if err != nil {
		return nil, err
	}
	credential, err := s.webAuthn.FinishLogin(waUser, session.WebAuthn, request)
	if err != nil {
		return nil, classifyPasskeyVerificationError(kind, userID, err)
	}
	if err := s.repo.UpdateCredential(ctx, userID, credential, time.Now().UTC()); err != nil {
		return nil, err
	}
	return user, nil
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
