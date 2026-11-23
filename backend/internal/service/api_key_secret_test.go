package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// secretTestAPIKeyRepo 只实现封存服务需要的 GetByID（归属校验），
// 其余方法由内嵌接口兜底。
type secretTestAPIKeyRepo struct {
	APIKeyRepository
	keys map[int64]*APIKey
}

func (r *secretTestAPIKeyRepo) GetByID(_ context.Context, id int64) (*APIKey, error) {
	if key, ok := r.keys[id]; ok && key != nil {
		return key, nil
	}
	return nil, ErrAPIKeyNotFound
}

// secretTestStore 是内存版 api_key_secrets。
type secretTestStore struct {
	APIKeySecretRepository
	rows    map[int64]*APIKeySecret
	upserts int
}

func newSecretTestStore() *secretTestStore {
	return &secretTestStore{rows: map[int64]*APIKeySecret{}}
}

func (s *secretTestStore) Upsert(_ context.Context, secret *APIKeySecret) error {
	if s.rows == nil {
		s.rows = map[int64]*APIKeySecret{}
	}
	copied := *secret
	copied.KDF = append(json.RawMessage(nil), secret.KDF...)
	s.rows[secret.APIKeyID] = &copied
	s.upserts++
	return nil
}

func (s *secretTestStore) GetByAPIKeyID(_ context.Context, apiKeyID, userID int64) (*APIKeySecret, error) {
	row, ok := s.rows[apiKeyID]
	if !ok || row.UserID != userID {
		return nil, ErrAPIKeySecretNotFound
	}
	copied := *row
	copied.KDF = append(json.RawMessage(nil), row.KDF...)
	return &copied, nil
}

func (s *secretTestStore) DeleteByAPIKeyID(_ context.Context, apiKeyID, userID int64) error {
	if row, ok := s.rows[apiKeyID]; ok && row.UserID == userID {
		delete(s.rows, apiKeyID)
	}
	return nil
}

func (s *secretTestStore) ListPresenceByAPIKeyIDs(_ context.Context, userID int64, apiKeyIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	for _, id := range apiKeyIDs {
		if row, ok := s.rows[id]; ok && row.UserID == userID {
			out[id] = row.AuthFactor
		}
	}
	return out, nil
}

func validSealInput(t *testing.T) APIKeySecretSealInput {
	t.Helper()
	kdf, err := json.Marshal(APIKeySecretKDF{
		Algo:       APIKeySecretKDFAlgoPBKDF2SHA256,
		Iterations: 310000,
		Salt:       base64.StdEncoding.EncodeToString([]byte("01234567")),
	})
	require.NoError(t, err)
	return APIKeySecretSealInput{
		Version: APIKeySecretSupportedVersion,
		KDF:     kdf,
		Sealed:  base64.StdEncoding.EncodeToString(make([]byte, apiKeySecretMinSealedBytes)),
	}
}

func newSecretTestService(keys ...*APIKey) (*APIKeySecretService, *secretTestStore) {
	byID := map[int64]*APIKey{}
	for _, key := range keys {
		byID[key.ID] = key
	}
	store := newSecretTestStore()
	return NewAPIKeySecretService(&secretTestAPIKeyRepo{keys: byID}, store), store
}

func TestAPIKeySecretServiceSealStoresOnlyCiphertext(t *testing.T) {
	key := &APIKey{ID: 11, UserID: 7, KeyPrefix: "sk-1234"}
	svc, store := newSecretTestService(key)
	input := validSealInput(t)

	got, err := svc.Seal(context.Background(), 7, key.ID, input)
	require.NoError(t, err)
	require.Equal(t, key.ID, got.ID)

	row, ok := store.rows[key.ID]
	require.True(t, ok)
	require.Equal(t, int64(7), row.UserID)
	require.Equal(t, APIKeySecretSupportedVersion, row.Version)
	require.Equal(t, input.Sealed, row.Sealed)
	require.JSONEq(t, string(input.KDF), string(row.KDF))
	// 领域模型里没有明文 key 字段：密文行只可能有 kdf/sealed。
	require.NotContains(t, string(row.KDF), "sk-1234")
}

func TestAPIKeySecretServiceSealIsIdempotent(t *testing.T) {
	key := &APIKey{ID: 12, UserID: 7}
	svc, store := newSecretTestService(key)
	first := validSealInput(t)
	_, err := svc.Seal(context.Background(), 7, key.ID, first)
	require.NoError(t, err)

	// 用户改登录密码后重新封装：同一行被覆盖，不产生第二份密文。
	second := validSealInput(t)
	kdf, err := json.Marshal(APIKeySecretKDF{
		Algo:       APIKeySecretKDFAlgoPBKDF2SHA256,
		Iterations: 400000,
		Salt:       base64.StdEncoding.EncodeToString([]byte("abcdefgh")),
	})
	require.NoError(t, err)
	second.KDF = kdf
	second.Sealed = base64.StdEncoding.EncodeToString([]byte("sealed-after-password-change-000001"))
	gotKey, err := svc.Seal(context.Background(), 7, key.ID, second)
	require.NoError(t, err)
	require.Equal(t, key.ID, gotKey.ID)

	require.Len(t, store.rows, 1)
	require.Equal(t, 2, store.upserts)
	row := store.rows[key.ID]
	require.Equal(t, second.Sealed, row.Sealed)
	require.JSONEq(t, string(second.KDF), string(row.KDF))

	loaded, loadedKey, err := svc.Get(context.Background(), 7, key.ID)
	require.NoError(t, err)
	require.Equal(t, second.Sealed, loaded.Sealed)
	require.Equal(t, key.ID, loadedKey.ID)
}

func TestAPIKeySecretServiceSealRejectsInvalidPayload(t *testing.T) {
	key := &APIKey{ID: 13, UserID: 7}
	svc, store := newSecretTestService(key)

	salt := func(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }
	sealed := func(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }
	validKDF := func(iterations int, saltValue string) json.RawMessage {
		raw, err := json.Marshal(APIKeySecretKDF{
			Algo:       APIKeySecretKDFAlgoPBKDF2SHA256,
			Iterations: iterations,
			Salt:       saltValue,
		})
		require.NoError(t, err)
		return raw
	}

	cases := []struct {
		name  string
		input APIKeySecretSealInput
	}{
		{"unsupported version", APIKeySecretSealInput{Version: 2, KDF: validKDF(310000, salt(16)), Sealed: sealed(32)}},
		{"missing kdf", APIKeySecretSealInput{Version: 1, Sealed: sealed(32)}},
		{"kdf not object", APIKeySecretSealInput{Version: 1, KDF: json.RawMessage(`"nope"`), Sealed: sealed(32)}},
		{"bad algo", APIKeySecretSealInput{Version: 1, KDF: json.RawMessage(`{"algo":"scrypt","iterations":310000,"salt":"` + salt(16) + `"}`), Sealed: sealed(32)}},
		{"iterations too small", APIKeySecretSealInput{Version: 1, KDF: validKDF(apiKeySecretMinIterations-1, salt(16)), Sealed: sealed(32)}},
		{"iterations too large", APIKeySecretSealInput{Version: 1, KDF: validKDF(apiKeySecretMaxIterations+1, salt(16)), Sealed: sealed(32)}},
		{"salt too short", APIKeySecretSealInput{Version: 1, KDF: validKDF(310000, salt(apiKeySecretMinSaltBytes-1)), Sealed: sealed(32)}},
		{"salt too long", APIKeySecretSealInput{Version: 1, KDF: validKDF(310000, salt(apiKeySecretMaxSaltBytes+1)), Sealed: sealed(32)}},
		{"salt not base64", APIKeySecretSealInput{Version: 1, KDF: validKDF(310000, "not base64!!"), Sealed: sealed(32)}},
		{"sealed too short", APIKeySecretSealInput{Version: 1, KDF: validKDF(310000, salt(16)), Sealed: sealed(apiKeySecretMinSealedBytes - 1)}},
		{"sealed too long", APIKeySecretSealInput{Version: 1, KDF: validKDF(310000, salt(16)), Sealed: sealed(apiKeySecretMaxSealedBytes + 1)}},
		{"sealed not base64", APIKeySecretSealInput{Version: 1, KDF: validKDF(310000, salt(16)), Sealed: "%%%"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Seal(context.Background(), 7, key.ID, tc.input)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrAPIKeySecretInvalid), "want API_KEY_SECRET_INVALID, got %v", err)
		})
	}
	require.Empty(t, store.rows, "invalid payload must never reach storage")
}

func TestAPIKeySecretServiceRejectsForeignKey(t *testing.T) {
	ownerKey := &APIKey{ID: 14, UserID: 7}
	svc, store := newSecretTestService(ownerKey)
	input := validSealInput(t)
	ctx := context.Background()

	// 本人可以封存。
	_, err := svc.Seal(ctx, 7, ownerKey.ID, input)
	require.NoError(t, err)

	// 别人拿同一个 key_id 做封存 / 读取 / 删除，一律按 404 处理（不泄露存在性）。
	foreignInput := validSealInput(t)
	foreignInput.Sealed = base64.StdEncoding.EncodeToString([]byte("foreign-attempt-ciphertext-00000001"))
	_, err = svc.Seal(ctx, 8, ownerKey.ID, foreignInput)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	_, _, err = svc.Get(ctx, 8, ownerKey.ID)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	_, err = svc.Delete(ctx, 8, ownerKey.ID)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	// 原密文没有被越权覆盖或删除。
	row := store.rows[ownerKey.ID]
	require.NotNil(t, row)
	require.Equal(t, input.Sealed, row.Sealed)
}

func TestAPIKeySecretServiceGetNotFound(t *testing.T) {
	key := &APIKey{ID: 15, UserID: 7}
	svc, _ := newSecretTestService(key)

	_, _, err := svc.Get(context.Background(), 7, key.ID)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAPIKeySecretNotFound), "want API_KEY_SECRET_NOT_FOUND, got %v", err)
}

func TestAPIKeySecretServiceDeleteIsIdempotent(t *testing.T) {
	key := &APIKey{ID: 16, UserID: 7}
	svc, store := newSecretTestService(key)
	ctx := context.Background()
	_, err := svc.Seal(ctx, 7, key.ID, validSealInput(t))
	require.NoError(t, err)

	_, err = svc.Delete(ctx, 7, key.ID)
	require.NoError(t, err)
	require.Empty(t, store.rows)

	// 重复删除（用户重复点“关闭后续可查看”）不报错。
	_, err = svc.Delete(ctx, 7, key.ID)
	require.NoError(t, err)
}

func TestAPIKeySecretServicePresence(t *testing.T) {
	keyA := &APIKey{ID: 21, UserID: 7}
	keyB := &APIKey{ID: 22, UserID: 7}
	keyC := &APIKey{ID: 23, UserID: 7}
	svc, _ := newSecretTestService(keyA, keyB, keyC)
	ctx := context.Background()
	_, err := svc.Seal(ctx, 7, keyA.ID, validSealInput(t))
	require.NoError(t, err)
	_, err = svc.Seal(ctx, 7, keyC.ID, validSealInput(t))
	require.NoError(t, err)

	present, err := svc.Presence(ctx, 7, []int64{keyA.ID, keyB.ID, keyC.ID})
	require.NoError(t, err)
	require.Contains(t, present, keyA.ID)
	require.NotContains(t, present, keyB.ID)
	require.Contains(t, present, keyC.ID)

	// 空列表不查库。
	present, err = svc.Presence(ctx, 7, nil)
	require.NoError(t, err)
	require.Empty(t, present)
}
