package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSmallRetentionDisabledMiddlewarePreservesWriterIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := requestcapture.NewSmallRecorder(nil, nil)
	defer r.Close()
	r.ApplyEnabled(false)
	router := gin.New()
	var original gin.ResponseWriter
	router.Use(func(c *gin.Context) {
		original = c.Writer
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 12, UserID: 11})
	}, RequestCapture(nil, r))
	router.POST("/v1/responses", func(c *gin.Context) {
		require.Same(t, original, c.Writer)
		require.Nil(t, requestcapture.SmallFromContext(c.Request.Context()))
		c.String(200, "unchanged")
	})
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, "unchanged", rec.Body.String())
}

func TestSmallRetentionSensitiveReadIsAuditedWithoutBodyCopies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
		c.Set(string(ContextKeyUserRole), "admin")
	}, gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	router.GET("/api/v1/admin/request-captures/small/:record", func(c *gin.Context) { c.JSON(200, gin.H{"request_body": "synthetic-private-input"}) })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/admin/request-captures/small/1", nil))
	audit.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	require.Equal(t, "admin.small_request_retention.content.read", repo.logs[0].Action)
	require.Empty(t, repo.logs[0].RequestBody)
}

func TestSmallRetentionEnabledMiddlewareKeepsOriginalClientExchange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"size"}).AddRow(0))
	for i := 0; i < 5; i++ {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
		mock.ExpectExec("DELETE FROM small_request_").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
	}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock_shared").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\$1::timestamptz").WillReturnRows(sqlmock.NewRows([]string{"fresh", "after_clear"}).AddRow(true, true))
	mock.ExpectQuery("INSERT INTO small_request_records").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(11), int64(12), "text-test", 200, sqlmock.AnyArg(), 99, 5, 800, 100, "0.00000001", sqlmock.AnyArg(), `["synthetic-request"]`, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "repeat_count"}).AddRow(1, 1))
	mock.ExpectExec("INSERT INTO small_request_occurrences").WithArgs(int64(1), int64(1), sqlmock.AnyArg(), "synthetic-request", sqlmock.AnyArg(), 200, 99, 5, 800, 100, "0.00000001", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	for i := 0; i < 2; i++ {
		mock.ExpectExec("INSERT INTO small_request_blobs").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("UPDATE small_request_responses SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO small_request_responses").WillReturnResult(sqlmock.NewResult(0, 1))
	for i := 0; i < 2; i++ {
		mock.ExpectExec("INSERT INTO small_request_blobs").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("DELETE FROM small_request_blobs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	r := requestcapture.NewSmallRecorder(&requestcapture.SmallStore{DB: db}, nil)
	defer r.Close()
	r.ApplyEnabled(true)
	require.Eventually(t, r.Enabled, time.Second, time.Millisecond)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 12, UserID: 11}) }, RequestCapture(nil, r))
	original := []byte("{ \"model\":\"text-test\",\"input\":\"hello\", \"signature\":\"business-signature\" }\n")
	sse := "data: {\"type\":\"response.completed\"}\n\ndata: [DONE]\n\n"
	router.POST("/v1/responses", func(c *gin.Context) {
		body, e := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, e)
		require.Equal(t, original, body)
		c.Header("Content-Type", "text/event-stream")
		_, e = c.Writer.WriteString(sse)
		require.NoError(t, e)
		c.Writer.Flush()
		requestcapture.SmallFromContext(c.Request.Context()).Usage(requestcapture.SmallUsage{RequestID: "synthetic-request", InputTokens: 99, OutputTokens: 5, CacheReadTokens: 800, CacheCreationTokens: 100, ActualCost: "0.00000001", Model: "text-test"})
	})
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	_, err = zip.Write(original)
	require.NoError(t, err)
	require.NoError(t, zip.Close())
	req := httptest.NewRequest("POST", "/v1/responses", &compressed)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer synthetic-only")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	require.Equal(t, 200, response.Code)
	require.Equal(t, sse, response.Body.String())
	require.True(t, response.Flushed)
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 2*time.Second, time.Millisecond)
}

func TestSmallRetentionUsesDecodedOriginalBodyAndUnmodifiedSSE(t *testing.T) {
	// A nil-store recorder stays closed. Its hooks must still leave the shared
	// decoder and real writer exactly unchanged, including compressed ingress.
	r := requestcapture.NewSmallRecorder(nil, nil)
	defer r.Close()
	original := []byte("{ \"key\":\"business-value\", \"signature\":\"business-signature\" }\n")
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	_, err := writer.Write(original)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", &zipped)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(req)
	require.NoError(t, err)
	require.Equal(t, original, body)
	rec := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(rec)
	w := &smallCaptureWriter{ResponseWriter: ctx.Writer}
	sse := "data: {\"signature\":\"business-signature\"}\n\ndata: [DONE]\n\n"
	n, err := w.WriteString(sse)
	require.NoError(t, err)
	require.Equal(t, len(sse), n)
	w.Flush()
	require.Equal(t, sse, rec.Body.String())
	require.True(t, rec.Flushed)
	require.Equal(t, "admin.small_request_retention.content.read", auditSensitiveReads["GET /api/v1/admin/request-captures/small/:record"])
	require.Nil(t, requestcapture.SmallFromContext(context.Background()))
	require.True(t, strings.Contains(sse, "business-signature"))
}
