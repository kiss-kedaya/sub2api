package requestcapture

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func smallTestRecorder() *SmallRecorder {
	r := &SmallRecorder{queue: make(chan *SmallHandle, SmallQueueLimit), handles: map[*SmallHandle]struct{}{}}
	r.enabled.Store(true)
	r.ready.Store(true)
	return r
}
func smallTestExchange() SmallExchange {
	return SmallExchange{StartedAt: time.Now().UTC(), Method: "POST", Path: "/v1/responses", UserID: 11, APIKeyID: 12, GroupID: 13, RequestHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseHeaders: http.Header{"Content-Type": {"text/event-stream"}}, RequestBody: []byte("{ \"model\":\"text-test\", \"input\":\"hello\",\"signature\":\"business-signature\",\"key\":\"business-value\" }\n"), ResponseBody: []byte("data: {\"text\":\"hello\",\"token\":\"business-field\"}\n\ndata: [DONE]\n\n"), Status: 200, Usage: SmallUsage{InputTokens: 999, OutputTokens: 5, Model: "text-test", RequestID: "synthetic-request", ActualCost: "0.00000001"}}
}
func smallCompleted(t *testing.T, r *SmallRecorder, u SmallUsage) *SmallHandle {
	t.Helper()
	h := r.Begin("POST", "/v1/responses", 11, 12, 13)
	require.NotNil(t, h)
	h.ClientRequest([]byte(`{"model":"text-test","input":"hello"}`), http.Header{"Content-Type": {"application/json"}})
	h.Response([]byte("data: hello\n\n"), http.Header{"Content-Type": {"text/event-stream"}})
	h.Finish(200, nil, false)
	h.Usage(u)
	return h
}
func TestSmallRetentionTokenBoundaryAndUnknownUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		u      SmallUsage
		queued bool
		reason string
	}{
		{"zero", SmallUsage{}, false, "usage_unknown"},
		{"1", SmallUsage{InputTokens: 1}, true, ""},
		{"999", SmallUsage{InputTokens: 999}, true, ""},
		{"1000", SmallUsage{InputTokens: 1000}, false, "input_not_small"},
		{"cached999", SmallUsage{InputTokens: 99, CacheReadTokens: 800, CacheCreationTokens: 100}, true, ""},
		{"cached1000", SmallUsage{InputTokens: 100, CacheReadTokens: 800, CacheCreationTokens: 100}, false, "input_not_small"},
		{"unknown", SmallUsage{}, false, "usage_unknown"},
		{"negative", SmallUsage{InputTokens: -1}, false, "usage_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := smallTestRecorder()
			h := smallCompleted(t, r, tc.u)
			require.Equal(t, tc.queued, len(r.queue) == 1)
			require.Equal(t, tc.reason, h.reason)
		})
	}
}
func TestSmallRetentionDisabledHasNoAllocationsOrLocks(t *testing.T) {
	r := smallTestRecorder()
	r.ApplyEnabled(false)
	r.mu.Lock()
	defer r.mu.Unlock()
	allocs := testing.AllocsPerRun(1000, func() { require.Nil(t, r.Begin("POST", "/v1/responses", 11, 12, 13)) })
	require.Zero(t, allocs)
	require.Zero(t, r.active.Load())
}
func TestSmallRetentionQueueAndCopyBoundsNeverBlock(t *testing.T) {
	r := smallTestRecorder()
	for i := 0; i < SmallQueueLimit; i++ {
		smallCompleted(t, r, SmallUsage{InputTokens: 1})
	}
	start := time.Now()
	h := smallCompleted(t, r, SmallUsage{InputTokens: 1})
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, "queue_full", h.reason)
	require.True(t, h.released)
	r = smallTestRecorder()
	h = r.Begin("POST", "/v1/responses", 11, 12, 13)
	h.ClientRequest(make([]byte, SmallRequestLimit+1), nil)
	require.Equal(t, "request_too_large", h.reason)
	require.Nil(t, h.exchange.RequestBody)
	r = smallTestRecorder()
	h = r.Begin("POST", "/v1/responses", 11, 12, 13)
	h.ClientRequest([]byte(`{}`), nil)
	h.Response(make([]byte, SmallResponseLimit), nil)
	h.Response([]byte{1}, nil)
	require.Equal(t, "response_too_large", h.reason)
	require.Nil(t, h.exchange.ResponseBody)
}
func TestSmallRetentionPendingUsageAndGeneration(t *testing.T) {
	r := smallTestRecorder()
	h := r.Begin("POST", "/v1/messages", 11, 12, 13)
	h.ClientRequest([]byte(`{}`), nil)
	h.Finish(500, nil, false)
	r.expirePending(time.Now().Add(3 * time.Minute))
	require.True(t, h.released)
	require.Equal(t, "usage_unknown", h.reason)
	h = r.Begin("POST", "/v1/messages", 11, 12, 13)
	h.ClientRequest([]byte(`{}`), nil)
	r.Invalidate()
	h.Usage(SmallUsage{InputTokens: 1})
	require.True(t, h.released)
	require.Equal(t, "disabled", h.reason)
}
func TestSmallRetentionBodyFidelityCredentialsAndStableHash(t *testing.T) {
	e := smallTestExchange()
	e.Path += "?key=synthetic-secret&normal=keep&X-Amz-Signature=synthetic-signature"
	e.RequestHeaders["Authorization"] = []string{"Bearer synthetic-secret"}
	e.RequestHeaders["Cookie"] = []string{"synthetic-cookie"}
	e.RequestHeaders["X-List"] = []string{"first", "second"}
	e.RequestHeaders["X-Request-Id"] = []string{"first-id"}
	e.ResponseHeaders["Set-Cookie"] = []string{"synthetic-cookie"}
	p, reason := prepareSmallExchange(e, func(name string) bool { return name == "authorization" })
	require.Empty(t, reason)
	for _, blob := range []smallBlob{p.RequestHeaderBlob, p.ResponseHeaderBlob} {
		decoded, err := decodeSmallBlob(blob.Codec, blob.Payload)
		require.NoError(t, err)
		require.NotContains(t, string(decoded), "synthetic-secret")
		require.NotContains(t, string(decoded), "synthetic-cookie")
	}
	request, err := decodeSmallBlob(p.RequestBodyBlob.Codec, p.RequestBodyBlob.Payload)
	require.NoError(t, err)
	require.Equal(t, e.RequestBody, request)
	response, err := decodeSmallBlob(p.ResponseBodyBlob.Codec, p.ResponseBodyBlob.Payload)
	require.NoError(t, err)
	// The shared SSE filter can normalize framing whitespace without changing the payload.
	require.JSONEq(t, `{"text":"hello","token":"business-field"}`, string(bytes.TrimPrefix(bytes.SplitN(response, []byte("\n"), 2)[0], []byte("data:"))))
	require.Contains(t, string(response), "data: [DONE]\n\n")
	require.Equal(t, []string{"first", "second"}, map[string][]string(p.RequestHeaders)["x-list"])
	require.NotContains(t, p.Path, "synthetic-secret")
	require.NotContains(t, p.Path, "synthetic-signature")
	e.RequestHeaders.Set("X-Request-Id", "second-id")
	e.ResponseHeaders.Set("Date", "tomorrow")
	p2, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	require.Equal(t, p.Fingerprint, p2.Fingerprint)
	require.Equal(t, p.ResponseFingerprint, p2.ResponseFingerprint)
	command := SmallCurl(p.Method, p.Path, p.RequestHeaders, request)
	require.NotContains(t, command, "synthetic-secret")
	require.Contains(t, command, "https://example.invalid")
	require.Contains(t, command, "business-signature")
	require.NotContains(t, command, "x-request-id:")
}
func TestSmallRetentionFiltersStructuredCredentialsBeforeHashing(t *testing.T) {
	e := smallTestExchange()
	e.RequestBody = []byte(`{"model":"m","api_key":"request-fixture","input":[{"client_secret":{"value":"nested-fixture"},"signature":"protocol-signature","key":"business-key","token":"business-token"}]}`)
	e.ResponseHeaders.Set("Content-Type", "text/event-stream")
	e.ResponseBody = []byte("event: response.created\ndata: {\"access_token\":\"response-fixture\",\"usage\":{\"output_tokens\":17}}\n\n" +
		"event: response.completed\ndata: {\"refresh_token\":\"second-event-fixture\",\"type\":\"response.completed\"}\n\n")
	p, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	request, err := decodeSmallBlob(p.RequestBodyBlob.Codec, p.RequestBodyBlob.Payload)
	require.NoError(t, err)
	response, err := decodeSmallBlob(p.ResponseBodyBlob.Codec, p.ResponseBodyBlob.Payload)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"m","api_key":"[REDACTED]","input":[{"client_secret":"[REDACTED]","signature":"protocol-signature","key":"business-key","token":"business-token"}]}`, string(request))
	require.NotContains(t, string(response), "response-fixture")
	require.NotContains(t, string(response), "second-event-fixture")
	require.Contains(t, string(response), `"output_tokens":17`)
	require.Contains(t, string(response), "event: response.completed\n")
	require.Contains(t, p.ReplayReasons, "request_credentials_redacted")
	require.NotContains(t, SmallCurl(p.Method, p.Path, p.RequestHeaders, request), "request-fixture")
	before := p.Fingerprint
	e.RequestBody = bytes.ReplaceAll(e.RequestBody, []byte("request-fixture"), []byte("rotated-fixture"))
	p, reason = prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	require.Equal(t, before, p.Fingerprint)
}

func TestSmallRetentionBodyURLsPreserveBusinessParameters(t *testing.T) {
	e := smallTestExchange()
	e.RequestBody = []byte(`{"signature":"https://example.invalid/callback?signature=business-signature&key=business-key"}`)
	plain, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	require.Equal(t, e.RequestBody, plain.RequestBody)
	require.NotContains(t, plain.ReplayReasons, "request_credentials_redacted")
	e.RequestBody = []byte(`{"target":"https://example.invalid/callback?signature=business-signature&access_token=url-fixture"}`)
	p, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	require.NotContains(t, string(p.RequestBody), "url-fixture")
	require.Contains(t, string(p.RequestBody), "signature=business-signature")
	require.Contains(t, p.ReplayReasons, "request_credentials_redacted")
}

func TestSmallRetentionAcceptsStandardSSELineEndings(t *testing.T) {
	for name, separator := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r"} {
		t.Run(name, func(t *testing.T) {
			e := smallTestExchange()
			e.ResponseHeaders.Set("Content-Type", "text/event-stream; charset=utf-8")
			e.ResponseBody = []byte("\xef\xbb\xbf" + strings.Join([]string{"event: response.completed", "data: {", "data: \"type\":\"response.completed\",", "data: \"password\":\"multipart-fixture\"}", "", ""}, separator))
			p, reason := prepareSmallExchange(e, nil)
			require.Empty(t, reason)
			require.NotContains(t, string(p.ResponseBody), "multipart-fixture")
			require.Contains(t, string(p.ResponseBody), "[REDACTED]")
			require.Contains(t, string(p.ResponseBody), "event: response.completed\n")
			require.NotContains(t, p.ReplayReasons, "stream_terminal_missing")
		})
	}
}

func TestSmallRetentionFiltersMislabelledJSONAndSkipsInvalidBody(t *testing.T) {
	e := smallTestExchange()
	e.ResponseHeaders.Set("Content-Type", "text/plain")
	e.ResponseBody = []byte(`{"password":"response-fixture","answer":"ordinary text"}`)
	p, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	body, err := decodeSmallBlob(p.ResponseBodyBlob.Codec, p.ResponseBodyBlob.Payload)
	require.NoError(t, err)
	require.JSONEq(t, `{"password":"[REDACTED]","answer":"ordinary text"}`, string(body))
	e.ResponseBody = []byte(`{"password":"incomplete-fixture`)
	_, reason = prepareSmallExchange(e, nil)
	require.Equal(t, "unsupported_body", reason)
}

func TestSmallRetentionCompressionAndReplayReasons(t *testing.T) {
	for _, body := range [][]byte{[]byte("tiny"), bytes.Repeat([]byte("compress me"), 1000)} {
		b := makeSmallBlob(body)
		require.LessOrEqual(t, len(b.Payload), len(body))
		restored, err := decodeSmallBlob(b.Codec, b.Payload)
		require.NoError(t, err)
		require.Equal(t, body, restored)
	}
	for _, field := range []string{"previous_response_id", "file_id", "cachedContent", "encrypted_content"} {
		e := smallTestExchange()
		e.RequestBody = []byte(`{"` + field + `":"synthetic-reference"}`)
		p, reason := prepareSmallExchange(e, nil)
		require.Empty(t, reason)
		require.NotEmpty(t, p.ReplayReasons)
	}
	e := smallTestExchange()
	e.RequestBody = []byte(`{"input":[{"type":"input_image","image_url":"https://example.invalid/image"}]}`)
	_, reason := prepareSmallExchange(e, nil)
	require.Equal(t, "unsupported_body", reason)
}
func TestSmallRetentionConcurrentUsageAndFinish(t *testing.T) {
	r := smallTestRecorder()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		h := r.Begin("POST", "/v1/responses", 11, 12, 13)
		h.ClientRequest([]byte(`{}`), nil)
		wg.Add(2)
		go func() { defer wg.Done(); h.Finish(200, nil, false) }()
		go func() { defer wg.Done(); h.Usage(SmallUsage{InputTokens: 1}) }()
	}
	wg.Wait()
	require.Len(t, r.queue, 32)
}
func TestSmallRetentionMissingTablesFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(false))
	s := &SmallStore{DB: db}
	require.False(t, s.Ready(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSmallRetentionBucketCleanupIsBoundedAndIndependentOfSwitch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 11, 18, 8, 59, 0, 0, time.UTC)
	cutoff := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	exact := cutoff.Add(59 * time.Minute)
	require.Equal(t, exact, smallCutoff(now))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(smallMaintenanceLock).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM small_request_occurrences WHERE ctid IN (SELECT ctid FROM small_request_occurrences WHERE seen_at<=$1 LIMIT 5000)`)).WithArgs(exact).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	for _, table := range []string{"small_request_responses", "small_request_records", "small_request_blobs", "small_request_skips"} {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs(smallMaintenanceLock).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM ` + table + ` WHERE ctid IN (SELECT ctid FROM ` + table + ` WHERE bucket<$1 LIMIT 5000)`)).WithArgs(cutoff).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
	require.NoError(t, (&SmallStore{DB: db}).Cleanup(context.Background(), now))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSmallRetentionSQLDuplicateReusesBlobsAndUpsertsCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	p, reason := prepareSmallExchange(smallTestExchange(), nil)
	require.Empty(t, reason)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock_shared").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\$1::timestamptz").WillReturnRows(sqlmock.NewRows([]string{"fresh", "after_clear"}).AddRow(true, true))
	mock.ExpectQuery(`(?s)INSERT INTO small_request_records.*ON CONFLICT\(bucket,fingerprint\) DO UPDATE SET.*repeat_count=small_request_records.repeat_count\+1.*LIMIT 20.*RETURNING id,repeat_count`).WillReturnRows(sqlmock.NewRows([]string{"id", "repeat_count"}).AddRow(1, 2))
	mock.ExpectExec("INSERT INTO small_request_occurrences").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE small_request_responses SET repeat_count=repeat_count\\+1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, (&SmallStore{DB: db}).Save(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSmallRetentionSQLResponseSampleLimitEvictsAndReclaims(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	p, reason := prepareSmallExchange(smallTestExchange(), nil)
	require.Empty(t, reason)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock_shared").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\$1::timestamptz").WillReturnRows(sqlmock.NewRows([]string{"fresh", "after_clear"}).AddRow(true, true))
	mock.ExpectQuery("INSERT INTO small_request_records").WillReturnRows(sqlmock.NewRows([]string{"id", "repeat_count"}).AddRow(1, 30))
	mock.ExpectExec("INSERT INTO small_request_occurrences").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE small_request_responses SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectQuery("SELECT fingerprint,headers_hash,body_hash,repeat_count").WillReturnRows(sqlmock.NewRows([]string{"fingerprint", "hh", "bh", "count"}).AddRow("old", "old-header", "old-body", 7))
	mock.ExpectExec("DELETE FROM small_request_responses").WillReturnResult(sqlmock.NewResult(0, 1))
	for i := 0; i < 2; i++ {
		mock.ExpectExec("UPDATE small_request_blobs SET ref_count=ref_count-1").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("UPDATE small_request_records SET omitted_responses").WithArgs(int64(1), int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO small_request_responses").WillReturnResult(sqlmock.NewResult(0, 1))
	for i := 0; i < 2; i++ {
		mock.ExpectExec("INSERT INTO small_request_blobs").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("DELETE FROM small_request_blobs").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	require.NoError(t, (&SmallStore{DB: db}).Save(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSmallRetentionSaveFailureDoesNotPanic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin().WillReturnError(errors.New("tables not migrated"))
	p, reason := prepareSmallExchange(smallTestExchange(), nil)
	require.Empty(t, reason)
	require.Error(t, (&SmallStore{DB: db}).Save(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSmallRetentionActiveBytesReleasedOnEveryExit(t *testing.T) {
	for _, exit := range []string{"drop", "complete", "expire", "disable", "clear"} {
		t.Run(exit, func(t *testing.T) {
			r := smallTestRecorder()
			for i := 0; i < 100; i++ {
				r.ApplyEnabled(true)
				h := r.Begin("POST", "/v1/responses", 11, 12, 13)
				h.ClientRequest([]byte(`{}`), http.Header{"X-Test": {"request"}})
				h.Response([]byte("hello"), http.Header{"X-Test": {"response"}})
				require.Positive(t, r.activeBytes.Load())
				switch exit {
				case "drop":
					h.Response(make([]byte, SmallResponseLimit+1), nil)
				case "complete":
					h.Finish(200, nil, false)
					h.Usage(SmallUsage{InputTokens: 1})
					queued := <-r.queue
					queued.mu.Lock()
					queued.releaseLocked()
					queued.mu.Unlock()
				case "expire":
					h.Finish(500, nil, false)
					r.expirePending(time.Now().Add(3 * time.Minute))
				case "disable":
					r.ApplyEnabled(false)
				case "clear":
					r.Invalidate()
				}
				require.Zero(t, r.activeBytes.Load())
				require.Zero(t, r.active.Load())
			}
		})
	}
}

func TestSmallRetentionByteBudgetAllowsMoreThan96SmallRequests(t *testing.T) {
	r := smallTestRecorder()
	for i := 0; i < 1000; i++ {
		h := r.Begin("POST", "/v1/responses", 11, 12, 13)
		require.NotNil(t, h)
		h.ClientRequest([]byte(`{}`), nil)
	}
	require.EqualValues(t, 1000, r.active.Load())
	require.EqualValues(t, 2000, r.activeBytes.Load())
	r.ApplyEnabled(false)
	require.Zero(t, r.activeBytes.Load())
	require.Zero(t, r.active.Load())
}

func TestSmallRetentionConcurrentBytesNeverExceedBudget(t *testing.T) {
	r := smallTestRecorder()
	body := make([]byte, SmallResponseLimit)
	var wg sync.WaitGroup
	for i := 0; i < 512; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := r.Begin("POST", "/v1/responses", 11, 12, 13)
			if h == nil {
				return
			}
			h.ClientRequest([]byte(`{}`), nil)
			h.Response(body, nil)
			require.LessOrEqual(t, r.activeBytes.Load(), int64(SmallActiveBytes))
		}()
	}
	wg.Wait()
	require.Positive(t, r.skipped[3].Load())
	r.ApplyEnabled(false)
	require.Zero(t, r.activeBytes.Load())
	require.Zero(t, r.active.Load())
}

func TestSmallRetentionHandleSafetyLimit(t *testing.T) {
	r := smallTestRecorder()
	for i := 0; i < SmallActiveLimit; i++ {
		require.NotNil(t, r.Begin("POST", "/v1/responses", 11, 12, 13))
	}
	require.Nil(t, r.Begin("POST", "/v1/responses", 11, 12, 13))
	require.EqualValues(t, SmallActiveLimit, r.active.Load())
	r.ApplyEnabled(false)
	require.Zero(t, r.active.Load())
	require.Zero(t, r.activeBytes.Load())
}

func TestSmallRetentionByteReservationRejectsOvershoot(t *testing.T) {
	r := smallTestRecorder()
	r.activeBytes.Store(SmallActiveBytes - 1)
	h := r.Begin("POST", "/v1/responses", 11, 12, 13)
	h.ClientRequest([]byte(`{}`), nil)
	require.Equal(t, "active_limit", h.reason)
	require.EqualValues(t, SmallActiveBytes-1, r.activeBytes.Load())
	require.Zero(t, r.active.Load())
	r.activeBytes.Store(0) // Return this test's synthetic pre-existing reservation.
}

func TestSmallRetentionStatsExposeAllReasonsAndQuota(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
	mock.ExpectQuery("SELECT count\\(\\*\\),COALESCE").WillReturnRows(sqlmock.NewRows([]string{"records", "requests", "raw", "oldest", "newest", "once", "few", "many", "hundred"}).AddRow(0, 0, 0, nil, nil, 0, 0, 0, 0))
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"size"}).AddRow(SmallStoredBytesLimit))
	mock.ExpectQuery("SELECT reason,sum").WillReturnRows(sqlmock.NewRows([]string{"reason", "count"}))
	stats, err := (&SmallStore{DB: db}).Stats(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, SmallStoredBytesLimit, stats.StoredLimitBytes)
	require.True(t, stats.QuotaFull)
	require.Len(t, stats.Skipped, len(smallSkipReasons))
	for _, reason := range smallSkipReasons {
		require.Contains(t, stats.Skipped, reason)
	}
	require.NotContains(t, stats.Skipped, "usage_estimated")
	require.Contains(t, stats.Skipped, "quota_full")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSmallRetentionStoredQuotaStartsClosed(t *testing.T) {
	r := NewSmallRecorder(nil, nil)
	defer r.Close()
	require.True(t, r.QuotaFull())
}

func TestSmallRetentionStoredQuotaRefreshAndQueryFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := smallTestRecorder()
	r.Store = &SmallStore{DB: db}
	for _, tc := range []struct {
		size          int64
		failure, full bool
	}{
		{SmallStoredBytesLimit, false, true},
		{SmallStoredBytesLimit + 1, false, true},
		{0, true, true},
		{SmallStoredBytesLimit - 1, false, false},
		{0, true, false},
	} {
		query := mock.ExpectQuery("SELECT COALESCE\\(sum\\(stored_size\\),0\\) FROM small_request_blobs WHERE bucket>=\\$1")
		if tc.failure {
			query.WillReturnError(errors.New("synthetic query failure"))
		} else {
			query.WillReturnRows(sqlmock.NewRows([]string{"size"}).AddRow(tc.size))
		}
		r.refreshQuota(context.Background())
		require.Equal(t, tc.full, r.QuotaFull())
		h := r.Begin("POST", "/v1/responses", 11, 12, 13)
		if tc.full {
			require.Nil(t, h)
		} else {
			require.NotNil(t, h)
			r.Invalidate()
		}
	}
	r.quotaFull.Store(true)
	mock.ExpectQuery("SELECT COALESCE").WillDelayFor(50 * time.Millisecond).WillReturnRows(sqlmock.NewRows([]string{"size"}).AddRow(0))
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	r.refreshQuota(ctx)
	require.True(t, r.QuotaFull(), "a query timeout must keep the last full decision")
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSmallRetentionResponseDecompressionUsesSameFilter(t *testing.T) {
	e := smallTestExchange()
	e.ResponseBody = []byte("data: {\"text\":\"hello\",\"access_token\":\"compressed-fixture\"}\n\ndata: [DONE]\n\n")
	plain, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, err := w.Write(e.ResponseBody)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	e.ResponseBody = compressed.Bytes()
	e.ResponseHeaders.Set("Content-Encoding", "gzip")
	p, reason := prepareSmallExchange(e, nil)
	require.Empty(t, reason)
	decoded, err := decodeSmallBlob(p.ResponseBodyBlob.Codec, p.ResponseBodyBlob.Payload)
	require.NoError(t, err)
	require.Equal(t, plain.ResponseBodyBlob.Hash, p.ResponseBodyBlob.Hash)
	require.NotContains(t, string(decoded), "compressed-fixture")
	require.Contains(t, string(decoded), "[REDACTED]")
	require.NotContains(t, p.ResponseHeaders, "content-encoding")
}
func TestSmallRetentionClearBlocksOldCopiesAcrossInstances(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := &SmallStore{DB: db}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE small_request_state SET cleared_at=clock_timestamp").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("TRUNCATE small_request_occurrences,small_request_responses").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, store.Clear(context.Background()))
	p, reason := prepareSmallExchange(smallTestExchange(), nil)
	require.Empty(t, reason)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock_shared").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\$1::timestamptz").WillReturnRows(sqlmock.NewRows([]string{"fresh", "after_clear"}).AddRow(true, false))
	mock.ExpectRollback()
	require.NoError(t, store.Save(context.Background(), p))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSmallRetentionMetadataContainsNoBodies(t *testing.T) {
	p, reason := prepareSmallExchange(smallTestExchange(), nil)
	require.Empty(t, reason)
	data, err := json.Marshal(SmallRecordData{Method: p.Method, Path: p.Path, FirstUsage: &p.Usage})
	require.NoError(t, err)
	require.NotContains(t, string(data), "business-signature")
	require.NotContains(t, string(data), "business-value")
	for _, path := range []string{"/v1/images/generations", "/v1/videos", "/v1/embeddings", "/v1/messages/count_tokens"} {
		require.False(t, SmallTextEndpoint("POST", path))
	}
	require.True(t, SmallTextEndpoint("POST", "/v1beta/models/test:streamGenerateContent"))
	require.True(t, strings.Contains(SmallCurl("POST", "/v1/messages", nil, []byte("it's")), `'"'"'`))
}
