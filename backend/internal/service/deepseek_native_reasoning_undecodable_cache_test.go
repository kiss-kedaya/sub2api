//go:build unit

package service

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// repository 层会对 DeepSeek 原生 reasoning 缓存值做透明 gzip 压缩。蓝绿期间和回滚后，旧进程会读到新进程
// 写进共享 Redis 的压缩值。旧进程的读侧与当前代码相同：这条测试证明读到任何「不是 JSON 的值」都只是
// 未命中——补中性占位符、请求照常发出，不 panic、不 400，与 Redis 被清空时一样。
func TestDeepSeekNativeReasoningReplayTreatsUndecodableCacheValueAsMiss(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekNativeReplayAccount()

	// 用真实写入路径造一条缓存条目。
	cache := &reasoningRecordingCache{}
	writer := &OpenAIGatewayService{cache: cache}
	c, _ := deepSeekNativeReplayContext(21, "gzip-thread")
	writer.prepareDeepSeekNativeReasoningReplay(c, account, []byte(`{"model":"deepseek-v4.1-flash","input":"question"}`))
	writer.cacheDeepSeekNativeReasoningBody(c, account, []byte(`{"output":[{"type":"reasoning","id":"rs_gz","encrypted_content":"opaque","content":[{"type":"reasoning_text","text":"`+strings.Repeat("deep thought ", 200)+`"}]}]}`))
	written := cache.snapshotSets()
	require.Len(t, written, 1)

	gz := func(s string) string {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, err := zw.Write([]byte(s))
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		return buf.String()
	}
	replay := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"reasoning","id":"rs_gz","summary":[],"encrypted_content":"opaque"}]}`)

	// 对照：原样的值会被还原，说明下面的占位符确实来自「读不懂」而不是别的原因。
	reader := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: written}}
	c, _ = deepSeekNativeReplayContext(21, "gzip-thread")
	out := reader.prepareDeepSeekNativeReasoningReplay(c, account, replay)
	require.Contains(t, gjson.GetBytes(out, "input.0.content.0.text").String(), "deep thought")

	for name, mutate := range map[string]func(string) string{
		"gzip, what a newer process stores": gz,
		"truncated gzip":                    func(s string) string { z := gz(s); return z[:len(z)/2] },
		"binary garbage": func(s string) string {
			return "\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xff" + strings.Repeat("\x80\x00\xfe", 50)
		},
		"half a JSON document": func(s string) string { return s[:len(s)/2] },
	} {
		undecodable := make(map[string]string, len(written))
		for key, value := range written {
			undecodable[key] = mutate(value)
		}
		reader := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: undecodable}}
		c, _ := deepSeekNativeReplayContext(21, "gzip-thread")
		var out []byte
		require.NotPanics(t, func() { out = reader.prepareDeepSeekNativeReasoningReplay(c, account, replay) }, name)
		reasoning := gjson.GetBytes(out, `input.#(type=="reasoning")`)
		require.True(t, reasoning.Exists(), "%s: the reasoning item must survive", name)
		require.Equal(t, deepSeekReasoningPlaceholderText, reasoning.Get("content.0.text").String(), "%s: a miss gets the neutral placeholder", name)
		require.Equal(t, "opaque", reasoning.Get("encrypted_content").String(), name)
	}
}
