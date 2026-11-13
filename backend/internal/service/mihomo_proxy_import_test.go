//go:build unit

package service_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const managedProxyCipherPrefix = "enc:managed:v1:"

// importFlowStore stands in for the entity layer: it keeps the raw stored row
// so a test can inspect the ciphertext that the ent MaxLen and column width see.
type importFlowStore struct {
	service.ProxyRepository
	row       *service.Proxy
	createErr error
}

func (s *importFlowStore) Create(_ context.Context, p *service.Proxy) error {
	if s.createErr != nil {
		return s.createErr
	}
	p.ID = 7
	row := *p
	s.row = &row
	return nil
}

func (s *importFlowStore) GetByID(context.Context, int64) (*service.Proxy, error) {
	if s.row == nil {
		return nil, errors.New("fixture row is missing")
	}
	row := *s.row
	return &row, nil
}

type importFlowAdmin struct {
	service.AdminService
	repo service.ProxyRepository
}

func (a *importFlowAdmin) CreateProxy(ctx context.Context, input *service.CreateProxyInput) (*service.Proxy, error) {
	p := &service.Proxy{
		Name:           input.Name,
		Protocol:       input.Protocol,
		Host:           input.Host,
		Port:           input.Port,
		Username:       input.Username,
		Password:       input.Password,
		Status:         service.StatusActive,
		FallbackMode:   input.FallbackMode,
		ExpiryWarnDays: input.ExpiryWarnDays,
	}
	if err := a.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

type importFlowBindings struct{}

func (importFlowBindings) WithHostLock(_ context.Context, _ string, run func() error) error {
	return run()
}
func (importFlowBindings) GetProxyID(context.Context, string, string) (int64, error) { return 0, nil }
func (importFlowBindings) Save(context.Context, string, string, int64) error         { return nil }

type importFlowKernel struct {
	bindings []mihomo.StaticNodeBinding
}

func (k *importFlowKernel) BindStaticNodes(context.Context, []string) ([]mihomo.StaticNodeBinding, error) {
	return k.bindings, nil
}
func (*importFlowKernel) DeactivateStaticNodes(context.Context, []string) error { return nil }
func (*importFlowKernel) HostID() string                                        { return "host-fixture" }

func newImportFlowService(t *testing.T, store *importFlowStore) (*service.MihomoProxyService, service.SecretEncryptor) {
	t.Helper()
	encryptor, err := repository.NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}})
	require.NoError(t, err)
	repo := service.WrapEncryptedManagedProxyRepository(store, encryptor)
	return service.NewMihomoProxyService(&importFlowAdmin{repo: repo}, repo, importFlowBindings{}), encryptor
}

func managedImportBinding(nodeName string) mihomo.StaticNodeBinding {
	return mihomo.StaticNodeBinding{
		Name:        nodeName,
		DisplayName: "🇨🇳 中文节点 " + strings.Repeat("长名称", 40),
		Host:        "198.51.100.10",
		Username:    "mh_" + nodeName,
		// A managed password is hex.EncodeToString of 32 random bytes.
		Password: strings.Repeat("ab", 32),
		Port:     32001,
		New:      true,
	}
}

// A generated managed password is 64 hex characters. Encrypted it is 139 bytes
// at rest, which used to fail both the entity MaxLen(100) validator and the
// VARCHAR(100) column even though the managed listener bind succeeded.
func TestManagedProxyImportPersistsFullLengthEncryptedPassword(t *testing.T) {
	store := &importFlowStore{}
	svc, encryptor := newImportFlowService(t, store)

	binding := managedImportBinding("node-3e36dd845c440144")
	kernel := &importFlowKernel{bindings: []mihomo.StaticNodeBinding{binding}}

	result, err := svc.CreateMihomoNodeProxies(context.Background(), kernel, []string{binding.Name})
	require.NoError(t, err)
	require.Empty(t, result.Failed)
	require.Len(t, result.Created, 1)

	created := result.Created[0]
	require.Equal(t, binding.Password, created.Password)
	// Long CJK/emoji display names are byte-truncated into proxies.name's limit.
	require.LessOrEqual(t, len(created.Name), 100)
	require.True(t, utf8.ValidString(created.Name))
	require.True(t, strings.HasPrefix(created.Name, "Mihomo · "))

	require.NotNil(t, store.row)
	require.NotEqual(t, binding.Password, store.row.Password)
	require.True(t, strings.HasPrefix(store.row.Password, managedProxyCipherPrefix))
	// len("enc:managed:v1:") + base64(12-byte nonce + 64-byte ciphertext + 16-byte tag)
	require.Equal(t, 139, len(store.row.Password))
	require.Greater(t, len(store.row.Password), 100, "the previous VARCHAR(100)/MaxLen(100) limit rejected this ciphertext")
	require.LessOrEqual(t, len(store.row.Password), 255, "ciphertext must fit the widened column")

	decoded, err := encryptor.Decrypt(strings.TrimPrefix(store.row.Password, managedProxyCipherPrefix))
	require.NoError(t, err)
	require.Equal(t, binding.Password, decoded)
}

type importFlowLogHandler struct {
	mu      *sync.Mutex
	records *[]slog.Record
}

func (h importFlowLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h importFlowLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, record.Clone())
	return nil
}
func (h importFlowLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h importFlowLogHandler) WithGroup(string) slog.Handler      { return h }

// The import result hides the real cause behind a fixed message; the original
// error has to reach the logs.
func TestManagedProxyImportLogsOriginalRegistrationError(t *testing.T) {
	var mu sync.Mutex
	var records []slog.Record
	previous := slog.Default()
	slog.SetDefault(slog.New(importFlowLogHandler{mu: &mu, records: &records}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	registrationErr := errors.New("ent: validator failed for field \"Proxy.password\": value is greater than the required length")
	store := &importFlowStore{createErr: registrationErr}
	svc, _ := newImportFlowService(t, store)

	binding := managedImportBinding("node-3e36dd845c440144")
	kernel := &importFlowKernel{bindings: []mihomo.StaticNodeBinding{binding}}
	result, err := svc.CreateMihomoNodeProxies(context.Background(), kernel, []string{binding.Name})
	require.NoError(t, err)
	require.Len(t, result.Failed, 1)
	require.Equal(t, "could not register encrypted managed proxy", result.Failed[0].Reason)

	mu.Lock()
	defer mu.Unlock()

	var registration *slog.Record
	for i := range records {
		if records[i].Message == "managed_proxy_registration_failed" {
			registration = &records[i]
			break
		}
	}
	require.NotNil(t, registration, "the original registration error was not logged")
	attrs := map[string]string{}
	registration.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.String()
		return true
	})
	require.Equal(t, binding.Name, attrs["node"])
	require.Contains(t, attrs["error"], registrationErr.Error())
}
