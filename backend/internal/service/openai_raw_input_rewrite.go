package service

import (
	"fmt"
	"unsafe"

	"github.com/tidwall/gjson"
)

type openAIRawInputItemRewrite struct {
	raw  string
	keep bool
}

type openAIRawInputItemTransformer func(index int, item gjson.Result) (replacement string, keep, changed bool, err error)

// rewriteOpenAIResponsesInput rewrites only the top-level input array. Raw
// items that do not change continue to reference body; one exact-size output
// allocation is made only when at least one item changes.
func rewriteOpenAIResponsesInput(
	body []byte,
	transform openAIRawInputItemTransformer,
	appendItems ...string,
) ([]byte, bool, error) {
	inputStart, inputEnd, isArray, ok := locateJSONInputArraySpan(nil, body)
	var input gjson.Result
	if ok {
		if !isArray {
			return body, false, nil
		}
		input = gjson.Parse(jsonView(body[inputStart:inputEnd]))
	} else {
		input = parseRawJSONView(body).Get("input")
		if !input.IsArray() {
			return body, false, nil
		}
		inputStart = input.Index
		if inputStart < 0 || inputStart+len(input.Raw) > len(body) ||
			unsafe.StringData(input.Raw) != unsafe.SliceData(body[inputStart:]) {
			// parseRawJSONView parses body directly, so a child result must retain
			// its exact source offset. Falling back to bytes.Index is unsafe when an
			// identical array also appears under a nested key and adds another full
			// scan of a potentially multi-megabyte request.
			return body, false, fmt.Errorf("locate top-level OpenAI Responses input array")
		}
		inputEnd = inputStart + len(input.Raw)
	}

	items := make([]openAIRawInputItemRewrite, 0, 16)
	changed := len(appendItems) > 0
	itemBytes := 0
	index := 0
	var transformErr error
	input.ForEach(func(_, item gjson.Result) bool {
		raw := item.Raw
		keep := true
		itemChanged := false
		if transform != nil {
			var replacement string
			replacement, keep, itemChanged, transformErr = transform(index, item)
			if transformErr != nil {
				return false
			}
			if itemChanged && keep {
				raw = replacement
			}
		}
		items = append(items, openAIRawInputItemRewrite{raw: raw, keep: keep})
		if keep {
			itemBytes += len(raw)
		}
		changed = changed || itemChanged || !keep
		index++
		return true
	})
	if transformErr != nil {
		return body, false, transformErr
	}
	if !changed {
		return body, false, nil
	}

	keptCount := len(appendItems)
	for _, item := range items {
		if item.keep {
			keptCount++
		}
	}
	for _, raw := range appendItems {
		itemBytes += len(raw)
	}
	arrayLen := 2 + itemBytes
	if keptCount > 1 {
		arrayLen += keptCount - 1
	}

	outputLen := len(body) - (inputEnd - inputStart) + arrayLen
	out := make([]byte, 0, outputLen)
	out = append(out, body[:inputStart]...)
	out = append(out, '[')
	written := 0
	for _, item := range items {
		if !item.keep {
			continue
		}
		if written > 0 {
			out = append(out, ',')
		}
		out = append(out, item.raw...)
		written++
	}
	for _, raw := range appendItems {
		if written > 0 {
			out = append(out, ',')
		}
		out = append(out, raw...)
		written++
	}
	out = append(out, ']')
	out = append(out, body[inputEnd:]...)
	return out, true, nil
}
