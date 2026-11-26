package service

import (
	"testing"

	"github.com/tidwall/gjson"
)

// These benchmarks only use APIs that exist both before and after the PERF-18
// change, so the very same file runs against the base commit and the branch:
//
//	go test -run '^$' -bench 'Perf18' -benchmem ./internal/service/
//
// Shapes: "code" is escape-dense Claude Code traffic (a \n every few dozen
// bytes), "prose" has almost no backslashes.

func perf18Parsed(b *testing.B, body []byte) *ParsedRequest {
	b.Helper()
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	if err != nil {
		b.Fatal(err)
	}
	return parsed
}

// BenchmarkPerf18ReplaceBodySameSlice is the Forward / handler no-op rewrite:
// StripEmptyTextBlocks and friends hand back the very slice they were given.
func BenchmarkPerf18ReplaceBodySameSlice(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		b.Run(sz.name, func(b *testing.B) {
			parsed := perf18Parsed(b, body)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := parsed.ReplaceBody(parsed.Body.Bytes()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPerf18ReplaceBodyEqualCopy alternates two byte-identical but distinct
// slices (a rewrite that rebuilt the body without changing it).
func BenchmarkPerf18ReplaceBodyEqualCopy(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		other := append([]byte(nil), body...)
		b.Run(sz.name, func(b *testing.B) {
			parsed := perf18Parsed(b, body)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				next := body
				if i&1 == 1 {
					next = other
				}
				if err := parsed.ReplaceBody(next); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPerf18ReplaceBodyChanged alternates two bodies that differ in the model
// name only (the account model mapping rewrite): every call has to refresh.
func BenchmarkPerf18ReplaceBodyChanged(b *testing.B) {
	for _, sz := range perf18Sizes {
		bodyA := perf18AnthropicBody(sz.size, perf18Code, "")
		bodyB := append([]byte(nil), bodyA...)
		copy(bodyB[len(`{"model":"claude-sonnet-4-`):], "6")
		b.Run(sz.name, func(b *testing.B) {
			parsed := perf18Parsed(b, bodyA)
			b.SetBytes(int64(len(bodyA)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				next := bodyA
				if i&1 == 1 {
					next = bodyB
				}
				if err := parsed.ReplaceBody(next); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPerf18CloneForBodySame is the per-account-attempt clone in the failover loop.
func BenchmarkPerf18CloneForBodySame(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		b.Run(sz.name, func(b *testing.B) {
			parsed := perf18Parsed(b, body)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := parsed.CloneForBody(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPerf18ParseGatewayRequest is the first parse of the client body (validity gate + scan).
func BenchmarkPerf18ParseGatewayRequest(b *testing.B) {
	for _, shape := range []struct {
		name string
		s    perf18Shape
	}{{"code", perf18Code}, {"prose", perf18Prose}} {
		for _, sz := range perf18Sizes {
			body := perf18AnthropicBody(sz.size, shape.s, "")
			b.Run(shape.name+"/"+sz.name, func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkPerf18ScanTopLevel isolates the structural scan (no validity gate).
func BenchmarkPerf18ScanTopLevel(b *testing.B) {
	for _, shape := range []struct {
		name string
		s    perf18Shape
	}{{"code", perf18Code}, {"prose", perf18Prose}} {
		for _, sz := range perf18Sizes {
			body := perf18AnthropicBody(sz.size, shape.s, "")
			b.Run(shape.name+"/"+sz.name, func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := scanGatewayRequestTopLevel(body); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkPerf18Validators compares the validity gates available for a body.
func BenchmarkPerf18Validators(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		b.Run("gjson/"+sz.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				if !gjson.ValidBytes(body) {
					b.Fatal("invalid")
				}
			}
		})
		b.Run("fast/"+sz.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				if !validJSON(body) {
					b.Fatal("invalid")
				}
			}
		})
	}
}

// BenchmarkPerf18SanitizeBetaTokens is the capability sanitize that buildUpstreamRequest*
// runs on every Anthropic request: header without the optional betas, body with none of
// the guarded fields (the overwhelmingly common case). "withOutputConfig" is a body whose
// top-level output_config makes the old "output_config" substring precheck pass.
func BenchmarkPerf18SanitizeBetaTokens(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		b.Run(sz.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, changed := sanitizeAnthropicBodyForBetaTokens(body, "oauth-2025-04-20")
				if changed {
					b.Fatal("unexpected change")
				}
				out, changed = sanitizeAnthropicFallbackFields(out, "oauth-2025-04-20")
				if changed {
					b.Fatal("unexpected change")
				}
			}
		})
	}
}

// BenchmarkPerf18StripEmptyTextBlocks is the no-op pre-filter every Forward / passthrough request runs.
func BenchmarkPerf18StripEmptyTextBlocks(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		b.Run(sz.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if out := StripEmptyTextBlocks(body); len(out) != len(body) {
					b.Fatal("unexpected change")
				}
			}
		})
	}
}

// BenchmarkPerf18ForwardRewriteChain approximates the no-op part of GatewayService.Forward for
// an API-key account: ten replaceBody calls that all return the same slice.
func BenchmarkPerf18ForwardRewriteChain(b *testing.B) {
	for _, sz := range perf18Sizes {
		body := perf18AnthropicBody(sz.size, perf18Code, "")
		b.Run(sz.name, func(b *testing.B) {
			parsed := perf18Parsed(b, body)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cur := parsed.Body.Bytes()
				for step := 0; step < 10; step++ {
					if err := parsed.ReplaceBody(cur); err != nil {
						b.Fatal(err)
					}
					cur = parsed.Body.Bytes()
				}
			}
		})
	}
}

// The generated corpus must be valid JSON with the expected top-level shape, or every
// benchmark above would be measuring the invalid-body path.
func TestPerf18CorpusIsValidJSON(t *testing.T) {
	for _, sz := range perf18Sizes {
		for _, shape := range []perf18Shape{perf18Prose, perf18Code} {
			body := perf18AnthropicBody(sz.size, shape, "")
			if !gjson.ValidBytes(body) {
				t.Fatalf("perf18 corpus %s/%d is not valid JSON", sz.name, shape)
			}
			if len(body) < sz.size*9/10 || len(body) > sz.size*12/10 {
				t.Fatalf("perf18 corpus %s/%d has %d bytes, want about %d", sz.name, shape, len(body), sz.size)
			}
			if _, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic); err != nil {
				t.Fatalf("perf18 corpus %s/%d does not parse: %v", sz.name, shape, err)
			}
		}
	}
}
