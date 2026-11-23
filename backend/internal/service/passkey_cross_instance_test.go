package service_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const (
	crossInstanceRPID    = "sub2api.example.com"
	crossInstanceOrigin  = "https://sub2api.example.com"
	crossInstanceUserID  = int64(7)
	crossInstanceSession = 5 * time.Minute
)

type crossInstanceUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *crossInstanceUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

type crossInstancePasskeyRepo struct {
	service.PasskeyRepository
	handles map[int64][]byte
	records []service.PasskeyCredentialRecord
	created []*service.PasskeyCredentialRecord
	nextID  int64
}

func newCrossInstancePasskeyRepo() *crossInstancePasskeyRepo {
	return &crossInstancePasskeyRepo{handles: map[int64][]byte{}}
}

func (r *crossInstancePasskeyRepo) EnsureUserHandle(_ context.Context, userID int64, candidate []byte) ([]byte, error) {
	if handle, ok := r.handles[userID]; ok {
		return handle, nil
	}
	r.handles[userID] = append([]byte(nil), candidate...)
	return r.handles[userID], nil
}

func (r *crossInstancePasskeyRepo) GetUserHandle(_ context.Context, userID int64) ([]byte, error) {
	handle, ok := r.handles[userID]
	if !ok {
		return nil, service.ErrPasskeyNotFound
	}
	return handle, nil
}

func (r *crossInstancePasskeyRepo) ListByUserID(_ context.Context, userID int64) ([]service.PasskeyCredentialRecord, error) {
	result := make([]service.PasskeyCredentialRecord, 0, len(r.records))
	for _, record := range r.records {
		if record.UserID == userID {
			result = append(result, record)
		}
	}
	return result, nil
}

func (r *crossInstancePasskeyRepo) Create(_ context.Context, record *service.PasskeyCredentialRecord) (*service.PasskeyCredentialRecord, error) {
	r.nextID++
	created := *record
	created.ID = r.nextID
	created.CreatedAt = time.Now().UTC()
	created.UpdatedAt = created.CreatedAt
	r.records = append(r.records, created)
	r.created = append(r.created, &created)
	return &created, nil
}

func newCrossInstancePasskeyService(t *testing.T, repo service.PasskeyRepository, sessions service.PasskeySessionStore, users service.UserRepository) *service.PasskeyService {
	t.Helper()
	svc, err := service.NewPasskeyService(&config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          crossInstanceRPID,
		RPOrigins:     []string{crossInstanceOrigin},
	}}, repo, sessions, users)
	require.NoError(t, err)
	return svc
}

func newCrossInstancePasskeyFixture(t *testing.T) (*miniredis.Miniredis, *service.PasskeyService, *service.PasskeyService, *crossInstancePasskeyRepo, *service.User) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	repo := newCrossInstancePasskeyRepo()
	user := &service.User{
		ID:       crossInstanceUserID,
		Email:    "boss@example.com",
		Username: "boss",
		Status:   service.StatusActive,
	}
	require.NoError(t, user.SetPassword("correct-password"))
	userRepo := &crossInstanceUserRepo{user: user}

	// 两个 service 实例共享同一套 Redis / DB 依赖，模拟负载均衡后的两台承载实例。
	serviceA := newCrossInstancePasskeyService(t, repo, repository.NewPasskeySessionStore(client), userRepo)
	serviceB := newCrossInstancePasskeyService(t, repo, repository.NewPasskeySessionStore(client), userRepo)
	return mini, serviceA, serviceB, repo, user
}

func passkeyCredentialRequest(t *testing.T, payload []byte) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/user/passkeys/register/finish", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	return request
}

// TestPasskeyRegistrationCompletesAcrossInstances 验证：begin 落在 A 实例、finish 落在
// B 实例时，只要两边共享同一个 Redis，challenge 就能读到并成功落库。
func TestPasskeyRegistrationCompletesAcrossInstances(t *testing.T) {
	_, instanceA, instanceB, repo, user := newCrossInstancePasskeyFixture(t)
	ctx := context.Background()

	creation, token, err := instanceA.BeginRegistration(ctx, user.ID, "correct-password")
	require.NoError(t, err)
	require.NotNil(t, creation)
	require.Contains(t, token, ".registration.7.")
	require.Empty(t, repo.created)

	payload := buildPasskeyRegistrationCredential(t, creation.Response.Challenge.String(), crossInstanceOrigin, crossInstanceRPID)
	summary, err := instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.NoError(t, err, "cross-instance finish must succeed when both instances share Redis")
	require.NotNil(t, summary)
	require.Equal(t, "ios", summary.Name)
	require.Len(t, repo.created, 1)
	require.EqualValues(t, user.ID, repo.created[0].UserID)
	require.NotEmpty(t, repo.created[0].Credential.ID)
}

// TestPasskeyChallengeCannotBeReplayed 验证 challenge 一次性：同一个 session_token
// 二次 finish 必须失败，且不会产生第二条凭据。
func TestPasskeyChallengeCannotBeReplayed(t *testing.T) {
	_, instanceA, instanceB, repo, user := newCrossInstancePasskeyFixture(t)
	ctx := context.Background()

	creation, token, err := instanceA.BeginRegistration(ctx, user.ID, "correct-password")
	require.NoError(t, err)
	payload := buildPasskeyRegistrationCredential(t, creation.Response.Challenge.String(), crossInstanceOrigin, crossInstanceRPID)
	_, err = instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.NoError(t, err)

	_, err = instanceA.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.ErrorIs(t, err, service.ErrPasskeySessionExpired)
	require.Len(t, repo.created, 1)
}

// TestPasskeyChallengeExpiresWhileTrying 验证 5 分钟 TTL：challenge 过期后 finish
// 返回明确的 PASSKEY_CHALLENGE_EXPIRED，而不是静默失败。
func TestPasskeyChallengeExpiresWhileTrying(t *testing.T) {
	mini, instanceA, instanceB, repo, user := newCrossInstancePasskeyFixture(t)
	ctx := context.Background()

	creation, token, err := instanceA.BeginRegistration(ctx, user.ID, "correct-password")
	require.NoError(t, err)
	payload := buildPasskeyRegistrationCredential(t, creation.Response.Challenge.String(), crossInstanceOrigin, crossInstanceRPID)

	mini.FastForward(crossInstanceSession + time.Second)

	_, err = instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.ErrorIs(t, err, service.ErrPasskeySessionExpired)
	require.Empty(t, repo.created)
}

// TestPasskeyRegistrationClassifiesWebAuthnFailures 验证校验失败的错误码分类：
// origin / RP ID / challenge 各自可区分，便于前端提示与运维定位。
func TestPasskeyRegistrationClassifiesWebAuthnFailures(t *testing.T) {
	_, instanceA, instanceB, repo, user := newCrossInstancePasskeyFixture(t)
	ctx := context.Background()

	begin := func() (challenge string, token string) {
		creation, token, err := instanceA.BeginRegistration(ctx, user.ID, "correct-password")
		require.NoError(t, err)
		return creation.Response.Challenge.String(), token
	}

	challenge, token := begin()
	payload := buildPasskeyRegistrationCredential(t, challenge, "https://evil.example", crossInstanceRPID)
	_, err := instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.ErrorIs(t, err, service.ErrPasskeyOriginMismatch)

	challenge, token = begin()
	payload = buildPasskeyRegistrationCredential(t, challenge, crossInstanceOrigin, "other.example.com")
	_, err = instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.ErrorIs(t, err, service.ErrPasskeyRPIDMismatch)

	challenge, token = begin()
	wrongChallenge := "AAAA" + challenge
	payload = buildPasskeyRegistrationCredential(t, wrongChallenge, crossInstanceOrigin, crossInstanceRPID)
	_, err = instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, payload))
	require.ErrorIs(t, err, service.ErrPasskeyChallengeMismatch)

	_, token = begin()
	_, err = instanceB.FinishRegistration(ctx, user.ID, token, "ios", passkeyCredentialRequest(t, []byte(`{"id":"AAAA","type":"public-key"}`)))
	require.ErrorIs(t, err, service.ErrPasskeyVerify)

	require.Empty(t, repo.created)
}

// ---------- 测试用「虚拟认证器」：构造格式合法的 none attestation ----------

func buildPasskeyRegistrationCredential(t *testing.T, challenge, origin, rpID string) []byte {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	credentialID := make([]byte, 32)
	_, err = rand.Read(credentialID)
	require.NoError(t, err)

	rpIDHash := sha256.Sum256([]byte(rpID))
	authData := make([]byte, 0, 32+1+4+16+2+len(credentialID)+80)
	authData = append(authData, rpIDHash[:]...)
	authData = append(authData, 0x45)                // UP | UV | AT
	authData = append(authData, 0, 0, 0, 0)          // signCount
	authData = append(authData, make([]byte, 16)...) // AAGUID
	authData = append(authData, byte(len(credentialID)>>8), byte(len(credentialID)))
	authData = append(authData, credentialID...)
	authData = append(authData, cborEncodeCOSEKey(t, &privateKey.PublicKey)...)

	attestationObject := cborEncodeAttestationObject(authData)
	clientData, err := json.Marshal(map[string]any{
		"type":        "webauthn.create",
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	require.NoError(t, err)

	payload, err := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(credentialID),
		"rawId": base64.RawURLEncoding.EncodeToString(credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": base64.RawURLEncoding.EncodeToString(attestationObject),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"transports":        []string{"internal"},
		},
	})
	require.NoError(t, err)
	return payload
}

func cborEncodeHead(major byte, n int) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	case n < 65536:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	default:
		return []byte{major<<5 | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

func cborEncodeBytes(value []byte) []byte {
	return append(cborEncodeHead(2, len(value)), value...)
}

func cborEncodeText(value string) []byte {
	return append(cborEncodeHead(3, len(value)), []byte(value)...)
}

func cborEncodeUint(value uint64) []byte {
	return cborEncodeHead(0, int(value))
}

func cborEncodeNegInt(value int64) []byte {
	return cborEncodeHead(1, int(-1-value))
}

// cborEncodeAttestationObject 编码 CBOR map{"fmt":"none","attStmt":{},"authData":<bytes>}。
func cborEncodeAttestationObject(authData []byte) []byte {
	out := cborEncodeHead(5, 3)
	out = append(out, cborEncodeText("fmt")...)
	out = append(out, cborEncodeText("none")...)
	out = append(out, cborEncodeText("attStmt")...)
	out = append(out, cborEncodeHead(5, 0)...)
	out = append(out, cborEncodeText("authData")...)
	out = append(out, cborEncodeBytes(authData)...)
	return out
}

// cborEncodeCOSEKey 编码 ES256 的 COSE_Key（EC2 / P-256）：
// {1: 2, 3: -7, -1: 1, -2: x, -3: y}
func cborEncodeCOSEKey(t *testing.T, publicKey *ecdsa.PublicKey) []byte {
	t.Helper()
	x := publicKey.X.FillBytes(make([]byte, 32))
	y := publicKey.Y.FillBytes(make([]byte, 32))

	out := cborEncodeHead(5, 5)
	out = append(out, cborEncodeUint(1)...)
	out = append(out, cborEncodeUint(2)...) // kty: EC2
	out = append(out, cborEncodeUint(3)...)
	out = append(out, cborEncodeNegInt(-7)...) // alg: ES256
	out = append(out, cborEncodeNegInt(-1)...)
	out = append(out, cborEncodeUint(1)...) // crv: P-256
	out = append(out, cborEncodeNegInt(-2)...)
	out = append(out, cborEncodeBytes(x)...)
	out = append(out, cborEncodeNegInt(-3)...)
	out = append(out, cborEncodeBytes(y)...)
	return out
}
