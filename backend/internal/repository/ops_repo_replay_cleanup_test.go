package repository

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// TestOpsErrorLogInsertDoesNotPersistRequestReplayFields 守住 136 迁移删掉的重放列
// 不会被重新写回来。
//
// 注意 request_headers：033 迁移曾经加过同名列用于「重放/重试」，136 把它删了。
// 253 迁移又加回同名（但语义不同）的列——现在存的是脱敏后的诊断快照
// （凭据头整条丢弃，见 internal/service/ops_header_snapshot.go），
// 不是当年那份含凭据的完整请求头。所以这里按「精确列名 + 数据来源」判断：
// 允许 request_headers 存在，但必须由 OpsInsertErrorLogInput.RequestHeaders
// （OpsHeaderSnapshot 类型）提供，而不是当年的 RequestHeadersJSON []byte。
func TestOpsErrorLogInsertDoesNotPersistRequestReplayFields(t *testing.T) {
	disallowedColumns := []string{
		"request_body",
		"request_body_truncated",
		"request_body_bytes",
		"is_retryable",
		"retry_count",
		"resolved_retry_id",
	}

	insertSQL := strings.ToLower(insertOpsErrorLogSQL)
	for _, column := range disallowedColumns {
		if strings.Contains(insertSQL, column) {
			t.Fatalf("ops error log insert still references dropped replay column %q", column)
		}
	}

	inputType := reflect.TypeOf(service.OpsInsertErrorLogInput{})
	disallowedFields := []string{
		"RequestBodyJSON",
		"RequestBodyTruncated",
		"RequestBodyBytes",
		"RequestHeadersJSON",
		"IsRetryable",
		"RetryCount",
		"ResolvedRetryID",
	}
	for _, field := range disallowedFields {
		if _, ok := inputType.FieldByName(field); ok {
			t.Fatalf("OpsInsertErrorLogInput still carries replay field %q", field)
		}
	}

	// 253 加回的同名列必须是脱敏快照类型，不能退回 []byte 的原始头。
	field, ok := inputType.FieldByName("RequestHeaders")
	if !ok {
		t.Fatalf("OpsInsertErrorLogInput is missing the sanitized request header snapshot field")
	}
	snapshotType := reflect.TypeOf(service.OpsHeaderSnapshot{})
	if field.Type != snapshotType {
		t.Fatalf("RequestHeaders must be %s (sanitized snapshot), got %s", snapshotType, field.Type)
	}
}
