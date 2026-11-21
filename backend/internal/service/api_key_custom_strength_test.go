//go:build unit

package service

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestValidateNewCustomKey(t *testing.T) {
	svc := &APIKeyService{cfg: &config.Config{}}
	svc.cfg.Default.APIKeyPrefix = "custom-prefix-"
	fixture := sha512.Sum512([]byte("deterministic test fixture, not a real credential"))
	maxLength := hex.EncodeToString(fixture[:])
	for _, tc := range []struct {
		name string
		key  string
		want error
	}{
		{name: "too short", key: "not-long", want: ErrAPIKeyTooShort},
		{name: "too long", key: maxLength + "Q", want: ErrAPIKeyTooLong},
		{name: "invalid characters", key: "disposable.fixture.with.dots", want: ErrAPIKeyInvalidChars},
		{name: "single character repetition", key: strings.Repeat("m", 20), want: ErrAPIKeyTooPredictable},
		{name: "pattern after configured prefix", key: svc.cfg.Default.APIKeyPrefix + strings.Repeat("nH8", 7), want: ErrAPIKeyTooPredictable},
		{name: "long repeat with one distinct byte", key: "sk-" + strings.Repeat("x", 44) + "t", want: ErrAPIKeyTooPredictable},
		{name: "known prefix is the whole value", key: "long-fixture-prefix-", want: ErrAPIKeyTooPredictable},
		{name: "valid minimum length", key: maxLength[:16]},
		{name: "valid configured prefix", key: svc.cfg.Default.APIKeyPrefix + maxLength[:32]},
		{name: "valid maximum length", key: maxLength},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validator := svc
			if tc.name == "known prefix is the whole value" {
				validator = &APIKeyService{cfg: &config.Config{}}
				validator.cfg.Default.APIKeyPrefix = tc.key
			}
			err := validator.ValidateCustomKey(tc.key)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
			require.NotContains(t, err.Error(), tc.key)
		})
	}
}

type customKeyValidationRepo struct {
	*createLimitAPIKeyRepoStub
	lookups int
}

func (r *customKeyValidationRepo) ExistsByKey(ctx context.Context, key string) (bool, error) {
	r.lookups++
	return r.createLimitAPIKeyRepoStub.ExistsByKey(ctx, key)
}

func TestAPIKeyServiceCreate_WeakCustomKeyDoesNotUseCreationBudget(t *testing.T) {
	repo, cache := newCreateLimitStubs()
	svc := newCreateLimitService(repo, cache, 0, 3)
	tracked := &customKeyValidationRepo{createLimitAPIKeyRepoStub: repo}
	svc.apiKeyRepo = tracked

	weak := strings.Repeat("v", 23)
	_, err := svc.Create(t.Context(), 7, CreateAPIKeyRequest{Name: "test fixture", CustomKey: &weak})
	require.ErrorIs(t, err, ErrAPIKeyTooPredictable)
	require.Zero(t, tracked.lookups)
	require.Empty(t, repo.created)
	require.Empty(t, cache.createCounts)

	strong := "tG3p_J7qL5mZ9n4X"
	accepted, err := svc.Create(t.Context(), 7, CreateAPIKeyRequest{Name: "test fixture", CustomKey: &strong})
	require.NoError(t, err)
	require.Equal(t, strong, accepted.Key)
	require.EqualValues(t, 7, accepted.UserID)
	require.Equal(t, 1, tracked.lookups)

	empty := ""
	var previous string
	for _, custom := range []*string{nil, &empty} {
		generated, err := svc.Create(t.Context(), 7, CreateAPIKeyRequest{Name: "test fixture", CustomKey: custom})
		require.NoError(t, err)
		require.NotEmpty(t, generated.Key)
		require.NotEqual(t, previous, generated.Key)
		previous = generated.Key
	}
	require.Len(t, repo.created, 3)
}
