package repository_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type installationSecretSettings struct {
	service.SettingRepository
	values map[string]string
}

func (r *installationSecretSettings) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *installationSecretSettings) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestBackupInstallationKeyEncryptsAndReloadsRealAESAcrossRestart(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ae", 32), EncryptionKeyPersistent: true}}
	settings := &installationSecretSettings{values: make(map[string]string)}
	firstEncryptor, err := repository.NewAESEncryptor(cfg)
	require.NoError(t, err)
	first := service.NewBackupService(settings, cfg, firstEncryptor, nil, nil)
	public, err := first.UpdateS3Config(ctx, service.BackupS3Config{Bucket: "test-bucket", AccessKeyID: "test-id", SecretAccessKey: "test-installation-s3-secret"})
	require.NoError(t, err)
	require.Empty(t, public.SecretAccessKey)
	raw := settings.values["backup_s3_config"]
	require.NotContains(t, raw, "test-installation-s3-secret")
	var stored service.BackupS3Config
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.NotEmpty(t, stored.SecretAccessKey)

	secondEncryptor, err := repository.NewAESEncryptor(cfg)
	require.NoError(t, err)
	restarted := service.NewBackupService(settings, cfg, secondEncryptor, nil, nil)
	// Omitting the secret exercises decrypt -> preserve -> re-encrypt after a
	// fresh service and encryptor are built with the bootstrapped installation key.
	_, err = restarted.UpdateS3Config(ctx, service.BackupS3Config{Bucket: "new-bucket", AccessKeyID: "test-id"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(settings.values["backup_s3_config"]), &stored))
	plain, err := secondEncryptor.Decrypt(stored.SecretAccessKey)
	require.NoError(t, err)
	require.Equal(t, "test-installation-s3-secret", plain)
	require.Equal(t, "new-bucket", stored.Bucket)
}
