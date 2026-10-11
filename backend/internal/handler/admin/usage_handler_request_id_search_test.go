package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageRequestIDSearchValidation(t *testing.T) {
	for _, tc := range []struct {
		id, dates string
		status    int
	}{
		{"", "", 200}, {"  =client:UUID  ", "", 200}, {"=upstream%_", "", 200}, {"=550e8400-e29b-41d4-a716-446655440000", "", 200},
		{"^abc", "", 200}, {"fragment", "", 200}, {"%_", "", 400}, {"ab", "", 400}, {"^ab", "", 400},
		{"=", "", 400}, {"^", "", 400}, {"=client:", "", 400},
		{"%_", "&start_date=2026-10-10&end_date=2026-10-10", 200},
		{"ab", "&start_date=2026-10-01&end_date=2026-10-10", 400},
		{"ab", "&start_date=2026-10-10", 400}, {"ab", "&start_date=2026-10-10T00:00:00&end_date=2026-10-11T00:00:00", 200},
	} {
		t.Run(tc.id+tc.dates, func(t *testing.T) {
			repo := &adminUsageRepoCapture{}
			router := newAdminUsageRequestTypeTestRouter(repo)
			req := httptest.NewRequest(http.MethodGet, "/admin/usage?request_id="+url.QueryEscape(tc.id)+tc.dates, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
		})
	}
}

func TestAdminUsageInflightRequestIDModes(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"=client:abc", []string{"abc"}}, {"^ABC", []string{"abc", "abcdef"}},
		{"=upstream-other", []string{}}, {"==abc", []string{"=abc"}},
		{"=literal%_", []string{"literal%_"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			rows := []service.UsageInflightSnapshot{{RequestID: "abc"}, {RequestID: "abcdef"}, {RequestID: "=abc"}, {RequestID: "literal%_"}}
			got := []string{}
			for _, row := range filterAdminUsageInflightRequestID(rows, tc.input) {
				got = append(got, row.RequestID)
			}
			require.Equal(t, tc.want, got)
		})
	}
}
