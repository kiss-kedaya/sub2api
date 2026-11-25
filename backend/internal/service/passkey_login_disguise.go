package service

import (
	"context"
	"math/bits"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// 登录首步 Passkey 伪装（审计 A-P1-02，设计见 login_shape_disguise.go）。
//
// 伪造的 assertion 选项直接由真实的 webauthn.BeginLogin 生成（同一个库、同一份配置、
// 同样的 user verification 要求），字段集合、字段顺序、challenge 长度 / 编码、rpId、timeout、
// allowCredentials 的结构因此与真实选项逐字段一致，只有取值不同：
//   - 存在真实账号可镜像：allowCredentials 用该账号真实的凭据（密码对错不改变响应）；
//   - 否则：凭据个数 / ID 长度 / transports 由 HMAC 稳定取值。
//
// 会话存进与真实会话相同的 PasskeySessionStore，TTL 相同；token 形状 v1.login_2fa.<数字>.<随机串>
// 也由同一个 Store 生成，用户 ID 段是数字——镜像账号用真实用户 ID，其余用 HMAC 取一个落在
// 现网用户 ID 范围内的数（DisguiseScope）。镜像账号时连 WebAuthn user handle 也用真实的：诱饵 ceremony
// 被拿去对真实登录会话提交时，与同一用户的另一个真实 ceremony 走同样的校验路径、给出同样的错误。

// UserMaxIDReader 由用户仓储可选实现，返回当前最大的用户 ID（没有用户时为 0）。
// 只用来估算伪造 token 里用户 ID 段的取值范围。
type UserMaxIDReader interface {
	MaxUserID(ctx context.Context) (int64, error)
}

const (
	disguiseIDSpaceTTL         = 10 * time.Minute
	disguiseIDSpaceQueryBudget = 500 * time.Millisecond
)

type disguiseIDSpaceCache struct {
	mu    sync.Mutex
	space int64
	at    time.Time
}

// nextPowerOfTwo 返回不小于 n 的最小 2 的幂（n<=1 返回 1）。取 2 的幂而不是精确的最大 ID，
// 是为了让同一个邮箱的伪造 ID 段在用户数缓慢增长时保持稳定，只在跨过 2 的幂时整体变化一次。
func nextPowerOfTwo(n int64) int64 {
	if n <= 1 {
		return 1
	}
	return int64(1) << bits.Len64(uint64(n-1))
}

// disguiseUserIDSpace 返回伪造用户 ID 段的取值上界。读不到最大用户 ID 时沿用上一次的值，
// 没有上一次的值就用保守常量。
func (s *PasskeyService) disguiseUserIDSpace(ctx context.Context) int64 {
	reader, ok := s.userRepo.(UserMaxIDReader)
	if !ok {
		return loginDisguiseUserIDSpaceFallback
	}
	s.idSpace.mu.Lock()
	defer s.idSpace.mu.Unlock()
	if s.idSpace.space > 0 && time.Since(s.idSpace.at) < disguiseIDSpaceTTL {
		return s.idSpace.space
	}
	queryCtx, cancel := context.WithTimeout(ctx, disguiseIDSpaceQueryBudget)
	defer cancel()
	maxID, err := reader.MaxUserID(queryCtx)
	if err != nil || maxID <= 0 {
		if s.idSpace.space > 0 {
			return s.idSpace.space
		}
		return loginDisguiseUserIDSpaceFallback
	}
	space := nextPowerOfTwo(maxID)
	if space < loginDisguiseUserIDSpaceFloor {
		space = loginDisguiseUserIDSpaceFloor
	}
	s.idSpace.space, s.idSpace.at = space, time.Now()
	return space
}

// discardPasskeyReads 做与真实路径等价的无副作用读（用户行、user handle、凭据列表各一次点查），结果丢弃。
//
// 真实的 begin / finish 在这几次库读上花的时间，是它与诱饵路径耗时差异的主要来源（没有 bcrypt 的
// passkey finish 尤其明显）。诱饵路径补上同样多的读，「是不是真实账号」就不能从响应时间的量级上
// 看出来。读的结果不影响任何响应，错误一律忽略；读的是占位用户 ID（镜像时是真实用户 ID），不外泄任何内容。
func (s *PasskeyService) discardPasskeyReads(ctx context.Context, userID int64, user, handle, list bool) {
	if user && s.userRepo != nil {
		_, _ = s.userRepo.GetByID(ctx, userID)
	}
	if s.repo == nil {
		return
	}
	if handle {
		_, _ = s.repo.GetUserHandle(ctx, userID)
	}
	if list {
		_, _ = s.repo.ListByUserID(ctx, userID)
	}
}

// DisguiseScope 返回伪装挑战 token 里的用户 ID 段，同时也是伪装登录会话与它的 Passkey ceremony 之间的
// 绑定值（对应真实路径里「ceremony 的用户 ID 必须等于登录会话的用户 ID」）：镜像真实账号时是真实用户 ID，
// 否则由 HMAC 在现网用户 ID 范围内取值。同一个邮箱永远得到同一个值。
func (s *PasskeyService) DisguiseScope(ctx context.Context, disguise *LoginDisguise, email string, mirror *User) int64 {
	if mirror != nil {
		return mirror.ID
	}
	if disguise == nil {
		return 0
	}
	return disguise.UserIDScope(email, s.disguiseUserIDSpace(ctx))
}

// BeginDisguisedLogin 为登录首步「凭据无效」的挑战生成 Passkey assertion 选项并存下诱饵会话。
//
// mirror 非空：邮箱对应真实账号，镜像它真实的凭据与用户 ID，让「密码错」与「密码对」的响应
// 在凭据 ID / transports / token 用户 ID 段上也完全一致；mirror 为空：用 HMAC 稳定伪造。
// 诱饵会话 UserID 只是 token 用户 ID 段的占位，Decoy 恒为 true，只能被 FinishDisguisedLogin 消费。
func (s *PasskeyService) BeginDisguisedLogin(
	ctx context.Context,
	disguise *LoginDisguise,
	email string,
	mirror *User,
) (assertion *protocol.CredentialAssertion, sessionToken string, err error) {
	if err = s.requireEnabled(); err != nil {
		return nil, "", err
	}
	if disguise == nil {
		return nil, "", ErrPasskeySession
	}
	var credentials []webauthn.Credential
	scope := s.DisguiseScope(ctx, disguise, email, mirror)
	handle := disguise.UserHandle(email)
	if mirror != nil {
		records, listErr := s.repo.ListByUserID(ctx, mirror.ID)
		if listErr != nil {
			return nil, "", listErr
		}
		for i := range records {
			credentials = append(credentials, records[i].Credential)
		}
		if len(credentials) > 0 {
			// 与真实 begin 一样读 user handle，并用真实的 handle 作为这个 ceremony 的 user handle。
			// 镜像路径已经读过用户行（Login 与 LoginFactorsForEmail 各一次）和凭据列表（LoginRequiredFactors 与上面
			// 各一次），这次读之后库读总数与真实 begin 路径（5 次）持平。
			if realHandle, handleErr := s.repo.GetUserHandle(ctx, mirror.ID); handleErr == nil && len(realHandle) > 0 {
				handle = realHandle
			}
		}
	}
	if len(credentials) == 0 {
		credentials = disguise.PasskeyCredentials(email)
		s.discardPasskeyReads(ctx, scope, true, true, true)
	}
	waUser := &passkeyUser{
		account:     &User{ID: scope, Email: email},
		handle:      handle,
		credentials: credentials,
	}
	assertion, session, err := s.webAuthn.BeginLogin(
		waUser,
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return nil, "", err
	}
	sessionToken, err = s.sessions.Store(ctx, &PasskeySession{
		Kind:     PasskeySessionKindLogin2FA,
		UserID:   scope,
		WebAuthn: *session,
		Decoy:    true,
	}, passkeySessionTTL)
	if err != nil {
		return nil, "", err
	}
	return assertion, sessionToken, nil
}

// FinishDisguisedLogin 消费诱饵 Passkey ceremony 并「验证」提交的 assertion，永远返回错误。
//
// loginScope 是伪装登录会话上记的用户 ID 段（TotpLoginSession.DecoyScope），对应 FinishLoginForUser 里
// 「ceremony 的用户 ID 必须等于登录会话的用户 ID」。与 FinishLoginForUser 同样：先消费会话（一次性，GETDEL），
// 不是登录 ceremony / 不是诱饵 ceremony / 用户 ID 段不一致都返回 ErrPasskeySession，然后用同一个库、同一份
// 配置校验 assertion——对象是一个只含诱饵凭据 ID 的占位用户（公钥为空，永远验不过）。解析失败 / challenge /
// origin / RP ID 不一致等错误因此和真实会话走同样的分类，返回同样的错误码；走到签名校验的一律落到 ErrPasskeyVerify。
func (s *PasskeyService) FinishDisguisedLogin(
	ctx context.Context,
	sessionToken string,
	request *http.Request,
	loginScope int64,
) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	session, err := s.sessions.Consume(ctx, sessionToken)
	if err != nil {
		return err
	}
	if session == nil || !session.Decoy || session.Kind != PasskeySessionKindLogin2FA || session.UserID != loginScope {
		return ErrPasskeySession
	}
	// 真实 finish 在校验 assertion 前读用户行、user handle、凭据列表；这里补上同样三次读。
	s.discardPasskeyReads(ctx, session.UserID, true, true, true)
	credentials := make([]webauthn.Credential, 0, len(session.WebAuthn.AllowedCredentialIDs))
	for _, id := range session.WebAuthn.AllowedCredentialIDs {
		credentials = append(credentials, webauthn.Credential{ID: id})
	}
	waUser := &passkeyUser{
		account:     &User{ID: session.UserID},
		handle:      session.WebAuthn.UserID,
		credentials: credentials,
	}
	if _, err = s.webAuthn.FinishLogin(waUser, session.WebAuthn, request); err != nil {
		return classifyPasskeyVerificationError(PasskeySessionKindLogin2FA, 0, err)
	}
	// 占位凭据没有公钥，签名校验不可能通过；真走到这里也不放行。
	return ErrPasskeyVerify
}
