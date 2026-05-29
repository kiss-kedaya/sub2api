package service

import (
	"strconv"
	"strings"
	"testing"
)

var benchmarkStringSink string

// BenchmarkGenerateSessionHash_Metadata 关注 JSON 解析与正则匹配开销。
func BenchmarkGenerateSessionHash_Metadata(b *testing.B) {
	svc := &GatewayService{}
	body := []byte(`{"metadata":{"user_id":"session_123e4567-e89b-12d3-a456-426614174000"},"messages":[{"content":"hello"}]}`)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
		if err != nil {
			b.Fatalf("解析请求失败: %v", err)
		}
		benchmarkStringSink = svc.GenerateSessionHash(parsed)
	}
}

// BenchmarkExtractCacheableContent_System 关注字符串拼接路径的性能。
func BenchmarkExtractCacheableContent_System(b *testing.B) {
	svc := &GatewayService{}
	req := buildSystemCacheableRequest(12)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkStringSink = svc.extractCacheableContent(req)
	}
}

func buildSystemCacheableRequest(parts int) *ParsedRequest {
	var builder strings.Builder
	builder.WriteString(`{"system":[`)
	for i := 0; i < parts; i++ {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`{"text":"system_part_`)
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString(`","cache_control":{"type":"ephemeral"}}`)
	}
	builder.WriteString(`]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(builder.String())), "")
	if err != nil {
		panic(err)
	}
	return parsed
}
