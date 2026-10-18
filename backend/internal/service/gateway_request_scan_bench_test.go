package service

import (
	"testing"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/tidwall/gjson"
)

// parseGatewayRequestGJSONLegacy 复刻改造前 parseGatewayRequestCurrentBody 的
// gjson 路径，用于对比同一份改动前后的成本（仅基准使用）。
func parseGatewayRequestGJSONLegacy(body []byte, protocol string) (*ParsedRequest, error) {
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body)}
	if !gjson.ValidBytes(body) {
		return nil, DescribeInvalidJSON(body)
	}
	jsonStr := *(*string)(unsafe.Pointer(&body))
	if hasDuplicateTopLevelKey(jsonStr, "model") {
		return nil, ErrDuplicateModelField
	}
	clearGatewayRequestDerivedState(parsed)
	parsed.protocol = protocol
	model := gjson.Get(jsonStr, "model")
	if model.Exists() {
		parsed.Model = model.String()
	}
	stream := gjson.Get(jsonStr, "stream")
	if stream.Exists() {
		parsed.Stream = stream.Bool()
	}
	parsed.MetadataUserID = gjson.Get(jsonStr, "metadata.user_id").String()
	thinkingType := gjson.Get(jsonStr, "thinking.type").String()
	parsed.ThinkingEnabled = thinkingType == "enabled" || thinkingType == "adaptive"
	parsed.OutputEffort = gjson.Get(jsonStr, "output_config.effort").String()
	if protocol == domain.PlatformAnthropic {
		parsed.Speed = gjson.Get(jsonStr, "speed").String()
	}
	parsed.MaxTokens = int(gjson.Get(jsonStr, "max_tokens").Int())
	setGatewayRequestRanges(parsed, protocol, jsonStr)
	return parsed, nil
}

func BenchmarkScanGatewayRequest_GJSONLegacy_Small(b *testing.B) {
	data := buildSmallJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseGatewayRequestGJSONLegacy(data, domain.PlatformAnthropic)
	}
}

func BenchmarkScanGatewayRequest_SinglePass_Small(b *testing.B) {
	data := buildSmallJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseGatewayRequest(NewRequestBodyRef(data), domain.PlatformAnthropic)
	}
}

func BenchmarkScanGatewayRequest_GJSONLegacy_Large(b *testing.B) {
	data := buildLargeJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseGatewayRequestGJSONLegacy(data, domain.PlatformAnthropic)
	}
}

func BenchmarkScanGatewayRequest_SinglePass_Large(b *testing.B) {
	data := buildLargeJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseGatewayRequest(NewRequestBodyRef(data), domain.PlatformAnthropic)
	}
}
