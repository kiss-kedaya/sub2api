package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

// Which request fields can sanitizeAnthropicBodyForBetaTokens / sanitizeAnthropicFallbackFields
// have to strip? Almost no request carries any of them, yet the sanitizers used to ask gjson for
// each field one by one, and every lookup of an absent field walks the whole body (squashing the
// large messages array on the way, and copying it for the messages lookup). Two cheap one-sided
// answers replace that:
//
//   - the anthropic-beta header says which fields it does NOT authorize (pure string work); the
//     others never need a lookup;
//   - one zero-allocation walk over the top-level members says which of those fields could be in
//     the body at all.
//
// "Could" is the contract: a cleared bit is a proof that the gjson lookup the sanitizer used to run
// would find nothing, a set bit only means "run that lookup" (the original code, unchanged, stays the
// single source of truth for what gets stripped). Everything the walk does not interpret itself
// (a top-level key spelled with an escape, text that is not an object, a duplicated thinking member,
// a thinking value that is an array, an unwalkable messages array ...) answers "could". See
// json_shallow_scan.go for the walkers' conservative rules. The differential test in
// anthropic_beta_field_probe_test.go compares the answer with the gjson lookups on random documents.

// betaFieldMask is a set of the body fields that are guarded by an anthropic-beta token.
type betaFieldMask uint8

const (
	// betaFieldContextManagement: top-level context_management.
	betaFieldContextManagement betaFieldMask = 1 << iota
	// betaFieldThinkingBlockBinding: thinking.block_binding.
	betaFieldThinkingBlockBinding
	// betaFieldFallbacks: top-level fallbacks.
	betaFieldFallbacks
	// betaFieldFallbackCreditToken: top-level fallback_credit_token.
	betaFieldFallbackCreditToken
	// betaFieldMessageOutputConfig: output_config on a message of the messages array.
	betaFieldMessageOutputConfig

	betaFieldsAll = betaFieldContextManagement | betaFieldThinkingBlockBinding | betaFieldFallbacks |
		betaFieldFallbackCreditToken | betaFieldMessageOutputConfig
)

// anthropicBetaFieldsToStrip returns the guarded fields the anthropic-beta header does not authorize,
// i.e. the ones the sanitizers delete when the body carries them. It is the header half of
// stripAnthropicBodyFieldUnlessBeta / stripAnthropicMessageOutputConfigUnlessBeta (a token is
// present when one comma separated, trimmed part equals it).
func anthropicBetaFieldsToStrip(header string) betaFieldMask {
	var contextManagement, compact, thinkingBinding, serverSideFallback, fallbackCredit, fallbackCreditLegacy, midConversation bool
	for rest := header; rest != ""; {
		var part string
		part, rest, _ = strings.Cut(rest, ",")
		switch strings.TrimSpace(part) {
		case anthropicBetaContextManagementToken:
			contextManagement = true
		case anthropicBetaCompactToken:
			compact = true
		case claude.BetaThinkingBindingControls:
			thinkingBinding = true
		case claude.BetaServerSideFallback:
			serverSideFallback = true
		case claude.BetaFallbackCredit:
			fallbackCredit = true
		case claude.BetaFallbackCreditLegacy:
			fallbackCreditLegacy = true
		case claude.BetaMidConversationOutputConfig:
			midConversation = true
		}
	}
	var need betaFieldMask
	if !contextManagement && !compact {
		need |= betaFieldContextManagement
	}
	if !thinkingBinding {
		need |= betaFieldThinkingBlockBinding
	}
	if !serverSideFallback {
		need |= betaFieldFallbacks
	}
	if !serverSideFallback && !fallbackCredit && !fallbackCreditLegacy {
		need |= betaFieldFallbackCreditToken
	}
	if !midConversation {
		need |= betaFieldMessageOutputConfig
	}
	return need
}

// anthropicBetaFieldsMaybeIn reports, restricted to want, which guarded fields body could carry. A
// field whose bit is clear is provably absent: the gjson lookup for it finds nothing.
func anthropicBetaFieldsMaybeIn(body []byte, want betaFieldMask) betaFieldMask {
	if want == 0 {
		return 0
	}
	var maybe betaFieldMask
	thinkings := 0
	messages, haveMessages := "", false
	complete := jsonForEachMember(jsonView(body), func(key, val string) bool {
		switch key {
		case "context_management":
			maybe |= betaFieldContextManagement
		case "fallbacks":
			maybe |= betaFieldFallbacks
		case "fallback_credit_token":
			maybe |= betaFieldFallbackCreditToken
		case "thinking":
			// gjson keeps trying later duplicates of the first path part, so only a lone member is
			// judged here.
			if thinkings++; thinkings > 1 || thinkingMayHaveBlockBinding(val) {
				maybe |= betaFieldThinkingBlockBinding
			}
		case "messages":
			// gjson takes the first member with that key, whatever it holds.
			if !haveMessages {
				messages, haveMessages = val, true
			}
		}
		return true
	})
	if !complete {
		return want
	}
	if haveMessages && want&betaFieldMessageOutputConfig != 0 && messagesMayHaveOutputConfig(messages) {
		maybe |= betaFieldMessageOutputConfig
	}
	return maybe & want
}

// thinkingMayHaveBlockBinding: could gjson find block_binding inside this thinking value?
func thinkingMayHaveBlockBinding(val string) bool {
	if val == "" {
		return true
	}
	switch val[0] {
	case '{':
		found := false
		complete := jsonForEachMember(val, func(key, _ string) bool {
			found = found || key == "block_binding"
			return !found
		})
		return found || !complete
	case '"', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// gjson cannot step into a string, a literal or a number.
		return false
	default:
		// an array, or text this walk does not know: leave it to gjson
		return true
	}
}

// messagesMayHaveOutputConfig: could one of the elements of this messages value have an
// output_config member? Only an array is looked at (the sanitizer returns the body untouched for any
// other first messages member), and only objects can hold a member.
func messagesMayHaveOutputConfig(messages string) bool {
	if messages == "" || messages[0] != '[' {
		return false
	}
	may := false
	complete := jsonForEachElement(messages, func(elem string) bool {
		if elem == "" || elem[0] != '{' {
			return true
		}
		found := false
		walked := jsonForEachMember(elem, func(key, _ string) bool {
			found = found || key == "output_config"
			return !found
		})
		may = found || !walked
		return !may
	})
	return may || !complete
}
