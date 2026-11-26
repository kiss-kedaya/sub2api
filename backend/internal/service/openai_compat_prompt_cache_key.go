package service

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const compatPromptCacheKeyPrefix = "compat_cc_"

func shouldAutoInjectPromptCacheKeyForCompat(model string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(model))
	canonical := canonicalizeOpenAIModelAliasSpelling(trimmed)
	// GPT-6 Sol/Luna share the supported Responses cache identity contract.
	if canonical == "gpt-6" || canonical == "gpt-6-astra" || openai.IsGPT6SolOrLunaModelSpelling(canonical) || openai.IsGPT61SolModelSpelling(canonical) || openai.IsGPT61TerraModelSpelling(canonical) {
		return true
	}
	// 仅对 Responses 兼容路径支持的 GPT-5 族开启自动注入，避免 normalizeCodexModel
	// 的默认兜底把任意模型（如 gpt-4o、claude-*）误判为 gpt-5.4。
	if !strings.Contains(trimmed, "gpt-5") && !strings.Contains(trimmed, "codex") {
		return false
	}
	normalized := strings.TrimSpace(strings.ToLower(normalizeCodexModel(trimmed)))
	return strings.HasPrefix(normalized, "gpt-5") || strings.Contains(normalized, "codex")
}
func deriveCompatPromptCacheKey(req *apicompat.ChatCompletionsRequest, mappedModel string) string {
	if req == nil {
		return ""
	}

	normalizedModel := normalizeCodexModel(strings.TrimSpace(mappedModel))
	if normalizedModel == "" {
		normalizedModel = normalizeCodexModel(strings.TrimSpace(req.Model))
	}
	if normalizedModel == "" {
		normalizedModel = strings.TrimSpace(req.Model)
	}

	seedParts := []string{"model=" + normalizedModel}
	if req.ReasoningEffort != "" {
		seedParts = append(seedParts, "reasoning_effort="+strings.TrimSpace(req.ReasoningEffort))
	}
	if len(req.ToolChoice) > 0 {
		seedParts = append(seedParts, "tool_choice="+normalizeCompatSeedJSON(req.ToolChoice))
	}
	if len(req.Tools) > 0 {
		if raw, err := json.Marshal(req.Tools); err == nil {
			seedParts = append(seedParts, "tools="+normalizeCompatSeedJSON(raw))
		}
	}
	if len(req.Functions) > 0 {
		if raw, err := json.Marshal(req.Functions); err == nil {
			seedParts = append(seedParts, "functions="+normalizeCompatSeedJSON(raw))
		}
	}

	firstUserCaptured := false
	for _, msg := range req.Messages {
		switch strings.TrimSpace(msg.Role) {
		case "system":
			seedParts = append(seedParts, "system="+normalizeCompatSeedJSON(msg.Content))
		case "user":
			if !firstUserCaptured {
				seedParts = append(seedParts, "first_user="+normalizeCompatSeedJSON(msg.Content))
				firstUserCaptured = true
			}
		}
	}

	return compatPromptCacheKeyPrefix + hashSensitiveValueForLog(strings.Join(seedParts, "|"))
}

func deriveAnthropicCompatPromptCacheKey(req *apicompat.AnthropicRequest, mappedModel string) string {
	if req == nil {
		return ""
	}
	if anchorKey := deriveAnthropicCacheControlPromptCacheKey(req); anchorKey != "" {
		return anchorKey
	}

	normalizedModel := normalizeCodexModel(strings.TrimSpace(mappedModel))
	if normalizedModel == "" {
		normalizedModel = normalizeCodexModel(strings.TrimSpace(req.Model))
	}
	if normalizedModel == "" {
		normalizedModel = strings.TrimSpace(req.Model)
	}

	seedParts := []string{"model=" + normalizedModel}
	if req.OutputConfig != nil && strings.TrimSpace(req.OutputConfig.Effort) != "" {
		seedParts = append(seedParts, "effort="+strings.TrimSpace(req.OutputConfig.Effort))
	}
	if len(req.ToolChoice) > 0 {
		seedParts = append(seedParts, "tool_choice="+normalizeCompatSeedJSON(req.ToolChoice))
	}
	if len(req.Tools) > 0 {
		if raw, err := json.Marshal(req.Tools); err == nil {
			seedParts = append(seedParts, "tools="+normalizeCompatSeedJSON(raw))
		}
	}
	if len(req.System) > 0 {
		seedParts = append(seedParts, "system="+normalizeCompatSeedJSON(req.System))
	}

	firstUserCaptured := false
	for _, msg := range req.Messages {
		if strings.TrimSpace(msg.Role) != "user" || firstUserCaptured {
			continue
		}
		seedParts = append(seedParts, "first_user="+normalizeCompatSeedJSON(msg.Content))
		firstUserCaptured = true
	}

	return compatPromptCacheKeyPrefix + hashSensitiveValueForLog(strings.Join(seedParts, "|"))
}

func deriveAnthropicCacheControlPromptCacheKey(req *apicompat.AnthropicRequest) string {
	if req == nil {
		return ""
	}

	var parts []string
	var systemBlocks []apicompat.AnthropicContentBlock
	if len(req.System) > 0 && json.Unmarshal(req.System, &systemBlocks) == nil {
		for _, block := range systemBlocks {
			if block.Type == "text" &&
				block.CacheControl != nil &&
				strings.TrimSpace(block.CacheControl.Type) == "ephemeral" &&
				strings.TrimSpace(block.Text) != "" {
				parts = append(parts, "system:"+strings.TrimSpace(block.Text))
			}
		}
	}

	firstUserAnchor := ""
	for _, msg := range req.Messages {
		var blocks []apicompat.AnthropicContentBlock
		if len(msg.Content) == 0 || json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		role := strings.TrimSpace(msg.Role)
		for _, block := range blocks {
			if block.Type != "text" ||
				block.CacheControl == nil ||
				strings.TrimSpace(block.CacheControl.Type) != "ephemeral" ||
				strings.TrimSpace(block.Text) == "" {
				continue
			}
			switch role {
			case "user":
				if firstUserAnchor == "" {
					firstUserAnchor = strings.TrimSpace(block.Text)
				}
			case "assistant":
				parts = append(parts, "assistant:"+strings.TrimSpace(block.Text))
			}
		}
	}
	if firstUserAnchor != "" {
		parts = append(parts, "user_anchor:"+firstUserAnchor)
	}
	if len(parts) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte("anthropic-cache:" + strings.Join(parts, "\n")))
	return fmt.Sprintf("anthropic-cache-%x", sum[:16])
}

func normalizeCompatSeedJSON(v json.RawMessage) string {
	if len(v) < compatSeedMemoMinItem || len(v) > compatSeedMemoMaxItem {
		return normalizeCompatSeedJSONUncached(v)
	}
	if out, ok := compatSeedMemo.get(v); ok {
		return out
	}
	out := normalizeCompatSeedJSONUncached(v)
	compatSeedMemo.put(v, out)
	return out
}

// normalizeCompatSeedJSONUncached re-encodes v canonically without building
// generic map[string]any / []any trees, preserving exact encoding/json output
// (sorted keys, last-wins duplicate keys, float64 formatting, and HTML/U+2028/
// U+2029 escaping). Input that is not valid JSON is returned as is.
func normalizeCompatSeedJSONUncached(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var memberBack [32]canonMember
	var keyBack [256]byte
	var strBack [512]byte
	var outBack [1024]byte
	scratch := canonScratch{
		members: memberBack[:0],
		keyBuf:  keyBack[:0],
		strBuf:  strBack[:0],
	}
	out, ok := appendNormalizedCompatSeedJSON(outBack[:0], jsonView(v), &scratch)
	if !ok {
		return string(v)
	}
	return string(out)
}

// appendCompatSeedJSON appends the canonicalized JSON representation of raw
// into dst, consulting compatSeedMemo for medium-sized items and streaming
// directly into dst without intermediate string allocations otherwise.
func appendCompatSeedJSON(dst []byte, raw string, scratch *canonScratch) []byte {
	if raw == "" {
		return dst
	}
	if len(raw) >= compatSeedMemoMinItem && len(raw) <= compatSeedMemoMaxItem {
		b := unsafe.Slice(unsafe.StringData(raw), len(raw))
		if out, ok := compatSeedMemo.get(b); ok {
			return append(dst, out...)
		}
		base := len(dst)
		dst, ok := appendNormalizedCompatSeedJSON(dst, raw, scratch)
		if ok {
			compatSeedMemo.put(b, string(dst[base:]))
		} else {
			compatSeedMemo.put(b, raw)
		}
		return dst
	}
	dst, _ = appendNormalizedCompatSeedJSON(dst, raw, scratch)
	return dst
}

type canonMember struct {
	key      string
	keyStart int
	keyEnd   int
	valStart int
	order    int32
	keyInBuf bool
}

type canonScratch struct {
	members []canonMember
	keyBuf  []byte
	strBuf  []byte
}

func appendNormalizedCompatSeedJSON(dst []byte, src string, scratch *canonScratch) ([]byte, bool) {
	if len(src) == 0 {
		return dst, true
	}
	if !validJSON(unsafe.Slice(unsafe.StringData(src), len(src))) {
		return append(dst, src...), false
	}
	base := len(dst)
	dst, _, ok := appendCanonicalJSONValue(dst, src, 0, scratch)
	if !ok {
		return append(dst[:base], src...), false
	}
	return dst, true
}

func appendCanonicalJSONValue(dst []byte, src string, i int, s *canonScratch) ([]byte, int, bool) {
	i = jsonSkipSpace(src, i)
	if i >= len(src) {
		return dst, i, false
	}
	switch src[i] {
	case 'n':
		return append(dst, "null"...), i + 4, true
	case 't':
		return append(dst, "true"...), i + 4, true
	case 'f':
		return append(dst, "false"...), i + 5, true
	case '"':
		end, esc, _ := jsonSkipString(src, i)
		inner := src[i+1 : end-1]
		if !esc && canonJSONPlainASCII(inner) {
			return append(dst, src[i:end]...), end, true
		}
		s.strBuf = appendUnquotedJSONString(s.strBuf[:0], inner)
		return appendJSONEscapedBytes(dst, s.strBuf), end, true
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		end := i
		hasSpecial := false
		for end < len(src) {
			c := src[end]
			if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			if c == '.' || c == 'e' || c == 'E' {
				hasSpecial = true
			}
			end++
		}
		num := src[i:end]
		if !hasSpecial && len(num) <= 15 && num != "-0" {
			return append(dst, num...), end, true
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return dst, i, false
		}
		abs := math.Abs(f)
		fmtByte := byte('f')
		if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
			fmtByte = 'e'
		}
		dst = strconv.AppendFloat(dst, f, fmtByte, -1, 64)
		if fmtByte == 'e' {
			n := len(dst)
			if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
				dst[n-2] = dst[n-1]
				dst = dst[:n-1]
			}
		}
		return dst, end, true
	case '[':
		dst = append(dst, '[')
		i++
		first := true
		for {
			i = jsonSkipSpace(src, i)
			if src[i] == ']' {
				return append(dst, ']'), i + 1, true
			}
			if src[i] == ',' {
				i++
				continue
			}
			if !first {
				dst = append(dst, ',')
			}
			first = false
			var ok bool
			dst, i, ok = appendCanonicalJSONValue(dst, src, i, s)
			if !ok {
				return dst, i, false
			}
		}
	case '{':
		mBase := len(s.members)
		kBase := len(s.keyBuf)
		i++
		var order int32
		for {
			i = jsonSkipSpace(src, i)
			if src[i] == '}' {
				i++
				break
			}
			if src[i] == ',' {
				i++
				continue
			}
			kEnd, kEsc, _ := jsonSkipString(src, i)
			kInner := src[i+1 : kEnd-1]
			var m canonMember
			if !kEsc && canonIsASCIIBytes(kInner) {
				m.key = kInner
			} else {
				m.keyStart = len(s.keyBuf)
				s.keyBuf = appendUnquotedJSONString(s.keyBuf, kInner)
				m.keyEnd = len(s.keyBuf)
				m.keyInBuf = true
			}
			i = jsonSkipSpace(src, kEnd)
			i = jsonSkipSpace(src, i+1) // skip ':'
			m.valStart = i
			m.order = order
			order++
			s.members = append(s.members, m)
			i, _ = jsonSkipValue(src, i)
		}

		members := s.members[mBase:]
		switch len(members) {
		case 0:
			dst = append(dst, '{', '}')
		case 1:
			dst = append(dst, '{')
			dst = appendJSONEscapedString(dst, canonMemberKey(members[0], s.keyBuf))
			dst = append(dst, ':')
			var ok bool
			dst, _, ok = appendCanonicalJSONValue(dst, src, members[0].valStart, s)
			if !ok {
				s.members = s.members[:mBase]
				s.keyBuf = s.keyBuf[:kBase]
				return dst, i, false
			}
			dst = append(dst, '}')
		default:
			sorted := true
			prevKey := canonMemberKey(members[0], s.keyBuf)
			for j := 1; j < len(members); j++ {
				curKey := canonMemberKey(members[j], s.keyBuf)
				if prevKey >= curKey {
					sorted = false
					break
				}
				prevKey = curKey
			}
			if !sorted {
				keyBuf := s.keyBuf
				slices.SortFunc(members, func(a, b canonMember) int {
					if c := cmp.Compare(canonMemberKey(a, keyBuf), canonMemberKey(b, keyBuf)); c != 0 {
						return c
					}
					return cmp.Compare(a.order, b.order)
				})
			}
			dst = append(dst, '{')
			emitted := false
			for j := 0; j < len(members); j++ {
				curKey := canonMemberKey(members[j], s.keyBuf)
				if j+1 < len(members) && curKey == canonMemberKey(members[j+1], s.keyBuf) {
					continue
				}
				if emitted {
					dst = append(dst, ',')
				}
				emitted = true
				dst = appendJSONEscapedString(dst, curKey)
				dst = append(dst, ':')
				var ok bool
				dst, _, ok = appendCanonicalJSONValue(dst, src, members[j].valStart, s)
				if !ok {
					s.members = s.members[:mBase]
					s.keyBuf = s.keyBuf[:kBase]
					return dst, i, false
				}
			}
			dst = append(dst, '}')
		}
		s.members = s.members[:mBase]
		s.keyBuf = s.keyBuf[:kBase]
		return dst, i, true
	default:
		return dst, i, false
	}
}

func canonMemberKey(m canonMember, keyBuf []byte) string {
	if m.keyInBuf {
		return jsonView(keyBuf[m.keyStart:m.keyEnd])
	}
	return m.key
}

func canonIsASCIIBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func canonJSONPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf || c == '<' || c == '>' || c == '&' {
			return false
		}
	}
	return true
}

func appendUnquotedJSONString(dst []byte, s string) []byte {
	r := 0
	for r < len(s) {
		c := s[r]
		if c == '\\' || c < utf8.RuneSelf {
			if c == '\\' {
				break
			}
			r++
			continue
		}
		rr, size := utf8.DecodeRuneInString(s[r:])
		if rr == utf8.RuneError && size == 1 {
			break
		}
		r += size
	}
	if r == len(s) {
		return append(dst, s...)
	}
	dst = append(dst, s[:r]...)
	for r < len(s) {
		c := s[r]
		switch {
		case c == '\\':
			r++
			if r >= len(s) {
				return dst
			}
			switch s[r] {
			case '"', '\\', '/', '\'':
				dst = append(dst, s[r])
				r++
			case 'b':
				dst = append(dst, '\b')
				r++
			case 'f':
				dst = append(dst, '\f')
				r++
			case 'n':
				dst = append(dst, '\n')
				r++
			case 'r':
				dst = append(dst, '\r')
				r++
			case 't':
				dst = append(dst, '\t')
				r++
			case 'u':
				rr := parseJSONHex4(s[r+1 : r+5])
				r += 5
				if utf16.IsSurrogate(rr) && r+6 <= len(s) && s[r] == '\\' && s[r+1] == 'u' {
					rr1 := parseJSONHex4(s[r+2 : r+6])
					if dec := utf16.DecodeRune(rr, rr1); dec != unicode.ReplacementChar {
						r += 6
						dst = utf8.AppendRune(dst, dec)
						continue
					}
					rr = unicode.ReplacementChar
				} else if utf16.IsSurrogate(rr) {
					rr = unicode.ReplacementChar
				}
				dst = utf8.AppendRune(dst, rr)
			default:
				dst = append(dst, s[r])
				r++
			}
		case c < utf8.RuneSelf:
			dst = append(dst, c)
			r++
		default:
			rr, size := utf8.DecodeRuneInString(s[r:])
			r += size
			dst = utf8.AppendRune(dst, rr)
		}
	}
	return dst
}

func parseJSONHex4(s string) rune {
	var r rune
	for i := 0; i < 4; i++ {
		c := s[i]
		var v rune
		switch {
		case c >= '0' && c <= '9':
			v = rune(c - '0')
		case c >= 'a' && c <= 'f':
			v = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = rune(c-'A') + 10
		}
		r = r<<4 | v
	}
	return r
}

const canonHexDigits = "0123456789abcdef"

var canonSafeSet = [utf8.RuneSelf]bool{
	' ': true, '!': true, '#': true, '$': true, '%': true, '\'': true, '(': true, ')': true,
	'*': true, '+': true, ',': true, '-': true, '.': true, '/': true, '0': true, '1': true,
	'2': true, '3': true, '4': true, '5': true, '6': true, '7': true, '8': true, '9': true,
	':': true, ';': true, '=': true, '?': true, '@': true, 'A': true, 'B': true, 'C': true,
	'D': true, 'E': true, 'F': true, 'G': true, 'H': true, 'I': true, 'J': true, 'K': true,
	'L': true, 'M': true, 'N': true, 'O': true, 'P': true, 'Q': true, 'R': true, 'S': true,
	'T': true, 'U': true, 'V': true, 'W': true, 'X': true, 'Y': true, 'Z': true, '[': true,
	']': true, '^': true, '_': true, '`': true, 'a': true, 'b': true, 'c': true, 'd': true,
	'e': true, 'f': true, 'g': true, 'h': true, 'i': true, 'j': true, 'k': true, 'l': true,
	'm': true, 'n': true, 'o': true, 'p': true, 'q': true, 'r': true, 's': true, 't': true,
	'u': true, 'v': true, 'w': true, 'x': true, 'y': true, 'z': true, '{': true, '|': true,
	'}': true, '~': true, '\x7f': true,
}

func appendJSONEscapedBytes(dst, src []byte) []byte {
	return appendJSONEscapedString(dst, jsonView(src))
}

func appendJSONEscapedString(dst []byte, src string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(src); {
		if b := src[i]; b < utf8.RuneSelf {
			if canonSafeSet[b] {
				i++
				continue
			}
			dst = append(dst, src[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', canonHexDigits[b>>4], canonHexDigits[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(src[i:])
		if c == utf8.RuneError && size == 1 {
			dst = append(dst, src[start:i]...)
			dst = append(dst, `\ufffd`...)
			i += size
			start = i
			continue
		}
		if c == '\u2028' || c == '\u2029' {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', canonHexDigits[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, src[start:]...)
	return append(dst, '"')
}

// The same tool arrays, system prompts and history items are canonicalized on
// every request of a conversation (session seeds, transcript block keys), and
// the result depends on the input bytes alone, so repeats are answered from a
// bounded memo instead of a decode/encode round trip. Small inputs are cheaper
// to recompute than to look up, and oversized ones would crowd the memo.
const (
	compatSeedMemoMinItem  = 256
	compatSeedMemoMaxItem  = 256 << 10
	compatSeedMemoShards   = 16
	compatSeedMemoMaxBytes = 32 << 20 // per generation, across all shards
)

var compatSeedMemo = newCompatSeedMemo(compatSeedMemoShards, compatSeedMemoMaxBytes)

// compatSeedMemoShard keeps two generations: a hit in the old one moves the
// entry forward, and when the current one fills up the old one is dropped.
type compatSeedMemoShard struct {
	mu       sync.Mutex
	cur, old map[string]string
	curBytes int
	maxBytes int
}

type compatSeedMemoCache struct {
	shards []compatSeedMemoShard
}

func newCompatSeedMemo(shards, maxBytes int) *compatSeedMemoCache {
	c := &compatSeedMemoCache{shards: make([]compatSeedMemoShard, shards)}
	for i := range c.shards {
		c.shards[i].maxBytes = maxBytes / shards
		c.shards[i].cur = make(map[string]string)
	}
	return c
}

// shard picks a shard from the length and the first and last bytes: cheap, and
// the same input always lands in the same shard.
func (c *compatSeedMemoCache) shard(v []byte) *compatSeedMemoShard {
	h := uint32(len(v)) * 2654435761
	for i := 0; i < 16 && i < len(v); i++ {
		h = h*16777619 ^ uint32(v[i])
		h = h*16777619 ^ uint32(v[len(v)-1-i])
	}
	return &c.shards[h%uint32(len(c.shards))]
}

func (c *compatSeedMemoCache) get(v []byte) (string, bool) {
	view := jsonView(v)
	s := c.shard(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	if out, ok := s.cur[view]; ok {
		return out, true
	}
	if out, ok := s.old[view]; ok {
		key := out
		if out != view {
			key = string(v)
		}
		s.putLocked(key, out)
		return out, true
	}
	return "", false
}

func (c *compatSeedMemoCache) put(v []byte, out string) {
	key := out
	if out != jsonView(v) {
		key = string(v)
	}
	s := c.shard(v)
	s.mu.Lock()
	s.putLocked(key, out)
	s.mu.Unlock()
}

func (s *compatSeedMemoShard) putLocked(key, out string) {
	size := len(key) + len(out)
	if size > s.maxBytes {
		return
	}
	if s.curBytes+size > s.maxBytes {
		s.old, s.cur, s.curBytes = s.cur, make(map[string]string), 0
	}
	if _, exists := s.cur[key]; !exists {
		s.cur[key] = out
		s.curBytes += size
	}
}
