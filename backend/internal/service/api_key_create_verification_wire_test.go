//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// wire 用 ProvideAPIKeyVerificationService 装配：漏掉注入偏好存储这一行，所有账号就会悄悄退回 auto
// （用户设的 on / off 都不生效），所以装配本身要有测试把关。
func TestProvideAPIKeyVerificationServiceWiresThePreferenceStore(t *testing.T) {
	ctx := context.Background()
	user := cvUser(cvPasswordOnly)
	users := &apiKeyVerifyUserRepoStub{user: user}
	prefs := newCVPrefs(APIKeyCreateVerificationOn)

	svc := ProvideAPIKeyVerificationService(users, nil, nil, nil, nil, newAPIKeyVerifyStoreStub(), prefs)

	factors, err := svc.Factors(ctx, cvUserID)
	require.NoError(t, err)
	require.Equal(t, APIKeyCreateVerificationOn, factors.CreateVerificationMode)
	require.True(t, factors.CreateVerificationRequired, "the stored on must be honoured by the provided service")
	_, err = svc.VerifyForCreate(ctx, cvUserID, nil)
	require.ErrorIs(t, err, ErrAPIKeyVerificationRequired)

	change, err := svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOn, nil)
	require.NoError(t, err)
	require.False(t, change.Changed)
}
