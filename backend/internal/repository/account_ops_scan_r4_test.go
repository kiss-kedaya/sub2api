package repository

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type r4ScanAccountRepo struct {
	service.AccountRepository
	reader  *accountRepository
	mu      sync.Mutex
	account service.Account
}

func (r *r4ScanAccountRepo) ListAccountOpsSnapshots(ctx context.Context, ids []int64) ([]service.Account, error) {
	return r.reader.ListAccountOpsSnapshots(ctx, ids)
}
func (r *r4ScanAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := r.account
	return &copy, nil
}
func (r *r4ScanAccountRepo) UpdateUpstreamBillingProbeSnapshot(_ context.Context, _ *service.Account, snapshot *service.UpstreamBillingProbeSnapshot, _ *float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.account.Extra = map[string]any{service.UpstreamBillingProbeExtraKey: snapshot}
	return nil
}

type r4ScanObservation struct {
	event service.AccountOpsEvent
	state string
}
type r4ScanEvents struct {
	service.AccountOpsRepository
	config string
	seen   chan r4ScanObservation
}

func (r *r4ScanEvents) ReadAccountOpsConfig(context.Context) (string, error) { return r.config, nil }
func (r *r4ScanEvents) SuppressDisabled(context.Context, service.AccountOpsConfig) error {
	return nil
}
func (r *r4ScanEvents) Claim(context.Context) (*service.AccountOpsEvent, error) { return nil, nil }
func (r *r4ScanEvents) ThresholdEventEligible(context.Context, *service.AccountOpsEvent) (bool, error) {
	return true, nil
}
func (r *r4ScanEvents) ObserveThreshold(_ context.Context, e service.AccountOpsEvent, state string, _ time.Time, _, _ bool) error {
	select {
	case r.seen <- r4ScanObservation{e, state}:
	default:
	}
	return nil
}

type r4ScanHTTP struct{ calls atomic.Int32 }

func (u *r4ScanHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	status, body := http.StatusNotFound, `{}`
	if req.URL.Path == "/v1/usage" {
		u.calls.Add(1)
		status, body = http.StatusOK, `{"isValid":true,"remaining":2,"unit":"USD"}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}
func (u *r4ScanHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestR4RealProjectionScannerRefreshAndThreshold(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	extra := `{"upstream_billing_provider":"sub2api","upstream_billing_user_id":7,"upstream_billing_probe_enabled":false,"upstream_billing_probe":{"status":"ok","upstream_kind":"sub2api"}}`
	for i := 0; i < 3; i++ {
		mock.ExpectQuery(`SELECT .*"accounts"\."status", "accounts"\."schedulable".*FROM "accounts"`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "type", "credentials", "extra", "session_window_end", "status", "schedulable"}).AddRow(int64(9101), "synthetic-active", service.PlatformOpenAI, service.AccountTypeAPIKey, []byte(`{"api_key":"synthetic-r4-key","base_url":"https://example.invalid"}`), []byte(extra), nil, service.StatusActive, true))
	}
	a := service.Account{ID: 9101, Name: "synthetic-active", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "synthetic-r4-key", "base_url": "https://example.invalid"}}
	require.NoError(t, json.Unmarshal([]byte(extra), &a.Extra))
	accounts := &r4ScanAccountRepo{reader: newAccountRepositoryWithSQL(client, db, nil), account: a}
	cfg := service.AccountOpsConfig{Enabled: true, Recipient: "synthetic@example.invalid", CooldownMinutes: 60, GroupsInitialized: true, Groups: []service.AccountOpsGroup{{ID: "manual-r4-wallet", Name: "Synthetic wallet", Provider: "sub2api", Site: "https://example.invalid", AccountIDs: []int64{a.ID}}}, BalanceThresholds: []service.AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 5, Unit: "USD"}}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	events := &r4ScanEvents{config: string(raw), seen: make(chan r4ScanObservation, 1)}
	svc := service.NewAccountOpsService(nil, events, nil)
	svc.SetNotificationDependencies(accounts, nil, false, "UTC")
	upstream := &r4ScanHTTP{}
	testService := service.NewAccountTestService(accounts, nil, nil, nil, nil, upstream, &config.Config{}, nil)
	probe := service.NewUpstreamBillingProbeService(accounts, testService, nil)
	probe.SetAccountOpsService(svc)
	svc.Start()
	defer svc.Stop()
	select {
	case observed := <-events.seen:
		require.Equal(t, "active", observed.state)
		require.NotNil(t, observed.event.Details)
		require.Equal(t, 2.0, *observed.event.Details.Balance)
		require.Equal(t, int32(1), upstream.calls.Load())
	case <-time.After(20 * time.Second):
		t.Fatal("the production projection -> loadThresholdAccounts -> refresh -> threshold scanner never observed a balance")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
