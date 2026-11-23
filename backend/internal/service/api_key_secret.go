package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 浏览器端封存 API Key（sealed secret）的契约常量。
//
// 安全模型（为什么服务端只存密文）：
//   - 用户创建 Key 后想「以后还能自己看到」，但又不能让数据库里出现明文。
//   - 因此由浏览器用登录密码经 PBKDF2-SHA256 派生 AES-256-GCM 密钥，加密后
//     只上传密文与 KDF 参数；解密也在浏览器里做。
//   - 服务端既不保存明文，也不保存密码、不持有任何可解密材料。拖库只能拿到
//     密文，攻击者还得先拿到用户登录密码。
//   - 「不保存」不等于「看不到」：封存请求（PUT）把登录密码和密文放在同一个请求体里，服务端收到后
//     只做一次 bcrypt 比对（确认封存用的确实是登录密码，见 APIKeyVerificationService.VerifyForSeal）
//     就丢弃，不落库、不进日志、不进审计。所以请求链路上的任何一环（TLS 终结点、反向代理、服务进程
//     内存）都能同时看到密码与密文——这条路径的信任边界是服务端进程，不是「只有浏览器」。
//
// 本文件定义三件东西：请求/存储模型、输入校验、以及「只做存取不做解密」的服务。
const (
	// APIKeySecretMaxBodyBytes 是封存接口允许的最大请求体（8KB）。
	// 前端上报的 sealed 上限 4096 字节，base64 后约 5.5KB，加 KDF 参数远低于此。
	APIKeySecretMaxBodyBytes int64 = 8 * 1024

	// APIKeySecretKDFAlgoPBKDF2SHA256 是当前唯一允许的 KDF 算法。
	APIKeySecretKDFAlgoPBKDF2SHA256 = "PBKDF2-SHA256"

	// APIKeySecretSupportedVersion 是当前唯一支持的密文信封版本。
	APIKeySecretSupportedVersion = 1

	apiKeySecretMinSealedBytes = 32
	apiKeySecretMaxSealedBytes = 4096
	apiKeySecretMinSaltBytes   = 8
	apiKeySecretMaxSaltBytes   = 64
	apiKeySecretMinIterations  = 100000
	apiKeySecretMaxIterations  = 2000000
)

// 审计动作名：查看密文必须留痕（它是一条把密文交给浏览器的读操作），
// 封存 / 删除同样是凭据生命周期事件。GET 的动作名由需求方固定为
// API_KEY_SECRET_FETCH，其余两个沿同一命名口径。
const (
	AuditActionAPIKeySecretFetch  = "API_KEY_SECRET_FETCH"
	AuditActionAPIKeySecretSeal   = "API_KEY_SECRET_SEAL"
	AuditActionAPIKeySecretDelete = "API_KEY_SECRET_DELETE"
)

var (
	// ErrAPIKeySecretNotFound 表示这把 Key 没有可查看的封存密文（从未封存或已删除）。
	ErrAPIKeySecretNotFound = infraerrors.NotFound("API_KEY_SECRET_NOT_FOUND", "api key secret not found")
	// ErrAPIKeySecretInvalid 表示封存请求的参数不合法（版本 / KDF / 密文长度）。
	ErrAPIKeySecretInvalid = infraerrors.BadRequest("API_KEY_SECRET_INVALID", "invalid api key secret")
)

// APIKeySecretKDF 是前端上报的密钥派生参数，原样随密文保存。
// 注意：salt 不是秘密，它必须和密文一起存，否则用户以后无法在浏览器里重新派生密钥。
type APIKeySecretKDF struct {
	Algo       string `json:"algo"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
}

// APIKeySecret 是 api_key_secrets 一行的领域模型。
//
// KDF 用 json.RawMessage 保存以便原样回给前端（不重新序列化，避免字段顺序、
// 数字精度等细节发生变化导致前端解不开）。
//
// AuthFactor 记录「创建时用哪个因子授权封存」（password / totp / passkey），
// 二次查看时必须用同一因子重新授权；NULL/空串表示本次改造前封存的旧密文。
type APIKeySecret struct {
	APIKeyID   int64
	UserID     int64
	Version    int
	KDF        json.RawMessage
	Sealed     string
	AuthFactor string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// APIKeySecretSealInput 是 PUT secret-seal 的已解析请求体。
type APIKeySecretSealInput struct {
	Version int
	KDF     json.RawMessage
	Sealed  string
	// AuthFactor 本次封存授权使用的因子（password/totp/passkey）。
	// 空串表示旧客户端：按兼容处理，不记录因子（查看时不限制因子）。
	AuthFactor string
}

// APIKeySecretRepository 是 api_key_secrets 的持久化端口。
//
// 全部方法都以 user_id 作为过滤条件：即使 api_key_id 被猜中，也只能命中
// 自己的那行；路由层还会先校验 Key 归属，这里是第二道防线。
type APIKeySecretRepository interface {
	// Upsert 幂等写入：同一把 Key 重复封存时覆盖旧密文（用户改密码后重新封装）。
	Upsert(ctx context.Context, secret *APIKeySecret) error
	// GetByAPIKeyID 读取一把 Key 的密文；不存在时返回 ErrAPIKeySecretNotFound。
	GetByAPIKeyID(ctx context.Context, apiKeyID, userID int64) (*APIKeySecret, error)
	// DeleteByAPIKeyID 删除密文；不存在时同样返回 nil（幂等）。
	DeleteByAPIKeyID(ctx context.Context, apiKeyID, userID int64) error
	// ListPresenceByAPIKeyIDs 返回这批 Key 中哪些已经封存过密文，以及各自的
	// 授权因子（空串 = 旧密文）。列表接口只用它算 has_sealed_secret /
	// seal_auth_factor，读取不到密文本体。
	ListPresenceByAPIKeyIDs(ctx context.Context, userID int64, apiKeyIDs []int64) (map[int64]string, error)
}

// APIKeySecretService 是「只存/取密文」的服务。
//
// 它不做任何加解密，也拿不到密码：封存请求里的登录密码只由 APIKeyVerificationService.VerifyForSeal
// 做 bcrypt 比对，不经过本服务；这里只存取密文。
// 所有查询都先经 APIKeyRepository 校验 Key 属于当前用户，避免越权读密文。
type APIKeySecretService struct {
	apiKeyRepo APIKeyRepository
	secretRepo APIKeySecretRepository
}

// NewAPIKeySecretService 创建封存密文服务。
func NewAPIKeySecretService(apiKeyRepo APIKeyRepository, secretRepo APIKeySecretRepository) *APIKeySecretService {
	return &APIKeySecretService{
		apiKeyRepo: apiKeyRepo,
		secretRepo: secretRepo,
	}
}

// Seal 校验并保存一把属于 userID 的 Key 的浏览器端密文。
// 返回的 *APIKey 供 handler 写审计 extra（api_key_id / prefix），不含密文。
func (s *APIKeySecretService) Seal(ctx context.Context, userID, apiKeyID int64, in APIKeySecretSealInput) (*APIKey, error) {
	key, err := s.ownedAPIKey(ctx, userID, apiKeyID)
	if err != nil {
		return nil, err
	}
	secret, err := validateAPIKeySealInput(in)
	if err != nil {
		return nil, err
	}
	secret.APIKeyID = key.ID
	secret.UserID = userID
	secret.AuthFactor = NormalizeAPIKeyAuthFactor(in.AuthFactor)
	if err := s.secretRepo.Upsert(ctx, secret); err != nil {
		return nil, fmt.Errorf("upsert api key secret: %w", err)
	}
	return key, nil
}

// Get 返回一把属于 userID 的 Key 的密文。Key 不属于该用户时按 404 处理，
// 不泄露「这个 ID 存在」；没有封存记录时返回 API_KEY_SECRET_NOT_FOUND。
func (s *APIKeySecretService) Get(ctx context.Context, userID, apiKeyID int64) (*APIKeySecret, *APIKey, error) {
	key, err := s.ownedAPIKey(ctx, userID, apiKeyID)
	if err != nil {
		return nil, nil, err
	}
	secret, err := s.secretRepo.GetByAPIKeyID(ctx, key.ID, userID)
	if err != nil {
		return nil, nil, err
	}
	return secret, key, nil
}

// Delete 删除一把属于 userID 的 Key 的密文（用户关闭「后续可查看」）。
// 重复删除幂等返回成功。
func (s *APIKeySecretService) Delete(ctx context.Context, userID, apiKeyID int64) (*APIKey, error) {
	key, err := s.ownedAPIKey(ctx, userID, apiKeyID)
	if err != nil {
		return nil, err
	}
	if err := s.secretRepo.DeleteByAPIKeyID(ctx, key.ID, userID); err != nil {
		return nil, fmt.Errorf("delete api key secret: %w", err)
	}
	return key, nil
}

// Presence 批量返回这批 Key 中哪些已有封存密文，以及各自的授权因子。
// 供列表计算 has_sealed_secret / seal_auth_factor。
func (s *APIKeySecretService) Presence(ctx context.Context, userID int64, apiKeyIDs []int64) (map[int64]string, error) {
	if s == nil || s.secretRepo == nil || len(apiKeyIDs) == 0 {
		return map[int64]string{}, nil
	}
	return s.secretRepo.ListPresenceByAPIKeyIDs(ctx, userID, apiKeyIDs)
}

// ownedAPIKey 加载 Key 并校验归属。非本人 Key 统一按 NotFound 返回，
// 避免通过错误码差异探测别人是否存在某个 Key ID。
func (s *APIKeySecretService) ownedAPIKey(ctx context.Context, userID, apiKeyID int64) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil || s.secretRepo == nil {
		return nil, ErrAPIKeySecretNotFound
	}
	if userID <= 0 || apiKeyID <= 0 {
		return nil, ErrAPIKeyNotFound
	}
	key, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	return key, nil
}

// validateAPIKeySealInput 校验前端上报的封存参数，并归一化成待落库模型。
//
// 这里的上限/下限是「格式健全性」检查，不是安全边界：真正的安全来自
// 「服务端拿不到解密材料」。但校验越界值可以挡住手滑、探针和明显异常的客户端。
func validateAPIKeySealInput(in APIKeySecretSealInput) (*APIKeySecret, error) {
	if in.Version != APIKeySecretSupportedVersion {
		return nil, invalidSecret("unsupported version")
	}

	var kdf APIKeySecretKDF
	if len(in.KDF) == 0 {
		return nil, invalidSecret("missing kdf")
	}
	if err := json.Unmarshal(in.KDF, &kdf); err != nil {
		return nil, invalidSecret("kdf must be a JSON object")
	}
	if kdf.Algo != APIKeySecretKDFAlgoPBKDF2SHA256 {
		return nil, invalidSecret("unsupported kdf algo")
	}
	if kdf.Iterations < apiKeySecretMinIterations || kdf.Iterations > apiKeySecretMaxIterations {
		return nil, invalidSecret("kdf iterations out of range")
	}
	salt, err := decodeBase64(kdf.Salt)
	if err != nil {
		return nil, invalidSecret("kdf salt must be base64")
	}
	if len(salt) < apiKeySecretMinSaltBytes || len(salt) > apiKeySecretMaxSaltBytes {
		return nil, invalidSecret("kdf salt length out of range")
	}

	sealed, err := decodeBase64(in.Sealed)
	if err != nil {
		return nil, invalidSecret("sealed must be base64")
	}
	if len(sealed) < apiKeySecretMinSealedBytes || len(sealed) > apiKeySecretMaxSealedBytes {
		return nil, invalidSecret("sealed length out of range")
	}

	return &APIKeySecret{
		Version: in.Version,
		KDF:     append(json.RawMessage(nil), in.KDF...),
		Sealed:  in.Sealed,
	}, nil
}

// decodeBase64 同时接受带填充与不带填充的标准 base64。
// 浏览器 btoa 一定带填充；部分客户端会用 URL-safe 之外的 Raw 变体，
// 这里两种都认，避免因为尾部 = 让功能不可用。
func decodeBase64(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("empty base64")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func invalidSecret(reason string) error {
	return infraerrors.BadRequest("API_KEY_SECRET_INVALID", "invalid api key secret: "+reason)
}
