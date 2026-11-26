package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
)

type openAICyberTranscriptBlockKeys struct {
	lookupKeys          []string
	preLatestUserKey    string
	lookupKeysTruncated bool
}

// Bound the Redis lookup work for a single request while retaining the most
// recent transcript prefixes, where a continuation is most likely to match.
const maxOpenAICyberTranscriptLookupKeys = 256

var (
	cyberTranscriptInstructionsPrefix = []byte("|instructions=")
	cyberTranscriptItemPrefix         = []byte("|item=")
)

// deriveOpenAICyberTranscriptBlockKeys returns cumulative semantic-history
// hashes plus the context key immediately before the latest user turn. The
// context key requires model-generated history so shared first-turn templates
// cannot block unrelated conversations.
func deriveOpenAICyberTranscriptBlockKeys(apiKeyID int64, body []byte) openAICyberTranscriptBlockKeys {
	return deriveOpenAICyberTranscriptBlockKeysCore(apiKeyID, body, false, false, "")
}

func deriveOpenAICyberTranscriptBlockKeysCore(
	apiKeyID int64,
	body []byte,
	requireUserAnchor bool,
	onlyLatestPair bool,
	keyPrefix string,
) openAICyberTranscriptBlockKeys {
	if len(body) == 0 {
		return openAICyberTranscriptBlockKeys{}
	}

	var (
		typeRaw         string
		responseRaw     string
		instructionsRaw string
		messagesRaw     string
		hasMessages     bool
		inputRaw        string
		hasInput        bool
		gjsonRoot       gjson.Result
		useGJSONRoot    bool
	)

	rootStr := jsonView(body)
	ok := jsonForEachMember(rootStr, func(key, val string) bool {
		switch key {
		case "type":
			if typeRaw == "" {
				typeRaw = val
			}
		case "response":
			if responseRaw == "" {
				responseRaw = val
			}
		case "instructions":
			if instructionsRaw == "" {
				instructionsRaw = val
			}
		case "messages":
			if !hasMessages {
				messagesRaw = val
				hasMessages = true
			}
		case "input":
			if !hasInput {
				inputRaw = val
				hasInput = true
			}
		}
		return true
	})

	if ok && len(typeRaw) > 0 && typeRaw[0] == '"' {
		eventType := jsonRawStringValue(typeRaw)
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(eventType)), "response.") &&
			len(responseRaw) > 0 && responseRaw[0] == '{' {
			instructionsRaw = ""
			messagesRaw = ""
			hasMessages = false
			inputRaw = ""
			hasInput = false
			ok = jsonForEachMember(responseRaw, func(key, val string) bool {
				switch key {
				case "instructions":
					if instructionsRaw == "" {
						instructionsRaw = val
					}
				case "messages":
					if !hasMessages {
						messagesRaw = val
						hasMessages = true
					}
				case "input":
					if !hasInput {
						inputRaw = val
						hasInput = true
					}
				}
				return true
			})
		}
	}

	if !ok {
		gjsonRoot = openAIRequestPayloadView(body)
		if !gjsonRoot.Exists() || !gjsonRoot.IsObject() {
			return openAICyberTranscriptBlockKeys{}
		}
		useGJSONRoot = true
		if instr := gjsonRoot.Get("instructions"); instr.Exists() {
			instructionsRaw = instr.Raw
		}
		if msgs := gjsonRoot.Get("messages"); msgs.Exists() {
			hasMessages = true
			messagesRaw = msgs.Raw
		} else if inp := gjsonRoot.Get("input"); inp.Exists() {
			hasInput = true
			inputRaw = inp.Raw
		}
	}

	var (
		seqRaw     string
		isMessages bool
	)
	if hasMessages {
		if len(messagesRaw) == 0 || messagesRaw[0] != '[' {
			return openAICyberTranscriptBlockKeys{}
		}
		seqRaw = messagesRaw
		isMessages = true
	} else if hasInput {
		if len(inputRaw) == 0 {
			return openAICyberTranscriptBlockKeys{}
		}
		seqRaw = inputRaw
	} else {
		return openAICyberTranscriptBlockKeys{}
	}

	if !isMessages && seqRaw[0] != '[' && seqRaw[0] != '"' {
		return openAICyberTranscriptBlockKeys{}
	}

	var memberBack [32]canonMember
	var keyBack [256]byte
	var strBack [512]byte
	scratch := canonScratch{
		members: memberBack[:0],
		keyBuf:  keyBack[:0],
		strBuf:  strBack[:0],
	}
	var canonBack [2048]byte
	canonBuf := canonBack[:0]

	h := sha256.New()
	var hdrBack [64]byte
	hdr := append(hdrBack[:0], "cyber-transcript:v3|api_key="...)
	hdr = strconv.AppendInt(hdr, apiKeyID, 10)
	_, _ = h.Write(hdr)

	if instructionsRaw != "" {
		if instructionsRaw[0] == '"' {
			instrStr := jsonRawStringValue(instructionsRaw)
			if strings.TrimSpace(instrStr) != "" {
				_, _ = h.Write(cyberTranscriptInstructionsPrefix)
				_, _ = h.Write(unsafe.Slice(unsafe.StringData(instrStr), len(instrStr)))
			}
		} else {
			canonBuf = appendCompatSeedJSON(canonBuf[:0], instructionsRaw, &scratch)
			_, _ = h.Write(cyberTranscriptInstructionsPrefix)
			_, _ = h.Write(canonBuf)
		}
	}

	if !isMessages && seqRaw[0] == '"' {
		inputStr := jsonRawStringValue(seqRaw)
		if strings.TrimSpace(inputStr) == "" {
			return openAICyberTranscriptBlockKeys{}
		}
		canonBuf = appendJSONEscapedString(canonBuf[:0], inputStr)
		_, _ = h.Write(cyberTranscriptItemPrefix)
		_, _ = h.Write(canonBuf)
		var digest [sha256.Size]byte
		h.Sum(digest[:0])
		return openAICyberTranscriptBlockKeys{
			lookupKeys: []string{formatCyberTranscriptDigest(keyPrefix, &digest)},
		}
	}

	var (
		ring                [maxOpenAICyberTranscriptLookupKeys][sha256.Size]byte
		ringLen             int
		ringWrite           int
		lookupKeysTruncated bool
		lastDigest          [sha256.Size]byte
		hasLastDigest       bool
		preLatestDigest     [sha256.Size]byte
		hasPreLatest        bool
		hasModelGenerated   bool
		anchorChecked       bool
		anchored            bool
	)

	processItem := func(itemRaw string, gjsonItem gjson.Result, hasGJSON bool) bool {
		var (
			roleRaw    string
			typeRaw    string
			contentRaw string
			hasContent bool
			textRaw    string
			hasText    bool
			parsedItem gjson.Result
			itemParsed bool
			roleStr    string
			typeStr    string
			canonSlice []byte
		)

		if len(itemRaw) > 0 && itemRaw[0] == '{' {
			memberOK := jsonForEachMember(itemRaw, func(key, val string) bool {
				switch key {
				case "role":
					if roleRaw == "" {
						roleRaw = val
					}
				case "type":
					if typeRaw == "" {
						typeRaw = val
					}
				case "content":
					if !hasContent {
						contentRaw = val
						hasContent = true
					}
				case "text":
					if !hasText {
						textRaw = val
						hasText = true
					}
				}
				return true
			})
			if !memberOK {
				if hasGJSON {
					parsedItem = gjsonItem
				} else {
					parsedItem = gjson.Parse(itemRaw)
				}
				itemParsed = true
				roleStr = parsedItem.Get("role").String()
				typeStr = parsedItem.Get("type").String()
				if c := parsedItem.Get("content"); c.Exists() {
					hasContent = true
					contentRaw = c.Raw
				}
				if t := parsedItem.Get("text"); t.Exists() {
					hasText = true
					textRaw = t.Raw
				}
			} else {
				if len(roleRaw) > 0 && roleRaw[0] == '"' {
					roleStr = jsonRawStringValue(roleRaw)
				} else if roleRaw != "" && roleRaw != "null" && roleRaw != "true" && roleRaw != "false" {
					roleStr = gjson.Parse(roleRaw).String()
				}
				if len(typeRaw) > 0 && typeRaw[0] == '"' {
					typeStr = jsonRawStringValue(typeRaw)
				} else if typeRaw != "" && typeRaw != "null" && typeRaw != "true" && typeRaw != "false" {
					typeStr = gjson.Parse(typeRaw).String()
				}
			}

			if requireUserAnchor && !anchorChecked {
				if strings.TrimSpace(roleStr) == "user" {
					anchorChecked = true
					if itemParsed {
						anchored = hasMeaningfulOpenAIContent(parsedItem.Get("content"))
					} else {
						anchored = hasMeaningfulOpenAIContentRaw(contentRaw, hasContent, &canonBuf, &scratch)
					}
					if !anchored {
						return false
					}
				} else if !isMessages && strings.TrimSpace(typeStr) == "input_text" {
					anchorChecked = true
					if hasText {
						anchored = strings.TrimSpace(jsonAnyRawStringValue(textRaw)) != ""
					}
					if !anchored {
						return false
					}
				}
			}

			canonBuf = appendCompatSeedJSON(canonBuf[:0], itemRaw, &scratch)
			canonSlice = canonBuf
		} else if len(itemRaw) > 0 && itemRaw[0] == '[' {
			canonBuf = appendCompatSeedJSON(canonBuf[:0], itemRaw, &scratch)
			canonSlice = canonBuf
		} else if len(itemRaw) > 0 && itemRaw[0] == '"' {
			s := jsonRawStringValue(itemRaw)
			canonBuf = appendJSONEscapedString(canonBuf[:0], s)
			canonSlice = canonBuf
		} else {
			canonSlice = unsafe.Slice(unsafe.StringData(itemRaw), len(itemRaw))
		}

		if len(bytes.TrimSpace(canonSlice)) == 0 {
			return true
		}

		if hasModelGenerated && hasLastDigest {
			startsUserTurn := false
			if itemParsed {
				startsUserTurn = openAICyberTranscriptItemStartsUserTurn(parsedItem)
			} else {
				startsUserTurn = openAICyberTranscriptItemStartsUserTurnRaw(roleStr, typeStr, contentRaw, hasContent)
			}
			if startsUserTurn {
				preLatestDigest = lastDigest
				hasPreLatest = true
			}
		}

		_, _ = h.Write(cyberTranscriptItemPrefix)
		_, _ = h.Write(canonSlice)
		h.Sum(lastDigest[:0])
		hasLastDigest = true

		if !onlyLatestPair {
			ring[ringWrite] = lastDigest
			if ringLen < maxOpenAICyberTranscriptLookupKeys {
				ringLen++
			} else {
				lookupKeysTruncated = true
			}
			ringWrite = (ringWrite + 1) % maxOpenAICyberTranscriptLookupKeys
		}

		if !hasModelGenerated {
			hasModelGenerated = openAICyberTranscriptItemIsModelGeneratedFields(roleStr, typeStr)
		}
		return true
	}

	var seqResult gjson.Result
	if useGJSONRoot {
		if isMessages {
			seqResult = gjsonRoot.Get("messages")
		} else {
			seqResult = gjsonRoot.Get("input")
		}
	}

	walkOK := false
	if !useGJSONRoot {
		walkOK = jsonForEachElement(seqRaw, func(elem string) bool {
			return processItem(elem, gjson.Result{}, false)
		})
	}
	if !walkOK {
		if requireUserAnchor {
			// Reset state on rare malformed/fallback pass to stay 100% identical to gjson.
			ringLen = 0
			ringWrite = 0
			lookupKeysTruncated = false
			hasLastDigest = false
			hasPreLatest = false
			hasModelGenerated = false
			anchorChecked = false
			anchored = false
			h.Reset()
			_, _ = h.Write(hdr)
			if instructionsRaw != "" {
				if instructionsRaw[0] == '"' {
					instrStr := jsonRawStringValue(instructionsRaw)
					if strings.TrimSpace(instrStr) != "" {
						_, _ = h.Write(cyberTranscriptInstructionsPrefix)
						_, _ = h.Write(unsafe.Slice(unsafe.StringData(instrStr), len(instrStr)))
					}
				} else {
					canonBuf = appendCompatSeedJSON(canonBuf[:0], instructionsRaw, &scratch)
					_, _ = h.Write(cyberTranscriptInstructionsPrefix)
					_, _ = h.Write(canonBuf)
				}
			}
		}
		if !useGJSONRoot {
			seqResult = gjson.Parse(seqRaw)
		}
		if !seqResult.Exists() || !seqResult.IsArray() {
			return openAICyberTranscriptBlockKeys{}
		}
		seqResult.ForEach(func(_, item gjson.Result) bool {
			return processItem(item.Raw, item, true)
		})
	}

	if requireUserAnchor && !anchored {
		return openAICyberTranscriptBlockKeys{}
	}
	if !hasLastDigest {
		return openAICyberTranscriptBlockKeys{}
	}

	var out openAICyberTranscriptBlockKeys
	out.lookupKeysTruncated = lookupKeysTruncated
	if hasPreLatest {
		out.preLatestUserKey = formatCyberTranscriptDigest(keyPrefix, &preLatestDigest)
	}
	if onlyLatestPair {
		out.lookupKeys = []string{formatCyberTranscriptDigest(keyPrefix, &lastDigest)}
		return out
	}

	out.lookupKeys = make([]string, 0, ringLen)
	if lookupKeysTruncated {
		for i := ringWrite; i < maxOpenAICyberTranscriptLookupKeys; i++ {
			out.lookupKeys = append(out.lookupKeys, formatCyberTranscriptDigest(keyPrefix, &ring[i]))
		}
		for i := 0; i < ringWrite; i++ {
			out.lookupKeys = append(out.lookupKeys, formatCyberTranscriptDigest(keyPrefix, &ring[i]))
		}
	} else {
		for i := 0; i < ringLen; i++ {
			out.lookupKeys = append(out.lookupKeys, formatCyberTranscriptDigest(keyPrefix, &ring[i]))
		}
	}
	return out
}

func formatCyberTranscriptDigest(prefix string, digest *[sha256.Size]byte) string {
	const hexLen = sha256.Size * 2
	if len(prefix)+hexLen <= 128 {
		var buf [128]byte
		copy(buf[:], prefix)
		hex.Encode(buf[len(prefix):], digest[:])
		return string(buf[:len(prefix)+hexLen])
	}
	buf := make([]byte, len(prefix)+hexLen)
	copy(buf, prefix)
	hex.Encode(buf[len(prefix):], digest[:])
	return string(buf)
}

func jsonRawStringValue(raw string) string {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		inner := raw[1 : len(raw)-1]
		if strings.IndexByte(inner, '\\') < 0 {
			return inner
		}
	}
	return gjson.Parse(raw).String()
}

func jsonAnyRawStringValue(raw string) string {
	if raw == "" || raw == "null" {
		return ""
	}
	if raw[0] == '"' {
		return jsonRawStringValue(raw)
	}
	if raw == "true" || raw == "false" {
		return raw
	}
	if raw[0] != '{' && raw[0] != '[' {
		return raw
	}
	return gjson.Parse(raw).String()
}

func openAICyberTranscriptItemStartsUserTurnRaw(roleStr, typeStr, contentRaw string, hasContent bool) bool {
	if strings.EqualFold(strings.TrimSpace(roleStr), "user") {
		if !hasContent {
			return false
		}
		if len(contentRaw) > 0 && contentRaw[0] == '[' {
			hasUserContent := false
			ok := jsonForEachElement(contentRaw, func(blockRaw string) bool {
				var blockTypeRaw string
				if len(blockRaw) > 0 && blockRaw[0] == '{' {
					if !jsonForEachMember(blockRaw, func(k, v string) bool {
						if k == "type" && blockTypeRaw == "" {
							blockTypeRaw = v
							return false
						}
						return true
					}) {
						blockTypeRaw = gjson.Parse(blockRaw).Get("type").Raw
					}
				}
				var bt string
				if blockTypeRaw != "" {
					bt = jsonAnyRawStringValue(blockTypeRaw)
				}
				switch strings.ToLower(strings.TrimSpace(bt)) {
				case "tool_result", "function_call_output", "custom_tool_call_output", "computer_call_output":
				default:
					hasUserContent = true
				}
				return !hasUserContent
			})
			if ok {
				return hasUserContent
			}
			return openAICyberTranscriptContentArrayHasUserContent(gjson.Parse(contentRaw))
		}
		return true
	}
	return strings.EqualFold(strings.TrimSpace(typeStr), "input_text")
}

func openAICyberTranscriptContentArrayHasUserContent(content gjson.Result) bool {
	hasUserContent := false
	content.ForEach(func(_, block gjson.Result) bool {
		switch strings.ToLower(strings.TrimSpace(block.Get("type").String())) {
		case "tool_result", "function_call_output", "custom_tool_call_output", "computer_call_output":
		default:
			hasUserContent = true
		}
		return !hasUserContent
	})
	return hasUserContent
}

func openAICyberTranscriptItemStartsUserTurn(item gjson.Result) bool {
	if strings.EqualFold(strings.TrimSpace(item.Get("role").String()), "user") {
		content := item.Get("content")
		if content.IsArray() {
			return openAICyberTranscriptContentArrayHasUserContent(content)
		}
		return content.Exists()
	}
	return strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "input_text")
}

func openAICyberTranscriptItemIsModelGeneratedFields(roleStr, typeStr string) bool {
	switch strings.ToLower(strings.TrimSpace(roleStr)) {
	case "assistant", "model":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(typeStr)) {
	case "output_text", "function_call", "tool_call", "custom_tool_call", "computer_call":
		return true
	default:
		return false
	}
}

func openAICyberTranscriptItemIsModelGenerated(item gjson.Result) bool {
	return openAICyberTranscriptItemIsModelGeneratedFields(item.Get("role").String(), item.Get("type").String())
}
