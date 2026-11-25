package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// reasoningCodecWords 是类推理文本里最常见的真词；长尾用伪词补到 2000 个。
var reasoningCodecWords = strings.Fields(`the to of and a is that I need user file check let me see wait actually so then this
function cache key value if it should be not with for in on from because but also now first next hmm okay maybe think about
how what which when return error miss test code line request response redis entry content reasoning gateway service account
group model token before after should would could already still need to make sure that the`)

const reasoningCodecHan = "的一是不了在人有我他这个们中来上大为和国地到以说时要就出会可也你对生能而子那得于着下自之年过发后作里用道行所然家种事成方多经么去法学如都同现当没动面起与好看天水化高已想开本见从将心前最样此意话正等间外实感理问题数据服务请求缓存读取写入压缩解压过期返回错误用户账号分组倍率计费余额模型上游网关会话密钥配置检查测试代码文件函数"

// reasoningCodecSampleText 用固定种子造「英文为主、夹中文」的类推理文本：高频词是真词，
// 长尾是伪词，词频按 Zipf 分布。它只是给测试和基准造形的合成数据，不是生产数据；
// 6.7KB 的一条用 gzip 默认级别约压到 1/2.1，比本机真实 LLM 思考文本（约 1/2.6）略偏保守。
func reasoningCodecSampleText(n int) string {
	rng := rand.New(rand.NewSource(20261126))
	syllables := []string{"ba", "co", "de", "fi", "ga", "he", "in", "jo", "ka", "lo", "mu", "ne", "or", "pa", "qui", "re", "st", "ta", "un", "ve", "wi", "ex", "ing", "ion", "er", "al", "tion", "ent", "ac", "is", "ou", "th", "ch", "pr", "tr", "com", "con", "per", "ro", "se"}
	vocab := append([]string(nil), reasoningCodecWords...)
	for len(vocab) < 2000 {
		var sb strings.Builder
		for i, k := 0, 1+rng.Intn(3); i < k; i++ {
			_, _ = sb.WriteString(syllables[rng.Intn(len(syllables))])
		}
		vocab = append(vocab, sb.String())
	}
	han := []rune(reasoningCodecHan)
	cjk := make([]string, 400)
	for i := range cjk {
		var word strings.Builder
		for j, k := 0, 2+rng.Intn(2); j < k; j++ {
			_, _ = word.WriteRune(han[rng.Intn(len(han))])
		}
		cjk[i] = word.String()
	}
	zipf := rand.NewZipf(rng, 1.3, 3, uint64(len(vocab)-1))
	zipfCJK := rand.NewZipf(rng, 1.3, 3, uint64(len(cjk)-1))
	var sb strings.Builder
	for words := 0; sb.Len() < n; words++ {
		if words%40 >= 34 { // 每 40 个词里夹 6 个中文词
			_, _ = sb.WriteString(cjk[zipfCJK.Uint64()])
			continue
		}
		_, _ = sb.WriteString(vocab[zipf.Uint64()])
		switch rng.Intn(12) {
		case 0:
			_, _ = sb.WriteString(". ")
		case 1:
			_, _ = sb.WriteString(", ")
		case 2:
			_, _ = sb.WriteString("\n")
		default:
			_, _ = sb.WriteString(" ")
		}
	}
	return sb.String()[:n]
}

// reasoningCodecSampleEntry 造一条与 deepSeekNativeReasoningEntry 同形的 JSON，总长约 n 字节：
// {"content":[{"type":"reasoning_text","text":"..."}],"encrypted_digest":"<64 hex>"}。
func reasoningCodecSampleEntry(n int) string {
	digest := strings.Repeat("a1b2c3d4", 8)
	overhead := len(`{"content":[{"type":"reasoning_text","text":""}],"encrypted_digest":""}`) + len(digest)
	text, _ := json.Marshal(strings.ToValidUTF8(reasoningCodecSampleText(n-overhead), ""))
	return `{"content":[{"type":"reasoning_text","text":` + string(text) + `}],"encrypted_digest":"` + digest + `"}`
}

func reasoningCodecNativeID(n int) string {
	return fmt.Sprintf("%s%d:%s", service.DeepSeekNativeReasoningCacheKeyPrefix, n, hex.EncodeToString(bytes.Repeat([]byte{byte(n)}, 32)))
}

// reasoningCodecLegacyID 是老族（Responses→Chat 桥接）的 item id，不压缩。
const reasoningCodecLegacyID = "rs_0123456789abcdef"

func newReasoningCodecCache(t *testing.T) (*miniredis.Miniredis, service.GatewayCache) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, NewGatewayCache(client)
}

func reasoningCodecRandom(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	_, _ = rng.Read(b)
	return string(b)
}

func reasoningCodecGzip(t testing.TB, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(data)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.String()
}

func requireReasoningMiss(t *testing.T, cache service.GatewayCache, id, why string) {
	t.Helper()
	got, err := cache.GetReasoningContent(context.Background(), id)
	require.ErrorIs(t, err, service.ErrReasoningContentNotFound, why)
	require.Empty(t, got, why)
}

// 往返必须逐字节相等：空串之外的任意字符串，两个 key 族都一样。
func TestReasoningContentRoundTripIsByteExact(t *testing.T) {
	_, cache := newReasoningCodecCache(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))
	cases := map[string]string{
		"1 byte":                  "x",
		"511 compressible":        strings.Repeat("a", 511),
		"512 compressible":        strings.Repeat("a", 512),
		"513 compressible":        strings.Repeat("a", 513),
		"511 text":                reasoningCodecSampleText(511),
		"512 text":                reasoningCodecSampleText(512),
		"513 text":                reasoningCodecSampleText(513),
		"typical 6.7KB entry":     reasoningCodecSampleEntry(6700),
		"1MB text":                reasoningCodecSampleText(1 << 20),
		"2MB text":                reasoningCodecSampleText(2 << 20),
		"NUL newline tab CR":      strings.Repeat("a\x00b\r\n\t\n\x00", 200),
		"utf8 mix":                strings.Repeat("思考 🚀 reasoning ✅   \"quoted\" \\ é ", 60),
		"invalid utf8":            "\xff\xfe" + strings.Repeat("\x80\xc3", 400),
		"random binary 600":       reasoningCodecRandom(rng, 600),
		"random binary 1MB":       reasoningCodecRandom(rng, 1<<20),
		"gzip magic, tiny":        "\x1f\x8b\x08",
		"gzip magic, small":       "\x1f\x8b\x08 not actually gzip",
		"gzip magic, large":       "\x1f\x8b\x08" + reasoningCodecSampleText(5000),
		"gzip magic, random":      "\x1f\x8b\x08" + reasoningCodecRandom(rng, 4000),
		"a real gzip stream":      reasoningCodecGzip(t, []byte(reasoningCodecSampleText(3000))),
		"gzip of a gzip stream":   reasoningCodecGzip(t, []byte(reasoningCodecGzip(t, []byte(reasoningCodecSampleText(3000))))),
		"almost gzip magic 1f 8b": "\x1f\x8b\x07" + reasoningCodecSampleText(2000),
	}
	for _, id := range []string{reasoningCodecNativeID(1), reasoningCodecLegacyID} {
		for name, want := range cases {
			require.NoError(t, cache.SetReasoningContent(ctx, id, want, time.Minute), name)
			got, err := cache.GetReasoningContent(ctx, id)
			require.NoError(t, err, name)
			if got != want {
				t.Fatalf("%s (%.12s): round trip changed the value: wrote %d bytes, read %d bytes", name, id, len(want), len(got))
			}
		}
	}

	// 空内容仍是 no-op（既有行为）：写不进去，读是未命中。
	require.NoError(t, cache.SetReasoningContent(ctx, reasoningCodecNativeID(2), "", time.Minute))
	requireReasoningMiss(t, cache, reasoningCodecNativeID(2), "empty content is never stored")
}

// 存储格式：小值与不可压缩的值存原文，大且可压缩的 DeepSeek 原生值存 gzip，老族永远存原文，TTL 不变。
func TestReasoningContentStorageFormat(t *testing.T) {
	mr, cache := newReasoningCodecCache(t)
	ctx := context.Background()
	stored := func(id string) string {
		v, err := mr.Get(reasoningContentPrefix + id)
		require.NoError(t, err)
		return v
	}
	rng := rand.New(rand.NewSource(11))

	t.Run("below the threshold stays plaintext", func(t *testing.T) {
		id, content := reasoningCodecNativeID(1), strings.Repeat("a", reasoningCompressMinBytes-1)
		require.NoError(t, cache.SetReasoningContent(ctx, id, content, time.Minute))
		require.Equal(t, content, stored(id))
	})
	t.Run("at the threshold and compressible is gzip", func(t *testing.T) {
		id, content := reasoningCodecNativeID(2), strings.Repeat("a", reasoningCompressMinBytes)
		require.NoError(t, cache.SetReasoningContent(ctx, id, content, time.Minute))
		require.True(t, hasGzipMagic(stored(id)))
		require.Less(t, len(stored(id)), len(content))
	})
	t.Run("incompressible large value stays plaintext", func(t *testing.T) {
		id, content := reasoningCodecNativeID(3), reasoningCodecRandom(rng, 8192)
		require.NoError(t, cache.SetReasoningContent(ctx, id, content, time.Minute))
		require.Equal(t, content, stored(id), "compression must never make the stored value bigger")
	})
	t.Run("legacy family is never compressed", func(t *testing.T) {
		// 老族由 reasoningContentByID 原样当 reasoning 文本用，旧进程读到二进制会把乱码发给上游。
		content := reasoningCodecSampleText(8192)
		require.NoError(t, cache.SetReasoningContent(ctx, reasoningCodecLegacyID, content, time.Minute))
		require.Equal(t, content, stored(reasoningCodecLegacyID))
	})
	t.Run("typical entry shrinks and keeps its TTL", func(t *testing.T) {
		id, entry := reasoningCodecNativeID(4), reasoningCodecSampleEntry(6700)
		require.NoError(t, cache.SetReasoningContent(ctx, id, entry, 7*24*time.Hour))
		require.True(t, hasGzipMagic(stored(id)))
		require.Less(t, len(stored(id)), len(entry)*7/10, "a ~6.7KB reasoning entry should compress to well under 70 percent")
		require.Equal(t, 7*24*time.Hour, mr.TTL(reasoningContentPrefix+id))
		got, err := cache.GetReasoningContent(ctx, id)
		require.NoError(t, err)
		require.Equal(t, entry, got)
	})
	t.Run("plaintext that looks like gzip is wrapped, even when tiny", func(t *testing.T) {
		id, content := reasoningCodecNativeID(5), "\x1f\x8b\x08tiny"
		require.NoError(t, cache.SetReasoningContent(ctx, id, content, time.Minute))
		require.True(t, hasGzipMagic(stored(id)))
		got, err := cache.GetReasoningContent(ctx, id)
		require.NoError(t, err)
		require.Equal(t, content, got)
	})
}

// 旧进程（以及压缩上线前写入、最长 7 天才过期的键）留下的明文照常读出。
func TestReasoningContentReadsPlaintextWrittenByOlderVersions(t *testing.T) {
	mr, cache := newReasoningCodecCache(t)
	for name, tc := range map[string]struct{ id, value string }{
		"native entry JSON":   {reasoningCodecNativeID(1), reasoningCodecSampleEntry(6700)},
		"native small":        {reasoningCodecNativeID(2), `{"content":[{"type":"reasoning_text","text":"hi"}]}`},
		"legacy summary text": {reasoningCodecLegacyID, reasoningCodecSampleText(3000)},
		"legacy short":        {"rs_short", "think hard"},
		"utf8":                {"rs_utf8", "先想一想 🚀"},
	} {
		require.NoError(t, mr.Set(reasoningContentPrefix+tc.id, tc.value), name)
		got, err := cache.GetReasoningContent(context.Background(), tc.id)
		require.NoError(t, err, name)
		require.Equal(t, tc.value, got, name)
	}
}

// 压缩炸弹：解压后超过 4MB 按未命中处理；恰好等于上限仍然命中。
func TestReasoningContentDecompressionBombIsAMiss(t *testing.T) {
	mr, cache := newReasoningCodecCache(t)
	ctx := context.Background()
	id := reasoningCodecNativeID(1)
	put := func(decoded int) {
		require.NoError(t, mr.Set(reasoningContentPrefix+id, reasoningCodecGzip(t, make([]byte, decoded))))
	}

	put(reasoningMaxDecodedBytes)
	got, err := cache.GetReasoningContent(ctx, id)
	require.NoError(t, err, "exactly the limit is still a hit")
	require.Len(t, got, reasoningMaxDecodedBytes)

	for _, decoded := range []int{reasoningMaxDecodedBytes + 1, 16 << 20} {
		put(decoded)
		stored, err := mr.Get(reasoningContentPrefix + id)
		require.NoError(t, err)
		require.Less(t, len(stored), 64<<10, "the bomb itself is tiny")
		requireReasoningMiss(t, cache, id, fmt.Sprintf("%d bytes decoded", decoded))
	}

	// 炸弹之后同一进程里的下一次正常读取不受影响（池里复用的 reader 状态是干净的）。
	require.NoError(t, cache.SetReasoningContent(ctx, id, reasoningCodecSampleEntry(6700), time.Minute))
	got, err = cache.GetReasoningContent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, reasoningCodecSampleEntry(6700), got)
}

// 损坏/截断的压缩流一律按未命中处理，不返回别的错误，也不留下脏状态。
func TestReasoningContentCorruptStreamIsAMiss(t *testing.T) {
	mr, cache := newReasoningCodecCache(t)
	ctx := context.Background()
	id := reasoningCodecNativeID(1)
	text := reasoningCodecSampleEntry(5000)
	valid := reasoningCodecGzip(t, []byte(text))
	require.True(t, hasGzipMagic(valid))
	flip := func(s string, at int) string {
		b := []byte(s)
		b[at] ^= 0xff
		return string(b)
	}
	rng := rand.New(rand.NewSource(3))
	corrupt := map[string]string{
		"truncated in the middle":      valid[:len(valid)/2],
		"trailer cut off":              valid[:len(valid)-3],
		"header only":                  valid[:10],
		"magic only":                   "\x1f\x8b\x08",
		"magic plus garbage":           "\x1f\x8b\x08" + reasoningCodecRandom(rng, 100),
		"flipped byte in the data":     flip(valid, len(valid)/2),
		"flipped byte in the CRC":      flip(valid, len(valid)-6),
		"wrong length in the trailer":  flip(valid, len(valid)-1),
		"trailing garbage":             valid + "not a second member",
		"valid header, stored garbage": valid[:10] + reasoningCodecRandom(rng, 200),
	}
	for name, value := range corrupt {
		require.NoError(t, mr.Set(reasoningContentPrefix+id, value), name)
		requireReasoningMiss(t, cache, id, name)

		// 每个坏值之后都读一次好值，确认池里的 reader 没被带坏。
		require.NoError(t, mr.Set(reasoningContentPrefix+id, valid), name)
		got, err := cache.GetReasoningContent(ctx, id)
		require.NoError(t, err, name)
		require.Equal(t, text, got, name)
	}
}

// 极端边界的取舍：Redis 里原样躺着一段以 1f 8b 08 开头、却不是合法 gzip 的值时，读侧无法分辨它是
// 「损坏的压缩流」还是「明文」，按未命中处理（降级，不报错）。通过 SetReasoningContent 写入的明文
// 不会落到这个口子（写侧会把它包一层，见 TestReasoningContentStorageFormat）；旧版本写入的明文是 JSON 或
// 合法 UTF-8 文本，0x8b 是 UTF-8 续字节，不可能以 1f 8b 开头。
func TestReasoningContentGzipLookalikePlaintextInRedisIsAMiss(t *testing.T) {
	mr, cache := newReasoningCodecCache(t)
	for _, id := range []string{reasoningCodecNativeID(1), reasoningCodecLegacyID} {
		require.NoError(t, mr.Set(reasoningContentPrefix+id, "\x1f\x8b\x08 plaintext that merely starts like gzip"))
		requireReasoningMiss(t, cache, id, id)
	}
}

// 并发读写（配合 go test -race）：池里的 writer/reader 被多个 goroutine 复用，各自的读写仍然逐字节正确，
// 竞争同一个 key 时读到的永远是某一次完整写入，不会是混合或损坏的值。
func TestReasoningContentConcurrentRoundTrip(t *testing.T) {
	_, cache := newReasoningCodecCache(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(5))
	payloads := []string{
		reasoningCodecSampleText(600),
		reasoningCodecSampleEntry(6700),
		reasoningCodecSampleText(96 << 10),
		reasoningCodecRandom(rng, 3000),
		"tiny",
		"\x1f\x8b\x08 gzip lookalike " + reasoningCodecSampleText(1000),
		strings.Repeat("z", 100<<10),
	}
	shared := reasoningCodecNativeID(0)

	const workers, rounds = 8, 20
	failures := make(chan string, workers*rounds*3)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			own := reasoningCodecNativeID(w + 1)
			for i := 0; i < rounds; i++ {
				p := payloads[(w+i)%len(payloads)]
				if err := cache.SetReasoningContent(ctx, own, p, time.Minute); err != nil {
					failures <- fmt.Sprintf("worker %d set own: %v", w, err)
					return
				}
				if got, err := cache.GetReasoningContent(ctx, own); err != nil || got != p {
					failures <- fmt.Sprintf("worker %d own key round %d: err=%v wrote %d bytes read %d bytes", w, i, err, len(p), len(got))
				}
				if err := cache.SetReasoningContent(ctx, shared, p, time.Minute); err != nil {
					failures <- fmt.Sprintf("worker %d set shared: %v", w, err)
					return
				}
				if got, err := cache.GetReasoningContent(ctx, shared); err != nil || !slices.Contains(payloads, got) {
					failures <- fmt.Sprintf("worker %d shared key round %d: err=%v read %d bytes that nobody wrote", w, i, err, len(got))
				}
			}
		}(w)
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
}

// BenchmarkReasoningContentCodec 量三个尺寸：6.7KB（生产均值）、164KB（生产观测到的最大）、2MB（写入上限）。
// stored-B / ratio-x 是压缩后字节数与压缩倍数；数据是合成的类推理文本，不是生产数据。
func BenchmarkReasoningContentCodec(b *testing.B) {
	id := reasoningCodecNativeID(1)
	for _, size := range []int{6700, 164 << 10, 2 << 20} {
		entry := reasoningCodecSampleEntry(size)
		stored := encodeReasoningContent(id, entry)
		name := fmt.Sprintf("%dB", len(entry))
		b.Run("encode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(entry)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = encodeReasoningContent(id, entry)
			}
			b.ReportMetric(float64(len(stored)), "stored-B")
			b.ReportMetric(float64(len(entry))/float64(len(stored)), "ratio-x")
		})
		b.Run("decode/"+name, func(b *testing.B) {
			b.SetBytes(int64(len(entry)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, ok := decodeReasoningContent(stored); !ok {
					b.Fatal("decode failed")
				}
			}
		})
	}
}
