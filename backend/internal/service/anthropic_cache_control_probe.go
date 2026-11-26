package service

import "strings"

// A zero-allocation answer to requestNeedsExtendedCacheTTLBeta.
//
// The gjson version walks the body several times (one lookup each for the top-level cache_control,
// system, tools and messages, the last of which copies the whole messages array) and allocates a
// Result per element it visits. This walks the top-level members once and then only the places a
// cache_control can sit: the top-level member, the elements of system and tools, and the content blocks
// of every message. Same questions, same first-member-wins semantics as the gjson lookups it replaces
// (see forEachCacheControl); whatever it does not interpret itself (a key spelled with an escape, a type
// or ttl value that is a string with an escape or a non-ASCII byte) reports known=false and the caller
// runs the gjson version. anthropic_cache_control_probe_test.go compares both on random bodies.

// thinkingDisplayUpdatesFast answers requestNeedsThinkingDisplayUpdatesBeta, i.e. is thinking.display
// the string "updates" (any case, surrounding space ignored), from one walk over the top-level members
// and the thinking object instead of a gjson lookup that squashes every member in front of thinking.
// known=false means "ask gjson": a key with an escape, a thinking member that is repeated (gjson keeps
// trying later duplicates of the first path part) or is an array, a display string with an escape or a
// non-ASCII byte.
func thinkingDisplayUpdatesFast(body []byte) (needed, known bool) {
	thinkings := 0
	thinking := ""
	if !jsonForEachMember(jsonView(body), func(key, val string) bool {
		if key == "thinking" {
			thinkings++
			thinking = val
		}
		return thinkings < 2
	}) {
		return false, false
	}
	switch {
	case thinkings == 0:
		return false, true
	case thinkings > 1:
		return false, false
	}
	switch thinking[0] {
	case '{':
	case '"', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return false, true // gjson cannot step into a string, a literal or a number
	default:
		return false, false
	}
	display, haveDisplay := "", false
	if !jsonForEachMember(thinking, func(key, val string) bool {
		if key == "display" {
			haveDisplay, display = true, val
			return false
		}
		return true
	}) {
		return false, false
	}
	if !haveDisplay || display[0] != '"' {
		return false, true // missing, or not a string
	}
	text, plain := jsonPlainString(display)
	if !plain {
		return false, false
	}
	return strings.EqualFold(strings.TrimSpace(text), "updates"), true
}

// extendedCacheTTLNeededFast reports whether some cache_control object of body has type "ephemeral"
// and ttl "1h". known=false means "ask gjson".
func extendedCacheTTLNeededFast(body []byte) (needed, known bool) {
	var topCC, system, tools, messages string
	var haveCC, haveSystem, haveTools, haveMessages bool
	complete := jsonForEachMember(jsonView(body), func(key, val string) bool {
		switch key {
		case "cache_control":
			if !haveCC {
				haveCC, topCC = true, val
			}
		case "system":
			if !haveSystem {
				haveSystem, system = true, val
			}
		case "tools":
			if !haveTools {
				haveTools, tools = true, val
			}
		case "messages":
			if !haveMessages {
				haveMessages, messages = true, val
			}
		}
		return true
	})
	if !complete {
		return false, false
	}
	if haveCC {
		hit, hitKnown := cacheControlIs1hEphemeral(topCC)
		if !hitKnown {
			return false, false
		}
		if hit {
			return true, true
		}
	}
	for _, blocks := range [2]string{system, tools} {
		hit, hitKnown := anyBlockCacheControlIs1h(blocks)
		if !hitKnown {
			return false, false
		}
		if hit {
			return true, true
		}
	}
	return messagesHave1hCacheControl(messages)
}

// messagesHave1hCacheControl: does a content block of some message carry a 1h ephemeral cache_control?
// Only an array is walked, and only the content member of an object message (the first one, as with
// gjson's Get).
func messagesHave1hCacheControl(messages string) (hit, known bool) {
	if messages == "" || messages[0] != '[' {
		return false, true
	}
	known = true
	walked := jsonForEachElement(messages, func(msg string) bool {
		if msg == "" || msg[0] != '{' {
			return true
		}
		content, haveContent := "", false
		if !jsonForEachMember(msg, func(key, val string) bool {
			if key == "content" {
				haveContent, content = true, val
				return false
			}
			return true
		}) {
			known = false
			return false
		}
		if !haveContent {
			return true
		}
		blockHit, blockKnown := anyBlockCacheControlIs1h(content)
		if !blockKnown {
			known = false
			return false
		}
		hit = blockHit
		return !hit
	})
	if !walked || !known {
		return false, false
	}
	return hit, true
}

// anyBlockCacheControlIs1h: is raw an array one of whose object elements carries a cache_control object
// that is 1h ephemeral? Anything that is not an array has no blocks (gjson's IsArray gate).
func anyBlockCacheControlIs1h(raw string) (hit, known bool) {
	if raw == "" || raw[0] != '[' {
		return false, true
	}
	known = true
	walked := jsonForEachElement(raw, func(block string) bool {
		if block == "" || block[0] != '{' {
			return true
		}
		cc, haveCC := "", false
		if !jsonForEachMember(block, func(key, val string) bool {
			if key == "cache_control" {
				haveCC, cc = true, val
				return false
			}
			return true
		}) {
			known = false
			return false
		}
		if !haveCC {
			return true
		}
		blockHit, blockKnown := cacheControlIs1hEphemeral(cc)
		if !blockKnown {
			known = false
			return false
		}
		hit = blockHit
		return !hit
	})
	if !walked || !known {
		return false, false
	}
	return hit, true
}

// cacheControlIs1hEphemeral: for a cache_control value, is it an object with type "ephemeral" and
// ttl "1h" (the first member of each name decides, as with gjson's Get)? A value that is not an object
// is not looked at.
func cacheControlIs1hEphemeral(raw string) (hit, known bool) {
	if raw == "" || raw[0] != '{' {
		return false, true
	}
	var typeVal, ttlVal string
	var haveType, haveTTL bool
	if !jsonForEachMember(raw, func(key, val string) bool {
		switch key {
		case "type":
			if !haveType {
				haveType, typeVal = true, val
			}
		case "ttl":
			if !haveTTL {
				haveTTL, ttlVal = true, val
			}
		}
		return !haveType || !haveTTL
	}) {
		return false, false
	}
	if !haveType || !haveTTL {
		return false, true
	}
	// jsonPlainString reads a non-string as "" (which equals neither wanted value, just as the raw
	// text of a number or literal does not) and refuses strings it would have to decode.
	typeText, typeOK := jsonPlainString(typeVal)
	ttlText, ttlOK := jsonPlainString(ttlVal)
	if !typeOK || !ttlOK {
		return false, false
	}
	return typeText == "ephemeral" && ttlText == cacheTTLTarget1h, true
}
