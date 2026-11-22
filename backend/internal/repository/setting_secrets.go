package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/pkg/atrest"
)

// SensitiveSettingKeys 是 settings 表中需要 at-rest 加密的键白名单。
// 只收「只能加密不能哈希」的独立密钥；白名单之外的值保持明文，
// 避免把开关/JSON 配置整体改写导致前端与迁移脚本难以处理。
var SensitiveSettingKeys = []string{
	"github_oauth_client_secret",
	"google_oauth_client_secret",
	"oidc_connect_client_secret",
	"smtp_password",
	"tencent_captcha_app_secret_key",
	"tencent_captcha_cloud_secret_id",
	"tencent_captcha_cloud_secret_key",
	"turnstile_secret_key",
	"notification_email_unsubscribe_secret",
}

var sensitiveSettingKeySet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(SensitiveSettingKeys))
	for _, key := range SensitiveSettingKeys {
		set[key] = struct{}{}
	}
	return set
}()

// IsSensitiveSettingKey 报告 settings 键是否在白名单内。
func IsSensitiveSettingKey(key string) bool {
	_, ok := sensitiveSettingKeySet[key]
	return ok
}

// encodeSettingValue 把要落库的 settings 值编码：白名单键且 sealing 开启
// （SUB2API_ATREST_ENABLED=true 且密钥有效）时写成 enc:v2: 密文；默认关闭或
// 未配置密钥时保持明文（绝不写出无法解密的密文）。
func encodeSettingValue(key, value string) (string, error) {
	if value == "" || !IsSensitiveSettingKey(key) || atrest.IsEncrypted(value) {
		return value, nil
	}
	if !atrest.Enabled() {
		return value, nil
	}
	ciphertext, err := atrest.EncryptString(value)
	if err != nil {
		return "", fmt.Errorf("encrypt setting %s: %w", key, err)
	}
	return ciphertext, nil
}

// decodeSettingValue 在读取边界解密：看到 enc:v2: 前缀就解密（解密失败
// fail-closed，绝不把密文当配置值返回），否则按历史明文返回。
func decodeSettingValue(key, value string) (string, error) {
	if !atrest.IsEncrypted(value) {
		return value, nil
	}
	plaintext, err := atrest.DecryptString(value)
	if err != nil {
		return "", fmt.Errorf("setting %s cannot be decrypted: %w", key, err)
	}
	return plaintext, nil
}

// settingValueEncryptedAtRest 报告该键在库内是否「可能是密文」，用于
// CompareAndSwapValue 决定是否要读原始行再解密比较。
// 判据是「加载到了可用密钥」而不是 sealing 开关：总开关关闭后，历史行仍可能
// 是 enc:v2: 密文，CAS 必须按密文/明文两种形态都能比较。
func settingValueEncryptedAtRest(key string) bool {
	return IsSensitiveSettingKey(key) && atrest.KeyConfigured()
}

// settingRawValue 读取 settings 表的原始值（不解密、不过缓存）。
// 用于 CompareAndSwapValue：调用方的 expected 是明文，而库内可能是密文，
// 需要拿原始行做条件写，才能保持 CAS 语义。
func (r *settingRepository) settingRawValue(ctx context.Context, key string) (string, bool, error) {
	queryCtx, cancel := settingLoadContext(ctx)
	defer cancel()
	m, err := r.client.Setting.Query().Where(setting.KeyEQ(key)).Only(queryCtx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return m.Value, true, nil
}
