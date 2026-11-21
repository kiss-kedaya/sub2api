package service

import (
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/ent/authidentitychannel"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authsubject"
)

// AuthIdentitySubjectKey 是登录匹配与身份查重的统一入口。
//
// 迁移策略（阶段 A → 阶段 C）：
//  1. 阶段 A 起，写入路径同时写明文列（回退用）、keyed 盲索引 hash 与展示前缀；
//  2. 查询路径优先命中 hash 列，过渡期回退到明文列，保证「已回填」与
//     「未回填」的行都能登录；
//  3. 全量部署 + 回填校验通过后，运维 SQL 清空明文列；OR 的明文分支自然失效
//     （NULL 不命中），不需要再改代码删除回退路径。
//
// 哈希是 HMAC-SHA256(installation key, domain|provider_type|subject)：
// 安装密钥由 repository/security_secret_bootstrap.go 在启动时注入，
// 数据库泄露但没有安装密钥时无法离线反推 email 等低熵 subject。
const (
	// AuthSubjectHashHexLen 与 *_subject_hash 列宽一致。
	AuthSubjectHashHexLen = authsubject.HashHexLen
	// AuthSubjectPrefixMaxLen 与 *_subject_prefix 列宽一致。
	AuthSubjectPrefixMaxLen = authsubject.PrefixColumnLen
)

var (
	// authIdentityMetadataSensitiveKeys 是 identity/channel metadata 中不允许
	// 持久化的 subject 派生字段。写路径统一走 SanitizeAuthIdentityMetadata，
	// 历史行由运维 SQL 一次性 scrub。
	authIdentityMetadataSensitiveKeys = map[string]struct{}{
		"subject":          {},
		"provider_subject": {},
		"union_id":         {},
		"unionid":          {},
		"openid":           {},
		"channel_subject":  {},
		"corp_user_id":     {},
		"synthetic_email":  {},
		"email":            {},
	}
	// authIdentityClaimsSensitiveKeys 是 pending 会话 claims 中要丢弃的
	// subject 明文键（channel_subject 单独替换成 hash/prefix）。
	authIdentityClaimsSensitiveKeys = map[string]struct{}{
		"subject":          {},
		"provider_subject": {},
		"union_id":         {},
		"unionid":          {},
		"openid":           {},
		"channel_subject":  {},
	}
)

// ConfigureAuthSubjectHashKey 在安装密钥 bootstrap 之后注入 keyed hash 的安装密钥。
func ConfigureAuthSubjectHashKey(installationKeyHex string) error {
	return authsubject.ConfigureInstallationKey(installationKeyHex)
}

// AuthSubjectHashKeyConfigured 报告 keyed hash 的安装密钥是否已注入。
func AuthSubjectHashKeyConfigured() bool {
	return authsubject.InstallationKeyConfigured()
}

// NormalizeAuthSubject 返回参与哈希与明文比对的规范形式。
func NormalizeAuthSubject(subject string) string {
	return authsubject.Normalize(subject)
}

// HashAuthIdentitySubject 返回 auth_identities.provider_subject 的 keyed 盲索引。
func HashAuthIdentitySubject(providerType, subject string) string {
	return authsubject.HashIdentity(providerType, subject)
}

// HashAuthChannelSubject 返回 auth_identity_channels.channel_subject 的 keyed 盲索引。
func HashAuthChannelSubject(providerType, subject string) string {
	return authsubject.HashChannel(providerType, subject)
}

// AuthSubjectPrefixFromHash 返回后台展示用的短前缀（取自盲索引，不含 subject 原文）。
func AuthSubjectPrefixFromHash(hashValue string) string {
	return authsubject.PrefixFromHash(hashValue)
}

// MaskAuthSubjectPrefix 把落库前缀渲染成掩码。
func MaskAuthSubjectPrefix(prefix string) string {
	return authsubject.MaskPrefix(prefix)
}

// MaskAuthSubjectHash 把盲索引渲染成掩码。
func MaskAuthSubjectHash(hashValue string) string {
	return authsubject.MaskPrefix(authsubject.PrefixFromHash(hashValue))
}

// IsAuthSubjectHash 判断字符串是否形如 HMAC-SHA256 hex 摘要（迁移期辅助判断）。
func IsAuthSubjectHash(value string) bool {
	return authsubject.IsHash(value)
}

// SealAuthSubjectValue 用安装密钥封存 pending 会话里的 subject 明文。
func SealAuthSubjectValue(plaintext string) (string, error) {
	return authsubject.SealSensitive(plaintext)
}

// IsAuthSubjectSealed 判断值是否被安装密钥封存（pending 会话用）。
func IsAuthSubjectSealed(value string) bool {
	return authsubject.IsSealed(value)
}

// PendingAuthSubjectPlaintext 读取 pending 会话里的 subject：
//   - 新会话是 "enc:sub:v1:" 密文，用安装密钥解开；
//   - 历史在途会话是明文，原样返回；
//   - 解密失败返回空串（调用方仍有 hash 列可完成匹配）。
func PendingAuthSubjectPlaintext(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	plaintext, sealed, err := authsubject.OpenSensitive(value)
	if !sealed {
		return value
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(plaintext)
}

// AuthSubjectPlaintext 安全地读取 ent 里可空的 subject / channel_subject。
func AuthSubjectPlaintext(subject *string) string {
	if subject == nil {
		return ""
	}
	return strings.TrimSpace(*subject)
}

// AuthSubjectColumns 返回 (hash, prefix, plaintext) 三列的标准写法。
// 过渡期明文列仍然写入，供旧二进制与回退查询使用；阶段 C 的运维 SQL
// 会把明文列清空，届时新版本代码会随下一版改为只写 hash/prefix。
func AuthSubjectColumns(providerType, subject string, hashFn func(providerType, subject string) string) (string, string, string) {
	normalized := authsubject.Normalize(subject)
	hashValue := hashFn(providerType, normalized)
	return hashValue, authsubject.PrefixFromHash(hashValue), normalized
}

// AuthIdentitySubjectLookupPredicate 构造「按明文 subject 匹配身份」的查询条件：
// hash 命中，或过渡期明文命中。
func AuthIdentitySubjectLookupPredicate(providerType, subject string) predicate.AuthIdentity {
	normalized := authsubject.Normalize(subject)
	return authidentity.Or(
		authidentity.ProviderSubjectHashEQ(authsubject.HashIdentity(providerType, normalized)),
		authidentity.ProviderSubjectEQ(normalized),
	)
}

// AuthIdentitySubjectHashLookupPredicate 用「库里已存 hash + 过渡期明文」匹配身份。
// hashValue 为空（历史未回填的 pending 会话）时只按明文匹配。
func AuthIdentitySubjectHashLookupPredicate(providerType, hashValue, plaintext string) predicate.AuthIdentity {
	hashValue = strings.TrimSpace(hashValue)
	plaintext = authsubject.Normalize(plaintext)
	if hashValue == "" && plaintext == "" {
		return authidentity.ProviderSubjectHashEQ(authsubject.HashIdentity(providerType, ""))
	}
	predicates := []predicate.AuthIdentity{}
	if hashValue != "" {
		predicates = append(predicates, authidentity.ProviderSubjectHashEQ(hashValue))
	}
	if plaintext != "" {
		predicates = append(predicates, authidentity.ProviderSubjectEQ(plaintext))
	}
	return authidentity.Or(predicates...)
}

// AuthChannelSubjectLookupPredicate 构造「按明文 channel_subject 匹配渠道」的查询条件：
// hash 命中，或过渡期明文命中。
func AuthChannelSubjectLookupPredicate(providerType, channelSubject string) predicate.AuthIdentityChannel {
	normalized := authsubject.Normalize(channelSubject)
	return authidentitychannel.Or(
		authidentitychannel.ChannelSubjectHashEQ(authsubject.HashChannel(providerType, normalized)),
		authidentitychannel.ChannelSubjectEQ(normalized),
	)
}

// AuthChannelSubjectHashLookupPredicate 用库里已存的 hash + 明文匹配渠道。
func AuthChannelSubjectHashLookupPredicate(providerType, hashValue, plaintext string) predicate.AuthIdentityChannel {
	hashValue = strings.TrimSpace(hashValue)
	plaintext = authsubject.Normalize(plaintext)
	if hashValue == "" && plaintext == "" {
		return authidentitychannel.ChannelSubjectHashEQ(authsubject.HashChannel(providerType, ""))
	}
	predicates := []predicate.AuthIdentityChannel{}
	if hashValue != "" {
		predicates = append(predicates, authidentitychannel.ChannelSubjectHashEQ(hashValue))
	}
	if plaintext != "" {
		predicates = append(predicates, authidentitychannel.ChannelSubjectEQ(plaintext))
	}
	return authidentitychannel.Or(predicates...)
}

// ApplyAuthIdentitySubjectCreate 给新建身份同时写明文（过渡期）、keyed hash 与展示前缀。
func ApplyAuthIdentitySubjectCreate(create *dbent.AuthIdentityCreate, providerType, subject string) *dbent.AuthIdentityCreate {
	hashValue, prefix, plaintext := AuthSubjectColumns(providerType, subject, authsubject.HashIdentity)
	return create.
		SetProviderSubject(plaintext).
		SetProviderSubjectHash(hashValue).
		SetProviderSubjectPrefix(prefix)
}

// ApplyAuthIdentitySubjectUpdate 在改 subject 时同步刷新 hash 与展示前缀。
func ApplyAuthIdentitySubjectUpdate(update *dbent.AuthIdentityUpdateOne, providerType, subject string) *dbent.AuthIdentityUpdateOne {
	hashValue, prefix, plaintext := AuthSubjectColumns(providerType, subject, authsubject.HashIdentity)
	return update.
		SetProviderSubject(plaintext).
		SetProviderSubjectHash(hashValue).
		SetProviderSubjectPrefix(prefix)
}

// ApplyAuthIdentitySubjectOnlyHash 给新建身份只写 hash 与展示前缀。
// 供「明文列已经被运维清空」之后的写路径（如 pending 会话派生）使用。
func ApplyAuthIdentitySubjectOnlyHash(create *dbent.AuthIdentityCreate, hashValue, prefix string) *dbent.AuthIdentityCreate {
	return create.
		SetProviderSubjectHash(strings.TrimSpace(hashValue)).
		SetProviderSubjectPrefix(strings.TrimSpace(prefix))
}

// ApplyAuthChannelSubjectCreate 给新建渠道写明文（过渡期）、keyed hash 与展示前缀。
func ApplyAuthChannelSubjectCreate(create *dbent.AuthIdentityChannelCreate, providerType, channelSubject string) *dbent.AuthIdentityChannelCreate {
	hashValue, prefix, plaintext := AuthSubjectColumns(providerType, channelSubject, authsubject.HashChannel)
	return create.
		SetChannelSubject(plaintext).
		SetChannelSubjectHash(hashValue).
		SetChannelSubjectPrefix(prefix)
}

// ApplyAuthChannelSubjectUpdate 在改 channel subject 时同步刷新 hash 与展示前缀。
func ApplyAuthChannelSubjectUpdate(update *dbent.AuthIdentityChannelUpdateOne, providerType, channelSubject string) *dbent.AuthIdentityChannelUpdateOne {
	hashValue, prefix, plaintext := AuthSubjectColumns(providerType, channelSubject, authsubject.HashChannel)
	return update.
		SetChannelSubject(plaintext).
		SetChannelSubjectHash(hashValue).
		SetChannelSubjectPrefix(prefix)
}

// ApplyAuthChannelSubjectOnlyHash 给新建渠道只写 hash 与展示前缀。
func ApplyAuthChannelSubjectOnlyHash(create *dbent.AuthIdentityChannelCreate, hashValue, prefix string) *dbent.AuthIdentityChannelCreate {
	return create.
		SetChannelSubjectHash(strings.TrimSpace(hashValue)).
		SetChannelSubjectPrefix(strings.TrimSpace(prefix))
}

// ApplyPendingAuthSubjectCreate 给新建 pending 会话写入：
//   - keyed hash + 展示前缀（登录匹配用）；
//   - 安装密钥封存的 subject 密文（完成流程时解出原文，用于 identity 双写与旧版本兼容）。
//
// 库里不再出现 pending 会话的 subject 明文。upstream claims 由
// SanitizeOAuthIdentityClaims 在写入前清洗。
func ApplyPendingAuthSubjectCreate(create *dbent.PendingAuthSessionCreate, providerType, subject string) (*dbent.PendingAuthSessionCreate, error) {
	hashValue, prefix, plaintext := AuthSubjectColumns(providerType, subject, authsubject.HashIdentity)
	if plaintext == "" {
		return create.SetProviderSubjectHash(hashValue).SetProviderSubjectPrefix(prefix), nil
	}
	sealed, err := authsubject.SealSensitive(plaintext)
	if err != nil {
		return nil, err
	}
	return create.
		SetProviderSubject(sealed).
		SetProviderSubjectHash(hashValue).
		SetProviderSubjectPrefix(prefix), nil
}

// AuthIdentitySubjectHashConflictColumns 返回 upsert 时用于去重的唯一索引列。
func AuthIdentitySubjectHashConflictColumns() []string {
	return []string{
		authidentity.FieldProviderType,
		authidentity.FieldProviderKey,
		authidentity.FieldProviderSubjectHash,
	}
}

// AuthChannelSubjectHashConflictColumns 返回渠道 upsert 时用于去重的唯一索引列。
func AuthChannelSubjectHashConflictColumns() []string {
	return []string{
		authidentitychannel.FieldProviderType,
		authidentitychannel.FieldProviderKey,
		authidentitychannel.FieldChannel,
		authidentitychannel.FieldChannelAppID,
		authidentitychannel.FieldChannelSubjectHash,
	}
}

// SanitizeAuthIdentityMetadata 返回 metadata 的安全副本：
// 去掉 subject 派生字段（subject/unionid/openid/channel_subject/...）与
// 派生的 synthetic_email，保留展示需要的 display_name/avatar/username/source。
func SanitizeAuthIdentityMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if _, sensitive := authIdentityMetadataSensitiveKeys[key]; sensitive {
			continue
		}
		out[key] = sanitizeAuthMetadataValue(value)
	}
	return out
}

func sanitizeAuthMetadataValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return SanitizeAuthIdentityMetadata(typed)
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, sanitizeAuthMetadataValue(item))
		}
		return items
	default:
		return value
	}
}

// SanitizeOAuthIdentityClaims 返回 pending 会话 claims 的安全副本：
//   - 丢弃 subject 明文键（subject/provider_subject/union_id/unionid/openid）；
//   - 把 channel_subject 替换成 channel_subject_hash + channel_subject_prefix；
//   - 保留钉钉同步等功能依赖的字段（email/username/nickname/corp_user_id/
//     primary_dept_id/issuer/suggested_*/aff_code）。
//
// 会话本身只存活分钟级，但数据库被 dump 时不应出现第三方身份原文。
func SanitizeOAuthIdentityClaims(providerType string, claims map[string]any) map[string]any {
	if len(claims) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(claims)+2)
	for key, value := range claims {
		if _, sensitive := authIdentityClaimsSensitiveKeys[key]; sensitive {
			continue
		}
		out[key] = sanitizeAuthMetadataValue(value)
	}
	if rawChannelSubject, ok := claims["channel_subject"]; ok {
		if channelSubject, ok := rawChannelSubject.(string); ok && strings.TrimSpace(channelSubject) != "" {
			channelHash, prefix, _ := AuthSubjectColumns(providerType, channelSubject, authsubject.HashChannel)
			out["channel_subject_hash"] = channelHash
			out["channel_subject_prefix"] = prefix
			// 历史数据里可能有 channel subject 被当成 canonical identity subject 的行，
			// 另存一份 identity 域 hash，供 pending 流程无明文回查。
			out["channel_subject_identity_hash"] = authsubject.HashIdentity(providerType, channelSubject)
			if sealed, err := authsubject.SealSensitive(channelSubject); err == nil {
				out["channel_subject_sealed"] = sealed
			}
		}
	}
	return out
}
