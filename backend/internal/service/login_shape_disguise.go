package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// 登录首步「凭据无效」挑战的稳定伪装（审计 A-P1-02）。
//
// 问题：凭据无效时下发的诱饵挑战固定是「TOTP 单因子」，而密码正确、已绑 Passkey 的
// 真实账号拿到的是 passkey / totp+passkey 形状。攻击者拿撞库密码打首步，看到 passkey
// 形状就等于确认了「密码正确 + 该账号绑了 Passkey」，不需要任何第二因子。
//
// 做法（开关 SettingKeyLoginShapeDisguiseEnabled，默认关闭）：
//   - 邮箱对应一个「已启用且至少有一个登录因子」的真实账号：凭据错误时也下发该账号
//     真实的因子形状（含 Passkey 选项）。密码对错不再改变响应形状，诱饵与真实挑战
//     只剩随机值（temp_token / challenge / session token）不同；
//   - 其余邮箱（不存在 / 未启用 / 没有 2FA 的账号）：形状、Passkey 凭据 ID、transports、
//     token 里的用户 ID 段，一律由 HMAC-SHA256(服务端密钥, 规范化邮箱) 确定性给出，
//     同一个邮箱永远得到同一套伪装，攻击者无法靠重复探测的「随机性」区分。
//
// 纯 HMAC（不镜像真实账号）治不了「密码对错可区分」：真实 Passkey 账号密码对时是
// passkey 形状、密码错时是 HMAC 形状，只要两者不同，形状一变就暴露了密码正确。
// 这也是为什么存在真实账号时必须镜像。
//
// 下面几组常量只影响「没有真实形状可镜像」的邮箱，后续可调（见 PR 描述里的校准查询）。

const (
	// 伪装形状占比（权重，合计 loginDisguiseShapeSlots）：绝大多数是 TOTP 单因子，
	// 与全站「开了 TOTP 的人远多于绑了 Passkey 的人」的现实一致；Passkey 相关形状留一点比例，
	// 让「看到 passkey 形状」不再等于「该账号真绑了 Passkey」。
	loginDisguiseShapeSlots             uint32 = 100
	loginDisguiseShapeTOTPWeight        uint32 = 90
	loginDisguiseShapePasskeyWeight     uint32 = 5
	loginDisguiseShapeTOTPPasskeyWeight uint32 = 5

	// 伪造 session token 的用户 ID 段的取值上界不能小于现网最大用户 ID（否则大于上界的
	// 真实 ID 会反过来暴露真实账号），读不到最大用户 ID 时的保守取值。
	loginDisguiseUserIDSpaceFallback int64 = 1 << 14
	// 上界的下限。
	loginDisguiseUserIDSpaceFloor int64 = 1 << 10
)

// 三个形状权重必须正好合计 loginDisguiseShapeSlots：不相等时数组长度非 0 / 为负，编译直接失败。
var _ [0]struct{} = [loginDisguiseShapeSlots - (loginDisguiseShapeTOTPWeight + loginDisguiseShapePasskeyWeight + loginDisguiseShapeTOTPPasskeyWeight)]struct{}{}

type loginDisguiseChoice[T any] struct {
	weight uint32
	value  T
}

// 伪造 Passkey 凭据的分布。它们必须落在真实认证器会产生的取值里：
// 个数取 1 为主；凭据 ID 的字节数取常见认证器的长度；transports 取浏览器真实会上报的组合
// （按字典序，与 WebAuthn getTransports() 的返回顺序一致）。
var (
	loginDisguiseCredentialCounts = []loginDisguiseChoice[int]{
		{weight: 80, value: 1},
		{weight: 15, value: 2},
		{weight: 5, value: 3},
	}
	loginDisguiseCredentialIDLengths = []loginDisguiseChoice[int]{
		{weight: 10, value: 16},
		{weight: 15, value: 20},
		{weight: 55, value: 32},
		{weight: 20, value: 64},
	}
	loginDisguiseTransportSets = []loginDisguiseChoice[[]protocol.AuthenticatorTransport]{
		{weight: 30, value: []protocol.AuthenticatorTransport{protocol.Internal}},
		{weight: 35, value: []protocol.AuthenticatorTransport{protocol.Hybrid, protocol.Internal}},
		{weight: 15, value: nil}, // 部分浏览器不上报 transports，此时字段被省略
		{weight: 8, value: []protocol.AuthenticatorTransport{protocol.USB}},
		{weight: 12, value: []protocol.AuthenticatorTransport{protocol.NFC, protocol.USB}},
	}
)

func pickLoginDisguiseChoice[T any](slot uint32, choices []loginDisguiseChoice[T]) T {
	var total uint32
	for _, choice := range choices {
		total += choice.weight
	}
	n := slot % total
	for _, choice := range choices {
		if n < choice.weight {
			return choice.value
		}
		n -= choice.weight
	}
	return choices[len(choices)-1].value
}

// LoginDisguise 由服务端稳定密钥派生，对同一个规范化邮箱永远给出同一套伪装素材。
// 零值不可用，用 NewLoginDisguise 构造；nil 表示「没有可用密钥」，调用方应退回固定 TOTP 诱饵。
type LoginDisguise struct {
	key [sha256.Size]byte
}

// NewLoginDisguise 从服务端稳定密钥（JWT secret，多个实例之间一致）派生伪装密钥，做域分离，
// 不直接拿 JWT secret 当 HMAC 密钥。secret 为空返回 nil。
func NewLoginDisguise(secret string) *LoginDisguise {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil
	}
	return &LoginDisguise{key: sha256.Sum256([]byte("sub2api/login-shape-disguise/v1\x00" + secret))}
}

// normalizeLoginDisguiseEmail 与 handler 里 loginChallengeRateLimitKey 同一规则：
// 去空白 + 小写，登录对邮箱大小写不敏感，伪装也必须不敏感。
func normalizeLoginDisguiseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// mac 计算 HMAC-SHA256(key, label \0 part1 \0 part2 ...)。
func (d *LoginDisguise) mac(label string, parts ...string) [sha256.Size]byte {
	h := hmac.New(sha256.New, d.key[:])
	_, _ = h.Write([]byte(label))
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// expand 把 HMAC 输出扩展到 n 个字节（凭据 ID 最长 64 字节）。
func (d *LoginDisguise) expand(label string, n int, parts ...string) []byte {
	out := make([]byte, 0, n+sha256.Size)
	for block := 0; len(out) < n; block++ {
		sum := d.mac(label, append(parts[:len(parts):len(parts)], strconv.Itoa(block))...)
		out = append(out, sum[:]...)
	}
	return out[:n]
}

// Shape 返回没有真实形状可镜像的邮箱的伪装因子形状（顺序固定 totp → passkey）：
// 只可能是 [totp]、[passkey]、[totp passkey] 三种，即现网真实挑战会出现的全部形状
// （TotpService.LoginRequiredFactors 的取值集合，去掉「无因子」——无因子直接签发令牌，没有挑战）。
//
// passkeyAvailable=false（Passkey 服务没有配置）时无法给出可用的 assertion 选项，
// 单 Passkey 形状改按 TOTP 单因子处理（仍然确定、稳定）。
func (d *LoginDisguise) Shape(email string, passkeyAvailable bool) []Factor {
	sum := d.mac("shape", normalizeLoginDisguiseEmail(email))
	slot := binary.BigEndian.Uint32(sum[:4]) % loginDisguiseShapeSlots
	switch {
	case slot < loginDisguiseShapeTOTPWeight:
		return []Factor{FactorTOTP}
	case slot < loginDisguiseShapeTOTPWeight+loginDisguiseShapePasskeyWeight:
		if !passkeyAvailable {
			return []Factor{FactorTOTP}
		}
		return []Factor{FactorPasskey}
	default:
		return []Factor{FactorTOTP, FactorPasskey}
	}
}

// PasskeyCredentials 合成该邮箱稳定的一组「看起来真实」的 Passkey 凭据，只用来生成
// assertion 选项：个数 / ID 字节数 / transports 按上面的常量分布稳定取值，凭据 ID 是
// HMAC(密钥, 邮箱 + 序号) 的输出，公钥留空（永远验不过）。
func (d *LoginDisguise) PasskeyCredentials(email string) []webauthn.Credential {
	email = normalizeLoginDisguiseEmail(email)
	countSum := d.mac("credential-count", email)
	count := pickLoginDisguiseChoice(binary.BigEndian.Uint32(countSum[:4]), loginDisguiseCredentialCounts)
	credentials := make([]webauthn.Credential, 0, count)
	for i := 0; i < count; i++ {
		index := strconv.Itoa(i)
		spec := d.mac("credential-spec", email, index)
		idLength := pickLoginDisguiseChoice(binary.BigEndian.Uint32(spec[0:4]), loginDisguiseCredentialIDLengths)
		transports := pickLoginDisguiseChoice(binary.BigEndian.Uint32(spec[4:8]), loginDisguiseTransportSets)
		credentials = append(credentials, webauthn.Credential{
			ID:        d.expand("credential-id", idLength, email, index),
			Transport: append([]protocol.AuthenticatorTransport(nil), transports...),
		})
	}
	return credentials
}

// UserIDScope 返回伪造 passkey_session_token 里的「用户 ID 段」。真实 token 的形状是
// v1.login_2fa.<用户 ID>.<随机串>，用户 ID 段是十进制数字，诱饵必须同样是数字。
// space 是取值上界（现网最大用户 ID 向上取 2 的幂），取值落在 [1, space]。
func (d *LoginDisguise) UserIDScope(email string, space int64) int64 {
	if space < 1 {
		space = 1
	}
	sum := d.mac("user-id-scope", normalizeLoginDisguiseEmail(email))
	return 1 + int64(binary.BigEndian.Uint64(sum[:8])%uint64(space))
}

// UserHandle 返回诱饵 Passkey 会话里的 WebAuthn user handle（只存在 Redis 会话里，不对外）。
func (d *LoginDisguise) UserHandle(email string) []byte {
	sum := d.mac("user-handle", normalizeLoginDisguiseEmail(email))
	return sum[:]
}

// PasswordProbe 是诱饵会话里「第一步提交过的密码」的不可逆指纹（带服务端密钥的 HMAC）。
//
// 真实会话在第二步会重新校验密码：密码错 → 立刻 INVALID_CREDENTIALS 且不消耗 2FA 尝试额度；
// 密码对 → 消耗额度后再校验动态码。诱饵会话要表现一致，就必须知道「第二步提交的密码是不是
// 第一步那个」——在真实会话里它等价于「密码对不对」。
func (d *LoginDisguise) PasswordProbe(email, password string) string {
	sum := d.mac("password-probe", normalizeLoginDisguiseEmail(email), password)
	return hex.EncodeToString(sum[:])
}

// PasswordProbeMatches 常数时间比较第二步提交的密码与诱饵会话里存的指纹。
func (d *LoginDisguise) PasswordProbeMatches(email, password, probe string) bool {
	if d == nil || probe == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(d.PasswordProbe(email, password)), []byte(probe)) == 1
}
