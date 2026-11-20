//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

var paymentConfigTestKey = []byte("0123456789abcdef0123456789abcdef")

func newPaymentConfigEncryptionTestService(client *dbent.Client, key []byte) *PaymentConfigService {
	return &PaymentConfigService{entClient: client, encryptionKey: key}
}

func createEncryptionTestProviderInstance(t *testing.T, svc *PaymentConfigService) *dbent.PaymentProviderInstance {
	t.Helper()
	inst, err := svc.CreateProviderInstance(context.Background(), CreateProviderInstanceRequest{
		ProviderKey:    payment.TypeStripe,
		Name:           "encryption-test",
		SupportedTypes: []string{payment.TypeStripe},
		// Disabled: the provider constructor is not under test here, only the
		// at-rest representation of the merchant credentials.
		Enabled: false,
		Config: map[string]string{
			"secretKey":      "sk_live_super_secret",
			"webhookSecret":  "whsec_super_secret",
			"publishableKey": "pk_live_public",
		},
	})
	require.NoError(t, err)
	return inst
}

func TestCreateProviderInstanceStoresEncryptedConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, paymentConfigTestKey)

	created := createEncryptionTestProviderInstance(t, svc)
	saved, err := client.PaymentProviderInstance.Get(ctx, created.ID)
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(saved.Config, payment.ProviderConfigCipherPrefix),
		"stored provider config must carry the v2 ciphertext prefix")
	require.True(t, payment.IsEncryptedProviderConfig(saved.Config))
	require.NotContains(t, saved.Config, "sk_live_super_secret")
	require.NotContains(t, saved.Config, "whsec_super_secret")

	cfg, err := svc.decryptConfig(saved.Config)
	require.NoError(t, err)
	require.Equal(t, "sk_live_super_secret", cfg["secretKey"])
	require.Equal(t, "whsec_super_secret", cfg["webhookSecret"])
	require.Equal(t, "pk_live_public", cfg["publishableKey"])
}

func TestProviderConfigDecryptFailsClosedOnWrongKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, paymentConfigTestKey)
	created := createEncryptionTestProviderInstance(t, svc)

	saved, err := client.PaymentProviderInstance.Get(ctx, created.ID)
	require.NoError(t, err)

	wrongKeySvc := newPaymentConfigEncryptionTestService(client, []byte("ffffffffffffffffffffffffffffffff"))
	cfg, decryptErr := wrongKeySvc.decryptConfig(saved.Config)
	require.Error(t, decryptErr)
	require.Nil(t, cfg, "undecryptable config must not be projected as an empty config")
	require.NotContains(t, decryptErr.Error(), "sk_live_super_secret")
	require.NotContains(t, decryptErr.Error(), "whsec_super_secret")
}

func TestProviderConfigMissingKeyFailsClosedOnWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, nil)

	_, err := svc.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey:    payment.TypeStripe,
		Name:           "no-key",
		SupportedTypes: []string{payment.TypeStripe},
		Enabled:        false,
		Config:         map[string]string{"secretKey": "sk_live_must_not_be_plaintext"},
	})
	require.Error(t, err)
	require.Equal(t, "PAYMENT_ENCRYPTION_KEY_UNAVAILABLE", infraerrors.FromError(err).Reason)

	count, countErr := client.PaymentProviderInstance.Query().Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, count, "a failed encryption must not leave a plaintext row behind")
}

func TestDecryptConfigReadsLegacyAndPlaintextRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, paymentConfigTestKey)

	legacyCiphertext, err := payment.Encrypt(`{"secretKey":"sk_live_legacy"}`, paymentConfigTestKey)
	require.NoError(t, err)

	for _, tc := range []struct {
		name   string
		stored string
	}{
		{name: "legacy ciphertext", stored: legacyCiphertext},
		{name: "pre-encryption plaintext", stored: `{"secretKey":"sk_live_plaintext"}`},
	} {
		inst, seedErr := client.PaymentProviderInstance.Create().
			SetProviderKey(payment.TypeStripe).
			SetName(tc.name).
			SetConfig(tc.stored).
			SetSupportedTypes(payment.TypeStripe).
			SetEnabled(false).
			Save(ctx)
		require.NoError(t, seedErr)

		cfg, decryptErr := svc.decryptConfig(inst.Config)
		require.NoError(t, decryptErr)
		require.NotEmpty(t, cfg["secretKey"])
	}
}

func TestUpdateProviderInstanceReEncryptsPlaintextConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, paymentConfigTestKey)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("legacy-plaintext-update").
		SetConfig(`{"secretKey":"sk_live_legacy","webhookSecret":"whsec_legacy"}`).
		SetSupportedTypes(payment.TypeStripe).
		SetEnabled(false).
		Save(ctx)
	require.NoError(t, err)

	_, err = svc.UpdateProviderInstance(ctx, inst.ID, UpdateProviderInstanceRequest{
		Config: map[string]string{"publishableKey": "pk_live_updated"},
	})
	require.NoError(t, err)

	saved, err := client.PaymentProviderInstance.Get(ctx, inst.ID)
	require.NoError(t, err)
	require.True(t, payment.IsEncryptedProviderConfig(saved.Config))
	require.NotContains(t, saved.Config, "sk_live_legacy")

	cfg, err := svc.decryptConfig(saved.Config)
	require.NoError(t, err)
	require.Equal(t, "sk_live_legacy", cfg["secretKey"])
	require.Equal(t, "whsec_legacy", cfg["webhookSecret"])
	require.Equal(t, "pk_live_updated", cfg["publishableKey"])
}

func TestMigrateLegacyProviderConfigsEncryptsPlaintextRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, paymentConfigTestKey)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("legacy-migrate").
		SetConfig(`{"secretKey":"sk_live_migrate"}`).
		SetSupportedTypes(payment.TypeStripe).
		SetEnabled(false).
		Save(ctx)
	require.NoError(t, err)
	require.False(t, payment.IsEncryptedProviderConfig(inst.Config))

	svc.MigrateLegacyProviderConfigs(ctx)

	migrated, err := client.PaymentProviderInstance.Get(ctx, inst.ID)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(migrated.Config, payment.ProviderConfigCipherPrefix))
	require.NotContains(t, migrated.Config, "sk_live_migrate")

	cfg, err := svc.decryptConfig(migrated.Config)
	require.NoError(t, err)
	require.Equal(t, "sk_live_migrate", cfg["secretKey"])

	// Idempotent: a second run must not rewrite already-encrypted rows.
	svc.MigrateLegacyProviderConfigs(ctx)
	again, err := client.PaymentProviderInstance.Get(ctx, inst.ID)
	require.NoError(t, err)
	require.Equal(t, migrated.Config, again.Config)
}

func TestListProviderInstancesWithConfigMigratesLegacyPlaintext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, paymentConfigTestKey)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("admin-list-legacy").
		SetConfig(`{"secretKey":"sk_live_admin_list","publishableKey":"pk_live_public"}`).
		SetSupportedTypes(payment.TypeStripe).
		SetEnabled(false).
		Save(ctx)
	require.NoError(t, err)

	responses, err := svc.ListProviderInstancesWithConfig(ctx)
	require.NoError(t, err)
	require.Len(t, responses, 1)
	// Sensitive fields stay masked in the admin DTO; the public field survives.
	require.Equal(t, "pk_live_public", responses[0].Config["publishableKey"])
	require.NotContains(t, responses[0].Config, "secretKey")

	saved, err := client.PaymentProviderInstance.Get(ctx, inst.ID)
	require.NoError(t, err)
	require.True(t, payment.IsEncryptedProviderConfig(saved.Config),
		"admin read must lazily encrypt a legacy plaintext row")
	require.NotContains(t, saved.Config, "sk_live_admin_list")
}

func TestMigrateLegacyProviderConfigsWithoutKeyIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := newPaymentConfigEncryptionTestService(client, nil)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("legacy-no-key").
		SetConfig(`{"secretKey":"sk_live_still_plaintext"}`).
		SetSupportedTypes(payment.TypeStripe).
		SetEnabled(false).
		Save(ctx)
	require.NoError(t, err)

	svc.MigrateLegacyProviderConfigs(ctx)

	unchanged, err := client.PaymentProviderInstance.Get(ctx, inst.ID)
	require.NoError(t, err)
	require.Equal(t, inst.Config, unchanged.Config)
}
