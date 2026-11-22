//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 管理面出站入口的统一校验回归：公网通过；私网字面量、解析到私网、
// DNS 失败/超时一律拒绝；对外错误不包含解析细节。

func TestBackupTestS3ConnectionRejectsOutboundTargets(t *testing.T) {
	restore := SetAdminOutboundAllowPrivateForTest(false)
	defer restore()
	svc := newTestBackupService(newMockSettingRepo(), &mockDumper{}, newMockObjectStore())
	base := BackupS3Config{Bucket: "bucket", AccessKeyID: "ak", SecretAccessKey: "sk"}

	for name, endpoint := range map[string]string{
		"loopback":       "http://127.0.0.1:9000",
		"rfc1918":        "https://10.1.2.3:8443",
		"link-local":     "http://169.254.169.254",
		"cgnat-metadata": "http://100.100.100.200/latest/meta-data",
		"ipv6-loopback":  "https://[::1]:9000",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			cfg.Endpoint = endpoint
			err := svc.TestS3Connection(context.Background(), cfg)
			require.ErrorIs(t, err, ErrAdminOutboundTargetRejected)
			require.NotContains(t, err.Error(), "127.0.0.1")
			require.NotContains(t, err.Error(), "169.254")
		})
	}

	t.Run("resolved to private", func(t *testing.T) {
		stubOutboundResolver(t, "10.20.30.40")
		cfg := base
		cfg.Endpoint = "https://rebind.example.com"
		err := svc.TestS3Connection(context.Background(), cfg)
		require.ErrorIs(t, err, ErrAdminOutboundTargetRejected)
		require.NotContains(t, err.Error(), "10.20.30.40")
	})

	t.Run("dns failure is a rejection", func(t *testing.T) {
		stubOutboundResolverError(t)
		cfg := base
		cfg.Endpoint = "https://unresolvable.example.com"
		err := svc.TestS3Connection(context.Background(), cfg)
		require.ErrorIs(t, err, ErrAdminOutboundTargetRejected)
	})

	t.Run("public endpoint passes", func(t *testing.T) {
		stubOutboundResolver(t, "93.184.216.34")
		cfg := base
		cfg.Endpoint = "https://s3.example.com"
		require.NoError(t, svc.TestS3Connection(context.Background(), cfg))
	})

	t.Run("empty endpoint keeps the provider default", func(t *testing.T) {
		require.NoError(t, svc.TestS3Connection(context.Background(), base))
	})
}

func TestImageStorageTestConnectionRejectsOutboundTargets(t *testing.T) {
	restore := SetAdminOutboundAllowPrivateForTest(false)
	defer restore()
	svc, _, built := newImageStorageFixture(t, config.ImageStorageConfig{})
	base := ImageStorageSettings{
		Enabled: true, Bucket: "bucket", AccessKeyID: "ak", SecretAccessKey: "sk",
	}

	for name, endpoint := range map[string]string{
		"loopback":   "http://127.0.0.1:9000",
		"rfc1918":    "https://192.168.1.10:9000",
		"link-local": "http://169.254.169.254",
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			in.Endpoint = endpoint
			require.ErrorIs(t, svc.TestConnection(context.Background(), in), ErrAdminOutboundTargetRejected)
		})
	}

	t.Run("resolved to private", func(t *testing.T) {
		stubOutboundResolver(t, "10.1.2.3")
		in := base
		in.Endpoint = "https://rebind.example.com"
		require.ErrorIs(t, svc.TestConnection(context.Background(), in), ErrAdminOutboundTargetRejected)
	})

	t.Run("dns failure is a rejection", func(t *testing.T) {
		stubOutboundResolverError(t)
		in := base
		in.Endpoint = "https://unresolvable.example.com"
		require.ErrorIs(t, svc.TestConnection(context.Background(), in), ErrAdminOutboundTargetRejected)
	})

	t.Run("public endpoint passes", func(t *testing.T) {
		stubOutboundResolver(t, "93.184.216.34")
		in := base
		in.Endpoint = "https://s3.example.com"
		require.NoError(t, svc.TestConnection(context.Background(), in))
		require.Len(t, *built, 1)
	})
}

func TestContentModerationTestAPIKeysRejectsOutboundTargets(t *testing.T) {
	restore := SetAdminOutboundAllowPrivateForTest(false)
	defer restore()
	svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{}}, nil, nil, nil, nil, nil, nil, nil)

	for name, baseURL := range map[string]string{
		"loopback":   "http://127.0.0.1:8080",
		"rfc1918":    "http://10.0.0.5:8080",
		"link-local": "http://169.254.169.254",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{
				BaseURL: baseURL, APIKeys: []string{"sk-test"}, Prompt: "hello",
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "仅允许公网")
			require.NotContains(t, err.Error(), baseURL)
		})
	}

	t.Run("resolved to private", func(t *testing.T) {
		stubOutboundResolver(t, "10.1.2.3")
		_, err := svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{
			BaseURL: "https://rebind.example.com", APIKeys: []string{"sk-test"}, Prompt: "hello",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "仅允许公网")
	})

	t.Run("dns failure is a rejection", func(t *testing.T) {
		stubOutboundResolverError(t)
		_, err := svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{
			BaseURL: "https://unresolvable.example.com", APIKeys: []string{"sk-test"}, Prompt: "hello",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "仅允许公网")
	})

	t.Run("public base url passes validation", func(t *testing.T) {
		stubOutboundResolver(t, "93.184.216.34")
		// 没有可用 key 时校验通过后直接返回，不发起真实请求。
		result, err := svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{
			BaseURL: "https://api.example.com",
		})
		require.NoError(t, err)
		require.Empty(t, result.Items)
	})
}

func TestSMTPDialPolicyRejectsPrivateMetadataAndCGNAT(t *testing.T) {
	t.Cleanup(func() { SetSMTPAllowPrivateTargetsForTest(true) })
	SetSMTPAllowPrivateTargetsForTest(false)

	for name, address := range map[string]string{
		"loopback":       "127.0.0.1:587",
		"rfc1918":        "10.0.0.1:25",
		"link-local":     "169.254.169.254:80",
		"cgnat-metadata": "100.100.100.200:80",
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, smtpCheckDialAddr(address), ErrSMTPTargetBlocked)
		})
	}
	require.NoError(t, smtpCheckDialAddr("8.8.8.8:587"))
}
