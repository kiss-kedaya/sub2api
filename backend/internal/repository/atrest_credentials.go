package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/pkg/atrest"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// at-rest 加密（2026-11）的关键约定：
//
//  1. 权威副本是 accounts.credentials_cipher（enc:v2: 密文，整段 credentials JSON）与
//     proxies.password_cipher。读取时密文优先，密文为空才回退历史明文列。
//  2. 「双写期」写路径同时写明文列与密文列，旧二进制仍能读明文 → 回滚安全。
//  3. `-atrest-migrate` 封存（seal）明文列：删除敏感子键，写入 "_sealed": true、
//     "_sealed_keys"（敏感键名清单）以及 SQL 仍需要按值比较/分组的身份摘要
//     （"_id_api_key" 等）。封存是确定性的，因此 CAS 可以用同一函数重算出库内
//     表示，避免「明文参数永远不等于封存后的 jsonb」导致的静默 CAS 失效。
//  4. 封存后写路径通过 SQL `CASE WHEN credentials ? '_sealed'` 保持封存状态，
//     不会把明文敏感值写回明文列。
const (
	sealedCredentialsMarker  = "_sealed"
	sealedCredentialsKeysKey = "_sealed_keys"
	sealedIdentityKeyPrefix  = "_id_"
)

// sealedIdentityKeys 是封存后仍需要支持 SQL 等值比较/分组的敏感子键。
// api_key：Ollama Cloud 用量按上游 key 分组/匹配；
// access_token：受限管理员 Codex 导入按 access token 匹配既有账号。
// 其它敏感键只需要 "_sealed_keys" 里的存在性。
var sealedIdentityKeys = []string{"api_key", "access_token"}

// credentialsPlainJSON 把凭据 map 序列化成与 ent JSONB 列一致的明文 JSON。
func credentialsPlainJSON(credentials map[string]any) ([]byte, error) {
	return json.Marshal(normalizeJSONMap(credentials))
}

// credentialsPlainJSONString 同上，返回 string 以便作为 SQL 参数。
func credentialsPlainJSONString(credentials map[string]any) (string, error) {
	raw, err := credentialsPlainJSON(credentials)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// IsSealedCredentials 报告凭据 map 是否已被 -atrest-migrate 封存。
func IsSealedCredentials(credentials map[string]any) bool {
	if credentials == nil {
		return false
	}
	marker, ok := credentials[sealedCredentialsMarker].(bool)
	return ok && marker
}

// SealCredentials 计算凭据 map 的封存投影（确定性，不修改入参）：
//   - 保留全部非敏感子键（base_url / model_mapping / oauth_type / email / ...），
//     所以所有结构性 SQL 投影与判定继续工作；
//   - 删除敏感子键，改为 "_sealed": true 与 "_sealed_keys" 清单；
//   - 对 SQL 仍需比较的少量键写入域分离 SHA-256 摘要（不可逆）。
func SealCredentials(credentials map[string]any) map[string]any {
	normalized := normalizeJSONMap(credentials)
	sealed := make(map[string]any, len(normalized)+3)
	sealedKeys := make([]string, 0, len(service.SensitiveCredentialKeys))
	for key, value := range normalized {
		if service.IsSensitiveCredentialKey(key) {
			if value != nil {
				sealedKeys = append(sealedKeys, key)
			}
			continue
		}
		sealed[key] = value
	}
	sort.Strings(sealedKeys)
	sealed[sealedCredentialsMarker] = true
	if len(sealedKeys) > 0 {
		sealed[sealedCredentialsKeysKey] = sealedKeys
	}
	for _, key := range sealedIdentityKeys {
		value, ok := normalized[key].(string)
		if !ok || value == "" {
			continue
		}
		sealed[sealedIdentityKeyPrefix+key] = sealedIdentityValueDigest(key, value)
	}
	return sealed
}

// sealCredentialsJSON 返回封存投影的 JSON（用于 CAS 参数与写路径）。
func sealCredentialsJSON(credentials map[string]any) (string, error) {
	raw, err := json.Marshal(SealCredentials(credentials))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// sealedIdentityValueDigest 计算身份摘要；access_token 的 SQL 匹配会先 btrim，
// 因此摘要也按去掉首尾空白后的值计算，保证迁移前后可比较。
func sealedIdentityValueDigest(key, value string) string {
	if key == "access_token" {
		value = strings.TrimSpace(value)
	}
	return atrest.IdentityDigest(key, value)
}

// refreshTokenPresentSQL 返回「凭据带非空 refresh_token」的 SQL 谓词，同时兼容
// 历史明文行与 at-rest 封存行（封存行删除敏感值，存在性记录在 _sealed_keys 清单）。
func refreshTokenPresentSQL(column string) string {
	return `
			AND (CASE WHEN ` + column + ` ? '` + sealedCredentialsMarker + `'
				THEN COALESCE((` + column + ` -> '` + sealedCredentialsKeysKey + `') ? 'refresh_token', false)
				ELSE (` + column + ` ? 'refresh_token' AND btrim(` + column + `->>'refresh_token') <> '')
			END)`
}

// refreshTokenAbsentSQL 返回「凭据没有可用的 refresh_token」的 SQL 谓词。
func refreshTokenAbsentSQL(column string) string {
	return `(CASE WHEN ` + column + ` ? '` + sealedCredentialsMarker + `'
				THEN NOT COALESCE((` + column + ` -> '` + sealedCredentialsKeysKey + `') ? 'refresh_token', false)
				ELSE NULLIF(BTRIM(` + column + `->>'refresh_token'), '') IS NULL
			END)`
}

// sealedIdentityExpr 返回「按值比较/分组用的身份表达式」：历史明支行取明文
// api_key/access_token，封存行取域分离摘要。两种形态都可用于等值比较与分组，
// 但同一个查询里两侧必须使用同一形态。
func sealedIdentityExpr(column, key string) string {
	return `CASE WHEN ` + column + ` ? '` + sealedCredentialsMarker + `'
				THEN ` + column + `->>'_id_` + key + `'
				ELSE ` + column + `->>'` + key + `'
			END`
}

// SealedIdentityDigest 提供给 SQL 参数使用：返回封存投影中该键的摘要。
func SealedIdentityDigest(key, value string) string {
	return sealedIdentityValueDigest(key, value)
}

// credentialsCipherForStorage 返回整段凭据的 enc:v2: 密文。
// 未配置 SUB2API_ATREST_KEY 时返回空字符串：写入保持历史明文行为，
// 绝不写出无法解密的密文。
func credentialsCipherForStorage(credentials map[string]any) (string, error) {
	if !atrest.Enabled() {
		return "", nil
	}
	plain, err := credentialsPlainJSON(credentials)
	if err != nil {
		return "", err
	}
	ciphertext, err := atrest.EncryptString(string(plain))
	if err != nil {
		return "", fmt.Errorf("encrypt account credentials: %w", err)
	}
	return ciphertext, nil
}

// credentialsStorageParams 计算写路径需要的三份表示：
// 明文 JSON（未封存行）、封存 JSON（已封存行）、密文列值（未启用密钥时为 nil）。
func credentialsStorageParams(credentials map[string]any) (plainJSON, sealedJSON string, cipher any, err error) {
	plain, err := credentialsPlainJSONString(credentials)
	if err != nil {
		return "", "", nil, err
	}
	sealed, err := sealCredentialsJSON(credentials)
	if err != nil {
		return "", "", nil, err
	}
	ciphertext, err := credentialsCipherForStorage(credentials)
	if err != nil {
		return "", "", nil, err
	}
	if ciphertext == "" {
		return plain, sealed, nil, nil
	}
	return plain, sealed, ciphertext, nil
}

// credentialsCASJSON 返回 CAS 谓词需要的两种库内表示：明文行与封存行。
// SQL 侧写成 (credentials = $plain OR credentials = $sealed) 即同时兼容两种状态。
func credentialsCASJSON(credentials map[string]any) (plainJSON, sealedJSON string, err error) {
	plain, err := credentialsPlainJSONString(credentials)
	if err != nil {
		return "", "", err
	}
	sealed, err := sealCredentialsJSON(credentials)
	if err != nil {
		return "", "", err
	}
	return plain, sealed, nil
}

// credentialsCASParam 把「明文/封存」两种表示包成单个 JSONB 参数：
//
//	{"plain": <明文 JSON>, "sealed": <封存 JSON>}
//
// 这样 CAS 谓词可以在 SQL 内按行状态取值，而参数个数与顺序保持不变：
//
//	(CASE WHEN credentials ? '_sealed'
//	      THEN ($N::jsonb -> 'sealed')
//	      ELSE ($N::jsonb -> 'plain') END) = credentials
//
// 既避免「明文参数永远不等于封存行」造成的静默 CAS 失效，也不引入两套 SQL。
func credentialsCASParam(credentials map[string]any) (string, error) {
	plain, sealed, err := credentialsCASJSON(credentials)
	if err != nil {
		return "", err
	}
	wrapped, err := json.Marshal(map[string]json.RawMessage{
		"plain":  json.RawMessage(plain),
		"sealed": json.RawMessage(sealed),
	})
	if err != nil {
		return "", err
	}
	return string(wrapped), nil
}

// credentialsCASParamFromSnapshotJSON 同 credentialsCASParam，输入为 service 层
// Grok 凭据快照的 CredentialsJSON（明文 JSON）。
func credentialsCASParamFromSnapshotJSON(snapshotJSON string) (string, error) {
	var credentials map[string]any
	if err := json.Unmarshal([]byte(snapshotJSON), &credentials); err != nil {
		return "", fmt.Errorf("decode credentials snapshot: %w", err)
	}
	return credentialsCASParam(credentials)
}

// credentialsCASMatchSQL 生成「库内 credentials 与 CAS 参数相等」的 SQL 谓词。
// column 为列表达式（如 "a.credentials"），placeholder 为 $N。
func credentialsCASMatchSQL(column, placeholder string) string {
	return `(CASE WHEN ` + column + ` ? '` + sealedCredentialsMarker + `'
			THEN (` + placeholder + `::jsonb -> 'sealed')
			ELSE (` + placeholder + `::jsonb -> 'plain')
		END)`
}

// credentialsCASReadSQL 生成按行状态读取 CAS 参数中对应表示的 JSONB 表达式。
func credentialsCASReadSQL(column, placeholder string) string {
	return `(CASE WHEN ` + column + ` ? '` + sealedCredentialsMarker + `'
			THEN (` + placeholder + `::jsonb -> 'sealed')
			ELSE (` + placeholder + `::jsonb -> 'plain')
		END)`
}

// credentialsCASWriteSQL 生成写路径的 SET 表达式：按行现有状态保持封存与否，
// 并把密文列刷新为同一次快照（placeholder 为空时保留原密文）。
func credentialsCASWriteSQL(column, placeholder, cipherPlaceholder string) string {
	expr := `credentials = (CASE WHEN ` + column + ` ? '` + sealedCredentialsMarker + `'
			THEN (` + placeholder + `::jsonb -> 'sealed')
			ELSE (` + placeholder + `::jsonb -> 'plain')
		END)`
	if cipherPlaceholder != "" {
		expr += `, credentials_cipher = COALESCE(` + cipherPlaceholder + `, credentials_cipher)`
	}
	return expr
}

// decryptCredentialsCipher 解密 credentials_cipher；空值返回 nil。
// 解密失败是硬错误：绝不能把密文当业务凭据向上返回。
func decryptCredentialsCipher(ciphertext string) (map[string]any, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return nil, nil
	}
	plain, err := atrest.DecryptString(ciphertext)
	if err != nil {
		return nil, err
	}
	var credentials map[string]any
	if err := json.Unmarshal([]byte(plain), &credentials); err != nil {
		return nil, fmt.Errorf("decode decrypted credentials: %w", err)
	}
	return normalizeJSONMap(credentials), nil
}

// decodeCredentialsStored 从数据库读到的两列还原完整凭据：密文优先，否则明文。
func decodeCredentialsStored(plainJSON []byte, ciphertext string) (map[string]any, error) {
	if strings.TrimSpace(ciphertext) != "" {
		return decryptCredentialsCipher(ciphertext)
	}
	if len(plainJSON) == 0 || string(plainJSON) == "null" {
		return map[string]any{}, nil
	}
	var credentials map[string]any
	if err := json.Unmarshal(plainJSON, &credentials); err != nil {
		return nil, fmt.Errorf("decode stored credentials: %w", err)
	}
	return normalizeJSONMap(credentials), nil
}

// credentialsJSONEqual 近似 jsonb 等值：Go map 序列化键有序，用于替代
// 原先 SQL 里的 `credentials IS DISTINCT FROM $1::jsonb` 判定。
func credentialsJSONEqual(left, right map[string]any) bool {
	leftJSON, leftErr := json.Marshal(normalizeJSONMap(left))
	rightJSON, rightErr := json.Marshal(normalizeJSONMap(right))
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

// jsonSubvalueEqual 比较两个 JSON 子值是否等价（替代 jsonb 子键等值比较）。
func jsonSubvalueEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

// accountCredentialsFromEntity 返回账号的权威凭据：
// 密文优先（解密失败 fail-closed），否则回退历史明文列。
func accountCredentialsFromEntity(m *dbent.Account) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	ciphertext := derefString(m.CredentialsCipher)
	if strings.TrimSpace(ciphertext) != "" {
		credentials, err := decryptCredentialsCipher(ciphertext)
		if err != nil {
			logger.LegacyPrintf("repository.account",
				"[AtRest] credentials_cipher decrypt failed: account=%d err=%v", m.ID, err)
			return nil, fmt.Errorf("account %d credentials cannot be decrypted: %w", m.ID, err)
		}
		return credentials, nil
	}
	return copyJSONMap(normalizeJSONMap(m.Credentials)), nil
}

// accountCredentialsWriteState 读取账号当前是否已封存（用于 ent 更新路径）。
func accountCredentialsWriteState(ctx context.Context, client *dbent.Client, id int64) (bool, error) {
	sealed, err := client.Account.Query().
		Where(dbaccount.IDEQ(id)).
		Select(dbaccount.FieldCredentials).
		Only(ctx)
	if err != nil {
		return false, err
	}
	return IsSealedCredentials(sealed.Credentials), nil
}

// refreshAccountCredentialsCipher 在「只改了 credentials 明文列」的写路径之后，
// 同事务刷新 credentials_cipher。密文是读取的权威副本：不刷新会让这次结构键
// 修改在下一次读取时被密文覆盖（模型白名单自动同步、jsonb_set 类更新）。
func refreshAccountCredentialsCipher(ctx context.Context, exec sqlExecutor, id int64, mutate func(map[string]any)) error {
	rows, err := exec.QueryContext(ctx, `
		SELECT credentials, COALESCE(credentials_cipher, '')
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, id)
	if err != nil {
		return err
	}
	if !rows.Next() {
		rowsErr := rows.Err()
		_ = rows.Close()
		if rowsErr != nil {
			return rowsErr
		}
		return nil
	}
	var (
		plainJSON []byte
		cipher    string
	)
	scanErr := rows.Scan(&plainJSON, &cipher)
	_ = rows.Close()
	if scanErr != nil {
		return scanErr
	}
	credentials, err := decodeCredentialsStored(plainJSON, cipher)
	if err != nil {
		return err
	}
	mutate(credentials)
	ciphertext, err := credentialsCipherForStorage(credentials)
	if err != nil {
		return err
	}
	if ciphertext == "" {
		return nil
	}
	_, err = exec.ExecContext(ctx, `UPDATE accounts SET credentials_cipher = $1 WHERE id = $2`, ciphertext, id)
	return err
}
