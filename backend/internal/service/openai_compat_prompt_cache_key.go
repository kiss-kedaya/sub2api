package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
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

// normalizeCompatSeedJSONUncached re-encodes v canonically: decoded into generic
// values and marshalled back, so key order, whitespace and number spelling do
// not change the seed. Input that is not valid JSON is returned as is.
func normalizeCompatSeedJSONUncached(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var tmp any
	if err := json.Unmarshal(v, &tmp); err != nil {
		return string(v)
	}
	out, err := json.Marshal(tmp)
	if err != nil {
		return string(v)
	}
	return string(out)
}

// The same tool arrays, system prompts and history items are canonicalized on
// every request of a conversation (session seeds, transcript block keys), and
// the result depends on the input bytes alone, so repeats are answered from a
// bounded memo instead of a decode/encode round trip. Small inputs are cheaper
// to recompute than to look up, and oversized ones would crowd the memo.
const (
	compatSeedMemoMinItem  = 512
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
	s := c.shard(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	if out, ok := s.cur[string(v)]; ok {
		return out, true
	}
	if out, ok := s.old[string(v)]; ok {
		s.putLocked(string(v), out)
		return out, true
	}
	return "", false
}

func (c *compatSeedMemoCache) put(v []byte, out string) {
	key := string(v)
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
