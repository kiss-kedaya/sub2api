package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// prefixEncryptor is a tiny reversible SecretEncryptor used to prove the stored
// response body is never persisted in plaintext. It is not real crypto.
type prefixEncryptor struct {
	prefix string
	fail   error
}

func (e prefixEncryptor) Encrypt(plaintext string) (string, error) {
	if e.fail != nil {
		return "", e.fail
	}
	return e.prefix + base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

func (e prefixEncryptor) Decrypt(ciphertext string) (string, error) {
	if e.fail != nil {
		return "", e.fail
	}
	if !strings.HasPrefix(ciphertext, e.prefix) {
		return "", errors.New("ciphertext does not carry the expected prefix")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, e.prefix))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func newStoredResponseTestOptions(key string) IdempotencyExecuteOptions {
	return IdempotencyExecuteOptions{
		Scope:                  "test.stored_response",
		Method:                 "POST",
		Route:                  "/test/stored-response",
		ActorScope:             "admin:1",
		RequireKey:             true,
		IdempotencyKey:         key,
		Payload:                map[string]any{"request": key},
		MaxStoredResponseLen:   512 * 1024,
		PreserveStoredResponse: true,
	}
}

func storedResponseBody(t *testing.T, repo *inMemoryIdempotencyRepo, opts IdempotencyExecuteOptions) string {
	t.Helper()
	keyHash := HashIdempotencyKey(opts.IdempotencyKey)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	record := repo.data[repo.key(opts.Scope, keyHash)]
	require.NotNil(t, record)
	require.NotNil(t, record.ResponseBody)
	return *record.ResponseBody
}

func TestIdempotencyCoordinator_SealsStoredResponseAtRest(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	coordinator.SetStoredResponseEncryptor(prefixEncryptor{prefix: "cipher|"})

	opts := newStoredResponseTestOptions("seal-at-rest")
	plaintextPayload := map[string]any{
		"codes": []any{"REDEEM-AAAA-BBBB", "REDEEM-CCCC-DDDD"},
	}
	execCount := 0
	exec := func(ctx context.Context) (any, error) {
		execCount++
		return plaintextPayload, nil
	}

	first, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.False(t, first.Replayed)
	require.Equal(t, 1, execCount)

	stored := storedResponseBody(t, repo, opts)
	require.True(t, strings.HasPrefix(stored, storedResponseCipherPrefix), "stored body must be marked as sealed ciphertext")
	require.NotContains(t, stored, "REDEEM-AAAA-BBBB", "redeemable plaintext must not be written to the idempotency journal")
	require.True(t, strings.HasPrefix(strings.TrimPrefix(stored, storedResponseCipherPrefix), "cipher|"))

	// The replay path must still return exactly the original response.
	second, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, 1, execCount, "replay must not re-execute business logic")
	require.Equal(t, plaintextPayload, second.Data)
}

func TestIdempotencyCoordinator_ReplaysLegacyPlaintextStoredResponse(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()

	// A record written before this change has no cipher prefix.
	legacy := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	opts := newStoredResponseTestOptions("legacy-plaintext")
	exec := func(ctx context.Context) (any, error) {
		return map[string]any{"codes": []any{"LEGACY-CODE"}}, nil
	}
	_, err := legacy.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(storedResponseBody(t, repo, opts), storedResponseCipherPrefix))

	// A rolling upgrade installs the encryptor; old rows must remain replayable.
	upgraded := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	upgraded.SetStoredResponseEncryptor(prefixEncryptor{prefix: "cipher|"})
	replayed, err := upgraded.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, map[string]any{"codes": []any{"LEGACY-CODE"}}, replayed.Data)
}

func TestIdempotencyCoordinator_FailsClosedWhenSealedResponseCannotBeDecrypted(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	opts := newStoredResponseTestOptions("undecryptable")
	exec := func(ctx context.Context) (any, error) {
		return map[string]any{"secret": "value"}, nil
	}

	writer := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	writer.SetStoredResponseEncryptor(prefixEncryptor{prefix: "cipher|"})
	_, err := writer.Execute(context.Background(), opts, exec)
	require.NoError(t, err)

	// Wrong key / corrupted key material: the JSON must never be replayed.
	reader := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	reader.SetStoredResponseEncryptor(prefixEncryptor{prefix: "other|"})
	result, err := reader.Execute(context.Background(), opts, exec)
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))
}

func TestIdempotencyCoordinator_FailsClosedWhenSealingFails(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	coordinator.SetStoredResponseEncryptor(prefixEncryptor{prefix: "cipher|", fail: errors.New("kms down")})

	opts := newStoredResponseTestOptions("seal-error")
	exec := func(ctx context.Context) (any, error) {
		return map[string]any{"secret": "value"}, nil
	}
	result, err := coordinator.Execute(context.Background(), opts, exec)
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))
	require.Equal(t, "", storedResponseBodyOrEmpty(t, repo, opts), "nothing may be persisted when sealing fails")
}

func storedResponseBodyOrEmpty(t *testing.T, repo *inMemoryIdempotencyRepo, opts IdempotencyExecuteOptions) string {
	t.Helper()
	keyHash := HashIdempotencyKey(opts.IdempotencyKey)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	record := repo.data[repo.key(opts.Scope, keyHash)]
	if record == nil || record.ResponseBody == nil {
		return ""
	}
	return *record.ResponseBody
}

func TestIdempotencyCoordinator_NilEncryptorKeepsPlaintextBehaviour(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())

	opts := newStoredResponseTestOptions("nil-encryptor")
	exec := func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	}
	_, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)

	stored := storedResponseBody(t, repo, opts)
	require.False(t, strings.HasPrefix(stored, storedResponseCipherPrefix))
	require.Contains(t, stored, `"ok":true`)
}
