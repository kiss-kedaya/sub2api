package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const deepSeekNativeReasoningScopeKey = "deepseek_native_reasoning_scope_v1"
const deepSeekNativeReasoningSourceKey = "deepseek_native_reasoning_source_v1"
const deepSeekNativeReasoningMaxBytes = 2 << 20
const deepSeekNativeReasoningMaxItems = 128

type deepSeekNativeReasoningSource struct {
	keyID        int64
	model        string
	conversation string
	explicit     bool
}

// DeepSeek requires the full reasoning_text of historical assistant turns when
// tools are enabled. Responses clients such as Codex retain summary/encrypted
// fields but may omit the provider-specific content array on replay. Preserve
// only reasoning that this gateway actually received, in shared Redis, scoped
// to the authenticated key, conversation and requested model. Never read the
// older unscoped Chat fallback cache and never substitute a summary for CoT.
func (s *OpenAIGatewayService) prepareDeepSeekNativeReasoningReplay(c *gin.Context, account *Account, body []byte) []byte {
	if s == nil || c == nil || account == nil || account.Platform != PlatformDeepseek || !account.UsesNativeCNResponses() {
		if c != nil {
			c.Set(deepSeekNativeReasoningScopeKey, nil)
			c.Set(deepSeekNativeReasoningSourceKey, nil)
		}
		return body
	}
	// Codex replays previous web search results as synthetic assistant messages
	// ("[Previous web search] ..."). DeepSeek's native Responses endpoint rejects
	// any assistant message without reasoning_text with HTTP 400, even though the
	// message is client context rather than model output. Rewrite only these
	// synthetic messages to the user role; real assistant turns keep their role.
	body = sanitizeDeepSeekNativeResponsesInput(body)
	if s.cache == nil {
		c.Set(deepSeekNativeReasoningScopeKey, nil)
		c.Set(deepSeekNativeReasoningSourceKey, nil)
		return body
	}
	keyID := getAPIKeyIDFromContext(c)
	if keyID <= 0 {
		c.Set(deepSeekNativeReasoningScopeKey, nil)
		c.Set(deepSeekNativeReasoningSourceKey, nil)
		return body
	}
	// A WebSocket may reuse its gin context for many turns. Always derive the
	// current identity again; a prior turn's cached scope is not authorization.
	conversation, _ := resolveOpenAIWSExecutionScope(c, body, keyID)
	source := deepSeekNativeReasoningSource{keyID: keyID, model: gjson.GetBytes(body, "model").String(), conversation: conversation, explicit: conversation != ""}
	if conversation == "" {
		source.conversation = deriveOpenAIAnchoredContentSessionSeed(body)
		// Internal hosted-tool recursion changes tool declarations. Keep its
		// original anonymous conversation identity only inside that trusted
		// request context, after rechecking key/model and absence of an explicit
		// identity. Public headers/body cannot set this private context marker.
		previous, _ := c.Get(deepSeekNativeReasoningSourceKey)
		if old, ok := previous.(deepSeekNativeReasoningSource); ok && c.Request != nil &&
			c.Request.Context().Value(deepSeekHostedSearchSkipContextKey{}) == true &&
			!old.explicit && old.keyID == source.keyID && old.model == source.model {
			source.conversation = old.conversation
		}
	}
	if source.conversation == "" {
		c.Set(deepSeekNativeReasoningScopeKey, nil)
		c.Set(deepSeekNativeReasoningSourceKey, nil)
		return body
	}
	scopeString := deepSeekNativeReasoningDigest(fmt.Sprintf("%d\x00%s\x00%s", source.keyID, source.conversation, source.model))
	c.Set(deepSeekNativeReasoningScopeKey, scopeString)
	c.Set(deepSeekNativeReasoningSourceKey, source)
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	items := input.Array()
	replacements := make(map[int][]byte)
	addedBytes := 0
	lookups := 0
	for i, item := range items {
		if item.Get("type").String() != "reasoning" || item.Get("id").String() == "" || hasDeepSeekNativeReasoningText(item.Get("content")) {
			continue
		}
		// An explicit non-empty vendor shape belongs to the client. Only fill
		// omitted/null/empty arrays, and leave all other fields byte-for-byte.
		content := item.Get("content")
		if content.Exists() && content.Type != gjson.Null && (!content.IsArray() || len(content.Array()) > 0) {
			continue
		}
		lookups++
		if lookups > deepSeekNativeReasoningMaxItems || ctx.Err() != nil {
			break
		}
		cached, err := s.cache.GetReasoningContent(ctx, deepSeekNativeReasoningCacheKey(source.keyID, item.Get("id").String()))
		if err != nil || len(cached) > deepSeekNativeReasoningMaxBytes {
			continue
		}
		var entry deepSeekNativeReasoningEntry
		if json.Unmarshal([]byte(cached), &entry) != nil || !hasDeepSeekNativeReasoningText(gjson.ParseBytes(entry.Content)) {
			continue
		}
		encryptedDigest := ""
		if encrypted := item.Get("encrypted_content").String(); encrypted != "" {
			encryptedDigest = deepSeekNativeReasoningDigest(encrypted)
		}
		// Absence is part of the binding too: dropping the opaque value must
		// not turn an encrypted item into an unbound item-id lookup.
		if encryptedDigest != entry.EncryptedDigest {
			continue
		}
		if len(entry.Content) > deepSeekNativeReasoningMaxBytes-addedBytes {
			break
		}
		if updated, err := sjson.SetRawBytes([]byte(item.Raw), "content", entry.Content); err == nil {
			added := max(len(entry.Content), len(updated)-len(item.Raw))
			if added > deepSeekNativeReasoningMaxBytes-addedBytes {
				break
			}
			replacements[i] = updated
			addedBytes += added
		}
	}
	if len(replacements) == 0 {
		return body
	}
	// Rebuild the input and request once. Rewriting the entire growing request
	// once per restored item would multiply both allocations and copy time.
	var restored bytes.Buffer
	restored.Grow(len(input.Raw) + addedBytes)
	_ = restored.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			_ = restored.WriteByte(',')
		}
		if replacement, ok := replacements[i]; ok {
			_, _ = restored.Write(replacement)
		} else {
			_, _ = restored.WriteString(item.Raw)
		}
	}
	_ = restored.WriteByte(']')
	if next, err := sjson.SetRawBytes(body, "input", restored.Bytes()); err == nil {
		return next
	}
	return body
}

func deepSeekNativeReasoningDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func deepSeekNativeReasoningCacheKey(keyID int64, itemID string) string {
	return fmt.Sprintf("deepseek-native-v1:%d:%s", keyID, deepSeekNativeReasoningDigest(itemID))
}

type deepSeekNativeReasoningEntry struct {
	Content         json.RawMessage `json:"content"`
	EncryptedDigest string          `json:"encrypted_digest,omitempty"`
}

func hasDeepSeekNativeReasoningText(content gjson.Result) bool {
	if !content.IsArray() {
		return false
	}
	for _, part := range content.Array() {
		if part.Get("type").String() == "reasoning_text" && part.Get("text").Type == gjson.String && part.Get("text").String() != "" {
			return true
		}
	}
	return false
}

// sanitizeDeepSeekNativeResponsesInput rewrites synthetic assistant messages
// that carry client-side context (normally "[Previous ...]" tool results) to
// the user role. DeepSeek's native Responses API requires every assistant
// message in thinking mode to carry reasoning_text; these messages never have
// it because the client created them, not the model. Converting them preserves
// the context and avoids inventing chain-of-thought.
func sanitizeDeepSeekNativeResponsesInput(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	items := input.Array()
	changed := false
	for i, item := range items {
		if item.Get("type").String() != "message" || item.Get("role").String() != "assistant" {
			continue
		}
		text := strings.TrimSpace(deepSeekNativeMessageText(item.Get("content")))
		if !strings.HasPrefix(text, "[Previous ") {
			continue
		}
		updated, err := sjson.SetBytes([]byte(item.Raw), "role", "user")
		if err != nil {
			continue
		}
		items[i] = gjson.ParseBytes(updated)
		changed = true
	}
	if !changed {
		return body
	}
	var restored bytes.Buffer
	restored.Grow(len(input.Raw))
	_ = restored.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			_ = restored.WriteByte(',')
		}
		_, _ = restored.WriteString(item.Raw)
	}
	_ = restored.WriteByte(']')
	if next, err := sjson.SetRawBytes(body, "input", restored.Bytes()); err == nil {
		return next
	}
	return body
}

func deepSeekNativeMessageText(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Array() {
		if part.Get("text").Type == gjson.String {
			_, _ = builder.WriteString(part.Get("text").String())
		}
	}
	return builder.String()
}

type deepSeekNativeReasoningItem struct {
	ids       map[string]bool
	parts     map[int]*strings.Builder
	encrypted string
	opaqueIDs map[string]string
	content   string
	stored    map[string]string
	bytes     int
}

type deepSeekNativeReasoningRecorder struct {
	s              *OpenAIGatewayService
	scope          string
	keyID          int64
	items          map[int]*deepSeekNativeReasoningItem
	bytes          int
	cacheWriteTime time.Duration
	cacheFailed    bool
}

func (s *OpenAIGatewayService) newDeepSeekNativeReasoningRecorder(c *gin.Context, account *Account) *deepSeekNativeReasoningRecorder {
	if s == nil || s.cache == nil || c == nil || account == nil || account.Platform != PlatformDeepseek || !account.UsesNativeCNResponses() {
		return nil
	}
	value, _ := c.Get(deepSeekNativeReasoningScopeKey)
	scope, _ := value.(string)
	identity, _ := c.Get(deepSeekNativeReasoningSourceKey)
	source, ok := identity.(deepSeekNativeReasoningSource)
	if scope == "" || !ok || source.keyID != getAPIKeyIDFromContext(c) {
		return nil
	}
	return &deepSeekNativeReasoningRecorder{s: s, scope: scope, keyID: source.keyID, items: make(map[int]*deepSeekNativeReasoningItem)}
}

func (r *deepSeekNativeReasoningRecorder) item(index int, id string) *deepSeekNativeReasoningItem {
	if r == nil || r.cacheFailed || index < 0 || index >= deepSeekNativeReasoningMaxItems || r.bytes > deepSeekNativeReasoningMaxBytes {
		return nil
	}
	item := r.items[index]
	if item == nil {
		item = &deepSeekNativeReasoningItem{ids: make(map[string]bool), parts: make(map[int]*strings.Builder), opaqueIDs: make(map[string]string), stored: make(map[string]string)}
		r.items[index] = item
	}
	if id != "" && len(id) <= 1024 && len(item.ids) < 4 {
		item.ids[id] = true
	}
	return item
}

func (r *deepSeekNativeReasoningRecorder) ObserveFrame(frame openAISSEDataFrame) {
	if r == nil || !frame.validJSON {
		return
	}
	switch frame.eventType {
	case "response.output_item.added", "response.output_item.done":
		item := frame.root.Get("item")
		if item.Get("type").String() == "reasoning" {
			r.observeItem(int(frame.root.Get("output_index").Int()), item, frame.eventType == "response.output_item.done")
		}
	case "response.reasoning_text.delta", "response.reasoning_text.done":
		item := r.item(int(frame.root.Get("output_index").Int()), frame.root.Get("item_id").String())
		index := int(frame.root.Get("content_index").Int())
		if item == nil || index < 0 || index >= deepSeekNativeReasoningMaxItems {
			return
		}
		text := frame.root.Get("delta").String()
		if frame.eventType == "response.reasoning_text.done" {
			text = frame.root.Get("text").String()
			if previous := item.parts[index]; previous != nil {
				r.bytes -= previous.Len()
				item.bytes -= previous.Len()
			}
			item.parts[index] = &strings.Builder{}
		}
		r.bytes += len(text)
		item.bytes += len(text)
		if r.bytes > deepSeekNativeReasoningMaxBytes {
			return
		}
		if item.parts[index] == nil {
			item.parts[index] = &strings.Builder{}
		}
		_, _ = item.parts[index].WriteString(text)
	case "response.completed", "response.done", "response.incomplete":
		r.ObserveResponse(frame.root.Get("response"))
		if frame.eventType != "response.incomplete" {
			// Some providers omit output from the successful terminal and omit
			// content from item.done. The complete delta stream is authoritative.
			for index := range r.items {
				r.observeItem(index, gjson.Result{}, true)
			}
		}
	}
}

func (r *deepSeekNativeReasoningRecorder) ObserveResponse(response gjson.Result) {
	if r == nil {
		return
	}
	for index, item := range response.Get("output").Array() {
		if item.Get("type").String() == "reasoning" {
			r.observeItem(index, item, true)
		}
	}
}

func (r *deepSeekNativeReasoningRecorder) observeItem(index int, raw gjson.Result, complete bool) {
	item := r.item(index, raw.Get("id").String())
	if item == nil {
		return
	}
	if encrypted := raw.Get("encrypted_content").String(); encrypted != "" {
		item.encrypted = deepSeekNativeReasoningDigest(encrypted)
		if id := raw.Get("id").String(); item.ids[id] {
			item.opaqueIDs[id] = item.encrypted
		}
	}
	content := raw.Get("content")
	if !complete {
		return
	}
	if !hasDeepSeekNativeReasoningText(content) && item.content != "" {
		content = gjson.Parse(item.content)
	}
	if !hasDeepSeekNativeReasoningText(content) {
		indexes := make([]int, 0, len(item.parts))
		for i := range item.parts {
			indexes = append(indexes, i)
		}
		sort.Ints(indexes)
		parts := make([]map[string]string, 0, len(indexes))
		for _, i := range indexes {
			parts = append(parts, map[string]string{"type": "reasoning_text", "text": item.parts[i].String()})
		}
		encoded, _ := json.Marshal(parts)
		content = gjson.ParseBytes(encoded)
	}
	if !hasDeepSeekNativeReasoningText(content) || len(content.Raw) > deepSeekNativeReasoningMaxBytes || r.bytes > deepSeekNativeReasoningMaxBytes {
		return
	}
	if len(content.Raw) > item.bytes {
		r.bytes += len(content.Raw) - item.bytes
		item.bytes = len(content.Raw)
	}
	if r.bytes > deepSeekNativeReasoningMaxBytes {
		return
	}
	item.content = content.Raw
	remaining := 2*time.Second - r.cacheWriteTime
	if remaining <= 0 {
		r.cacheFailed = true
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	started := time.Now()
	defer func() { r.cacheWriteTime += time.Since(started) }()
	for id := range item.ids {
		encrypted := item.opaqueIDs[id]
		if encrypted == "" {
			encrypted = item.encrypted
		}
		entry, err := json.Marshal(deepSeekNativeReasoningEntry{Content: json.RawMessage(content.Raw), EncryptedDigest: encrypted})
		if err != nil {
			continue
		}
		digest := deepSeekNativeReasoningDigest(string(entry))
		if item.stored[id] == digest {
			continue
		}
		if err := r.s.cache.SetReasoningContent(ctx, deepSeekNativeReasoningCacheKey(r.keyID, id), string(entry), responsesReasoningCacheTTL); err == nil {
			item.stored[id] = digest
		} else {
			// The cache is best-effort. One failure disables writes for this
			// response, so a Redis outage never adds two seconds per frame/item.
			r.cacheFailed = true
			break
		}
	}
}

func (s *OpenAIGatewayService) cacheDeepSeekNativeReasoningBody(c *gin.Context, account *Account, body []byte) {
	r := s.newDeepSeekNativeReasoningRecorder(c, account)
	if r == nil {
		return
	}
	if gjson.ValidBytes(body) {
		r.ObserveResponse(gjson.ParseBytes(body))
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		if data, ok := extractOpenAISSEDataLine(line); ok {
			r.ObserveFrame(parseOpenAISSEDataFrame([]byte(data), ""))
		}
	}
}
