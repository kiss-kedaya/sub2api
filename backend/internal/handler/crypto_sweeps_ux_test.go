package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Everything here is a made-up fixture: addresses are repeated digits and the
// "personal data" is an obviously fake mailbox.
const fixtureLeakEmail = "fixture-leak@example.test"

func ownersFixtureAddress(digit byte) string {
	return "0x" + strings.Repeat(string(digit), 40)
}

type fakeSweepPager struct {
	records []cryptopay.SweepRecord
	err     error
	calls   int
	chain   string
	limit   int
	before  int64
}

func (f *fakeSweepPager) ListSweepRecordsPage(_ context.Context, chain string, limit int, beforeID int64) ([]cryptopay.SweepRecord, error) {
	f.calls++
	f.chain, f.limit, f.before = chain, limit, beforeID
	return f.records, f.err
}

type fakeOwnerResolver struct {
	owners map[string]cryptopay.AddressOwner
	err    error
	calls  int
	raw    []string
}

func (f *fakeOwnerResolver) LookupAddressOwners(_ context.Context, addresses []string) (map[string]cryptopay.AddressOwner, error) {
	f.calls++
	f.raw = append([]string(nil), addresses...)
	return f.owners, f.err
}

func ginGET(target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c, w
}

// captureLogs routes both slog and the standard logger into one buffer.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previousSlog := slog.Default()
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(&buf)
	t.Cleanup(func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
	return &buf
}

func TestSweepRecordsQueryDefaultsKeepTheOldBehaviour(t *testing.T) {
	pager := &fakeSweepPager{}
	c, w := ginGET("/api/v1/admin/crypto/sweep/records")
	serveSweepRecords(c, pager)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, pager.calls)
	require.Equal(t, "", pager.chain)
	require.Equal(t, 100, pager.limit)
	require.Zero(t, pager.before)
}

func TestSweepRecordsQueryPassesChainLimitAndCursorThrough(t *testing.T) {
	pager := &fakeSweepPager{}
	c, w := ginGET("/api/v1/admin/crypto/sweep/records?chain=bsc&limit=25&before_id=900")
	serveSweepRecords(c, pager)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "bsc", pager.chain)
	require.Equal(t, 25, pager.limit)
	require.Equal(t, int64(900), pager.before)
}

func TestSweepRecordsQueryLimitBoundaries(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int
		ok    bool
	}{
		{"limit=1", 1, true},
		{"limit=200", 200, true},
		{"limit=", 100, true}, // an empty value is "not provided"
		{"limit=0", 0, false},
		{"limit=201", 0, false},
		{"limit=-3", 0, false},
		{"limit=abc", 0, false},
		{"limit=10.5", 0, false},
		{"before_id=0", 0, false},
		{"before_id=-1", 0, false},
		{"before_id=abc", 0, false},
		{"before_id=", 100, true},
		{"before_id=99999999999", 100, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			pager := &fakeSweepPager{}
			c, w := ginGET("/api/v1/admin/crypto/sweep/records?" + tc.query)
			serveSweepRecords(c, pager)
			if !tc.ok {
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				require.Zero(t, pager.calls, "a malformed paging parameter never reaches the database")
				return
			}
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, tc.want, pager.limit)
		})
	}
}

func TestSweepRecordsResponseStaysAnArrayWithNewFields(t *testing.T) {
	pager := &fakeSweepPager{records: []cryptopay.SweepRecord{
		{ID: 5, Chain: "bsc", FromAddress: ownersFixtureAddress('a'), Token: "0xtoken", TokenSymbol: "USDT",
			Amount: "1.5", Status: "swept", CreatedAt: "2026-11-05T00:00:00Z", UpdatedAt: "2026-11-05T01:00:00Z"},
	}}
	c, w := ginGET("/api/v1/admin/crypto/sweep/records")
	serveSweepRecords(c, pager)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	row := body.Data[0]
	for _, key := range []string{"id", "chain", "from_address", "to_address", "token", "amount",
		"gas_tx_hash", "sweep_tx_hash", "status", "error", "created_at"} {
		require.Contains(t, row, key, "existing field %q must stay", key)
	}
	require.Equal(t, "USDT", row["token_symbol"])
	require.Equal(t, "2026-11-05T01:00:00Z", row["updated_at"])
}

func TestSweepRecordsEmptyPageIsAnEmptyArray(t *testing.T) {
	c, w := ginGET("/api/v1/admin/crypto/sweep/records")
	serveSweepRecords(c, &fakeSweepPager{records: nil})
	require.JSONEq(t, `{"data":[]}`, w.Body.String())
}

func TestSweepRecordsFailureUsesTheEnvelopeWithoutRawCause(t *testing.T) {
	c, w := ginGET("/api/v1/admin/crypto/sweep/records")
	serveSweepRecords(c, &fakeSweepPager{err: errors.New("dial tcp 10.0.0.1:5432: connect: refused")})
	require.GreaterOrEqual(t, w.Code, 500)
	require.NotContains(t, w.Body.String(), "10.0.0.1")
}

func TestAdminSweepRecordsWithoutServiceIsUnavailable(t *testing.T) {
	c, w := ginGET("/api/v1/admin/crypto/sweep/records")
	(&CryptoHandler{}).AdminSweepRecords(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "crypto channel unavailable")
}

func TestAddressOwnersPassesRawQueryValuesToTheLookup(t *testing.T) {
	a := ownersFixtureAddress('a')
	resolver := &fakeOwnerResolver{owners: map[string]cryptopay.AddressOwner{
		a: {UserID: 7, Username: "fixture-name", Email: "fixture@example.test", Deleted: false},
	}}
	c, w := ginGET("/api/v1/admin/crypto/address-owners?address=" + strings.ToUpper(a[:2]) + a[2:] + "&address=" + a + "&address=junk")
	serveAddressOwners(c, resolver)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, resolver.calls)
	require.Len(t, resolver.raw, 3, "validation and de-duplication belong to the lookup, not the transport")
	require.JSONEq(t, fmt.Sprintf(`{"data":{"owners":{%q:{"user_id":7,"username":"fixture-name","email":"fixture@example.test","deleted":false}}}}`, a), w.Body.String())
}

func TestAddressOwnersNilResultIsAnEmptyObject(t *testing.T) {
	c, w := ginGET("/api/v1/admin/crypto/address-owners")
	serveAddressOwners(c, &fakeOwnerResolver{owners: nil})
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"data":{"owners":{}}}`, w.Body.String())
}

func TestAddressOwnersTooManyAddressesIsABadRequest(t *testing.T) {
	for _, err := range []error{cryptopay.ErrTooManyOwnerAddresses, fmt.Errorf("wrapped: %w", cryptopay.ErrTooManyOwnerAddresses)} {
		c, w := ginGET("/api/v1/admin/crypto/address-owners")
		serveAddressOwners(c, &fakeOwnerResolver{err: err})
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "too many addresses")
	}
}

func TestAddressOwnersFailureIsUniformAndLeaksNothing(t *testing.T) {
	logs := captureLogs(t)
	causes := []error{
		cryptopay.ErrOwnerLookupFailed,
		errors.New("pq: could not read row for " + fixtureLeakEmail),
		fmt.Errorf("scan: %w", errors.New(`converting driver.Value type string ("`+fixtureLeakEmail+`") to a int64`)),
	}
	var first string
	for i, cause := range causes {
		c, w := ginGET("/api/v1/admin/crypto/address-owners?address=" + ownersFixtureAddress('a'))
		serveAddressOwners(c, &fakeOwnerResolver{err: cause})
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.NotContains(t, w.Body.String(), fixtureLeakEmail)
		require.NotContains(t, w.Body.String(), "pq:")
		if i == 0 {
			first = w.Body.String()
		}
		require.Equal(t, first, w.Body.String(), "every failure produces the same text")
	}
	require.NotEmpty(t, logs.String(), "a failure should still leave a trace for the operator")
	require.NotContains(t, logs.String(), fixtureLeakEmail)
	require.NotContains(t, logs.String(), "example.test")
}

func TestAddressOwnersSuccessWritesNothingToTheLogs(t *testing.T) {
	logs := captureLogs(t)
	a := ownersFixtureAddress('a')
	c, w := ginGET("/api/v1/admin/crypto/address-owners?address=" + a)
	serveAddressOwners(c, &fakeOwnerResolver{owners: map[string]cryptopay.AddressOwner{
		a: {UserID: 1, Username: "fixture-name", Email: fixtureLeakEmail},
	}})
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), fixtureLeakEmail, "the admin response is where the email is allowed")
	require.NotContains(t, logs.String(), fixtureLeakEmail)
	require.NotContains(t, logs.String(), "fixture-name")
}

func TestAdminAddressOwnersWithoutServiceIsUnavailable(t *testing.T) {
	c, w := ginGET("/api/v1/admin/crypto/address-owners?address=" + ownersFixtureAddress('a'))
	(&CryptoHandler{}).AdminAddressOwners(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "crypto channel unavailable")
}

// A zero-value Service has no database. These inputs are answered before any
// query would run, which is exactly what the real handler does for them.
func TestAdminAddressOwnersAnswersEdgeInputsBeforeTouchingTheDatabase(t *testing.T) {
	h := &CryptoHandler{service: &cryptopay.Service{}}

	t.Run("no addresses", func(t *testing.T) {
		c, w := ginGET("/api/v1/admin/crypto/address-owners")
		h.AdminAddressOwners(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.JSONEq(t, `{"data":{"owners":{}}}`, w.Body.String())
	})

	t.Run("only invalid addresses are ignored", func(t *testing.T) {
		c, w := ginGET("/api/v1/admin/crypto/address-owners?address=nope&address=0x12&address=" + ownersFixtureAddress('a')[:41])
		h.AdminAddressOwners(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.JSONEq(t, `{"data":{"owners":{}}}`, w.Body.String())
	})

	t.Run("more than 100 distinct addresses", func(t *testing.T) {
		var query []string
		for i := 0; i < cryptopay.MaxOwnerLookupAddresses+1; i++ {
			query = append(query, fmt.Sprintf("address=0x%040x", i+1))
		}
		c, w := ginGET("/api/v1/admin/crypto/address-owners?" + strings.Join(query, "&"))
		h.AdminAddressOwners(c)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "too many addresses")
	})

	t.Run("exactly 100 distinct addresses plus repeats is accepted by validation", func(t *testing.T) {
		// Reaches the (absent) database, so the answer is the uniform 500 and
		// not a 400: the cap counts distinct addresses only.
		var query []string
		for i := 0; i < cryptopay.MaxOwnerLookupAddresses; i++ {
			query = append(query, fmt.Sprintf("address=0x%040x", i+1))
		}
		query = append(query, fmt.Sprintf("address=0x%040X", 1), fmt.Sprintf("address=0x%040x", 2))
		c, w := ginGET("/api/v1/admin/crypto/address-owners?" + strings.Join(query, "&"))
		h.AdminAddressOwners(c)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	})
}
