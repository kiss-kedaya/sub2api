package service

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Top-level index of a request body: scan once, look up many times.
//
// A request used to ask for a dozen top-level fields (stream, service_tier,
// reasoning.effort, max_output_tokens, tools, input, ...) through separate
// gjson.GetBytes(body, "field") calls, and every call rescans the body from the
// start, squashing each large value (input / messages / tools) it has to skip.
// On a 300 KB Codex body that is ~0.2 ms per call, several dozen calls per
// request (gjson.parseSquash was ~20% of the gateway CPU).
//
// BodyTop answers the same questions from one pass over the top-level members.
// The pass is lazy and resumable: a lookup scans only as far as the first member
// that answers it (exactly as far as gjson would), later lookups reuse what was
// scanned and continue from there, so the index never reads more than gjson would
// for a single question and never reads the same byte twice.
//
// The answer is what gjson.GetBytes(body, path) returns (Type, Raw, Str, Num,
// Index) for the paths it handles: a plain key, or two plain keys joined by '.'.
// Everything it is not certain about goes to gjson itself:
//
//   - the top level is not strictly well-formed before the point reached (BOM or
//     garbage before the object, trailing comma, missing colon, bad literal or
//     number, unterminated string or container, trailing garbage once the end is
//     reached, more than bodyTopMaxMembers members);
//   - a top-level key spells an escape (gjson matches keys after unescaping);
//   - a nested object that a two-key path has to enter is large or not valid JSON,
//     or more than bodyTopMaxNestedEntries of them would have to be entered;
//   - a member a two-key path would have to look into is an array (gjson walks it
//     with its own bracket matching, which on malformed text can run past the
//     extent the squash rule gives it; a well-formed array never answers a named
//     key, but this one is not known to be well-formed);
//   - the path is anything but plain keys (wildcards, queries, modifiers, array
//     indexes ...).
//
// gjson semantics the index reproduces (checked against gjson v1.18.0 and by the
// differential tests): the first member whose key matches wins, a two-key path
// keeps trying later duplicates of the first key until one yields a value, an
// array can never answer a non-numeric key, gjson never looks past the member it
// returns, and values are delimited by gjson's own squash rule (string quote
// parity; depth over {[( and }]) outside strings).
//
// The index is cached per request on the gin context and keyed by the identity
// (data pointer and length) of the body slice. That relies on the invariant the
// gateway already needs for failover retries:
//
//	A body slice handed to RequestBodyTop must never be modified in place
//	afterwards (no copy into it, no append(buf[:0], ...) over it, no sjson
//	ReplaceInPlace, no pooled buffer refilled for the next body); every rewrite
//	returns a new slice. A buffer that is reused must not be passed in.
//
// body_top_contract_test.go greps the gateway sources for those idioms and fails
// on an unannotated one.
//
// WebSocket contexts are left out: their context lives as long as the connection,
// the memo would pin two request bodies for all of it, and frames are small.
//
// Composite values (objects, arrays) are returned as zero-copy views of the body,
// unlike gjson.GetBytes which copies them: read them, do not keep them past the
// life of the request. Scalars are copied, so a retained service_tier or
// reasoning effort does not pin the body in memory.

const (
	// bodyTopMaxMembers bounds the member table; real requests have a few dozen
	// top-level members, a body with thousands is cheaper to hand to gjson.
	bodyTopMaxMembers = 1024
	// bodyTopMaxNestedBytes bounds the nested object a two-key path validates
	// before running gjson on it (reasoning, generationConfig ... are tiny).
	bodyTopMaxNestedBytes = 4 << 10
	// bodyTopMaxNestedEntries bounds how many same-named members one two-key
	// lookup may enter (validate + search). A request has one reasoning object; a
	// body with many is cheaper to hand to gjson, and this keeps the work of a
	// lookup within a small constant of gjson's own on any input.
	bodyTopMaxNestedEntries = 8
	// bodyTopDefaultMinBytes: below this a request body is cheap to rescan and
	// the original gjson path is kept.
	bodyTopDefaultMinBytes = 16 << 10

	bodyTopMemoKey = "__sub2api_body_top_memo"
)

// bodyTopMinBytes is the smallest body that gets an index. Tests lower it.
var bodyTopMinBytes atomic.Int64

func init() { bodyTopMinBytes.Store(bodyTopDefaultMinBytes) }

type topMember struct{ keyStart, keyEnd, valStart, valEnd int32 }

// topScan is where the resumable pass stands.
type topScan struct {
	i       int  // just past the last member scanned (or past '{')
	started bool // the opening brace has been consumed
	first   bool // no member scanned yet
	done    bool // reached the closing brace of a well-formed object
	failed  bool // not strictly well-formed at the point reached
}

type bodyTopIndex struct {
	body []byte

	mu      sync.Mutex // guards scan and members
	scan    topScan
	members []topMember

	validOnce sync.Once
	valid     bool

	inputOnce sync.Once
	inputIdx  *responsesInputIndex
}

// bodyTopMemo holds the two most recent bodies of one request (the incoming body
// and the one after the latest rewrite are usually both still being asked about).
type bodyTopMemo struct {
	slots [2]atomic.Pointer[bodyTopIndex]
	next  atomic.Uint32
}

// BodyTop answers top-level questions about a request body. The zero index (small
// body, no gin context) simply forwards to gjson.
type BodyTop struct {
	body []byte
	idx  *bodyTopIndex
}

// RequestBodyTop returns the lookup handle for body, creating the index at most
// once per distinct body per request. A nil c, a context without a request, a
// WebSocket context, or a body below the size threshold yields a handle that
// forwards to gjson.
//
// The caller promises not to modify body in place afterwards (see the file
// comment): the index is keyed by the slice's identity, not its content.
func RequestBodyTop(c *gin.Context, body []byte) BodyTop {
	if c == nil || int64(len(body)) < bodyTopMinBytes.Load() {
		return BodyTop{body: body}
	}
	var memo *bodyTopMemo
	if v, ok := c.Get(bodyTopMemoKey); ok {
		memo, _ = v.(*bodyTopMemo)
	}
	if memo == nil {
		// IsWebsocket reads the request headers, hence the nil check first. It is
		// asked once per request: later calls find the memo.
		if c.Request == nil || c.IsWebsocket() {
			return BodyTop{body: body}
		}
		memo = &bodyTopMemo{}
		c.Set(bodyTopMemoKey, memo)
	}
	for i := range memo.slots {
		if x := memo.slots[i].Load(); x != nil && x.holds(body) {
			return BodyTop{body: body, idx: x}
		}
	}
	x := &bodyTopIndex{body: body}
	memo.slots[memo.next.Add(1)&1].Store(x)
	return BodyTop{body: body, idx: x}
}

// Get is gjson.GetBytes(body, path).
func (t BodyTop) Get(path string) gjson.Result {
	if t.idx != nil {
		if r, ok := t.idx.get(path); ok {
			return r
		}
	}
	return gjson.GetBytes(t.body, path)
}

// Valid is gjson.ValidBytes(body), computed once per indexed body.
func (t BodyTop) Valid() bool {
	if t.idx != nil {
		return t.idx.isValid()
	}
	return validJSON(t.body) || gjson.ValidBytes(t.body)
}

func (t BodyTop) responsesInput() *responsesInputIndex {
	if t.idx == nil {
		return nil
	}
	return t.idx.responsesInput()
}

func (x *bodyTopIndex) holds(body []byte) bool {
	return len(x.body) == len(body) && unsafe.SliceData(x.body) == unsafe.SliceData(body)
}

func (x *bodyTopIndex) isValid() bool {
	x.validOnce.Do(func() { x.valid = validJSON(x.body) || gjson.ValidBytes(x.body) })
	return x.valid
}

// get answers path like gjson.Get. handled=false means "ask gjson".
func (x *bodyTopIndex) get(path string) (res gjson.Result, handled bool) {
	first, rest, nested := strings.Cut(path, ".")
	if !plainTopKey(first) || (nested && !plainTopKey(rest)) {
		return gjson.Result{}, false
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	entered := 0
	for k := 0; ; k++ {
		if k == len(x.members) && !x.advance() {
			// The object ended without an answer: gjson finds nothing either. If the
			// text stopped being well-formed first, only gjson can say what it does.
			return gjson.Result{}, !x.scan.failed
		}
		m := &x.members[k]
		if string(x.body[m.keyStart:m.keyEnd]) != first {
			continue
		}
		if !nested {
			return x.valueResult(m), true
		}
		// Only an object can hold a named member, and scalars have no members: a
		// first-key member that does not contain the key is skipped, as gjson
		// continues with the next one. An array is left to gjson, see the file comment.
		switch x.body[m.valStart] {
		case '{':
		case '[':
			return gjson.Result{}, false
		default:
			continue
		}
		if entered++; entered > bodyTopMaxNestedEntries {
			return gjson.Result{}, false
		}
		sub := x.body[m.valStart:m.valEnd]
		if len(sub) > bodyTopMaxNestedBytes || !validJSON(sub) {
			return gjson.Result{}, false
		}
		if r := gjson.Get(jsonView(sub), rest); r.Exists() {
			r = detachResult(r)
			r.Index += int(m.valStart)
			return r, true
		}
	}
}

// advance scans one more top-level member into x.members. It reports false when
// the object has ended (scan.done) or stopped being strictly well-formed
// (scan.failed). The caller holds x.mu.
func (x *bodyTopIndex) advance() bool {
	sc := &x.scan
	if sc.done || sc.failed {
		return false
	}
	b := x.body
	s := jsonView(b)
	n := len(s)
	i := sc.i
	if !sc.started {
		sc.started = true
		if n > 1<<31-2 {
			return sc.fail()
		}
		if i = skipJSONWhitespace(b, 0); i >= n || s[i] != '{' {
			return sc.fail()
		}
		i++
		sc.first = true
		x.members = make([]topMember, 0, 16)
	}
	if i = skipJSONWhitespace(b, i); i >= n {
		return sc.fail()
	}
	switch {
	case s[i] == '}':
		// End of the object (an empty one when no member came first). Bytes other
		// than whitespace after it make the body not strictly well-formed.
		sc.done = skipJSONWhitespace(b, i+1) == n
		sc.failed = !sc.done
		return false
	case sc.first: // the first key follows directly
	case s[i] == ',':
		if i = skipJSONWhitespace(b, i+1); i >= n {
			return sc.fail()
		}
	default:
		return sc.fail()
	}
	sc.first = false
	if s[i] != '"' || len(x.members) >= bodyTopMaxMembers {
		return sc.fail()
	}
	keyEnd, escaped, ok := jsonSkipString(s, i)
	if !ok || escaped {
		return sc.fail()
	}
	m := topMember{keyStart: int32(i + 1), keyEnd: int32(keyEnd - 1)}
	if i = skipJSONWhitespace(b, keyEnd); i >= n || s[i] != ':' {
		return sc.fail()
	}
	if i = skipJSONWhitespace(b, i+1); i >= n {
		return sc.fail()
	}
	valEnd, ok := topSkipValue(b, i)
	if !ok {
		return sc.fail()
	}
	m.valStart, m.valEnd = int32(i), int32(valEnd)
	x.members = append(x.members, m)
	sc.i = valEnd
	return true
}

func (sc *topScan) fail() bool {
	sc.failed = true
	return false
}

// valueResult builds the Result gjson.GetBytes returns for the member's value.
func (x *bodyTopIndex) valueResult(m *topMember) gjson.Result {
	raw := jsonView(x.body[m.valStart:m.valEnd])
	idx := int(m.valStart)
	switch raw[0] {
	case '{', '[':
		return gjson.Result{Type: gjson.JSON, Raw: raw, Index: idx}
	case 't':
		return gjson.Result{Type: gjson.True, Raw: "true", Index: idx}
	case 'f':
		return gjson.Result{Type: gjson.False, Raw: "false", Index: idx}
	case 'n':
		return gjson.Result{Type: gjson.Null, Raw: "null", Index: idx}
	case '"':
		// Parse shares gjson's unescape for Str. Its Raw can lose the closing quote
		// of an escaped string that ends the text it is given, so Raw is set here.
		r := gjson.Parse(raw)
		r.Raw, r.Index = raw, idx
		return detachResult(r)
	}
	num, _ := strconv.ParseFloat(raw, 64)
	return gjson.Result{Type: gjson.Number, Raw: strings.Clone(raw), Num: num, Index: idx}
}

// detachResult gives a scalar result its own memory (gjson.GetBytes does the
// same), leaving composites as views.
func detachResult(r gjson.Result) gjson.Result {
	switch r.Type {
	case gjson.True:
		r.Raw = "true"
	case gjson.False:
		r.Raw = "false"
	case gjson.Null:
		r.Raw = "null"
	case gjson.Number:
		r.Raw = strings.Clone(r.Raw)
	case gjson.String:
		raw := strings.Clone(r.Raw)
		if strings.IndexByte(raw, '\\') < 0 {
			r.Str = raw[1 : len(raw)-1] // unescaped: Str was a window into Raw
		} // escaped: Str is gjson's freshly built unescape
		r.Raw = raw
	}
	return r
}

// plainTopKey reports whether s is a key gjson's path syntax passes through
// untouched: letters, digits, '_' and '-', not all digits (a digit-only part is
// an array index to gjson).
func plainTopKey(s string) bool {
	digits := s != ""
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-':
			digits = false
		default:
			return false
		}
	}
	return s != "" && !digits
}

// topSquashClass drives gjson.parseSquash: 1 closes, 3 opens, 2 starts a string.
// '(' and ')' count like brackets there, so they do here.
var topSquashClass = [256]uint8{'"': 2, '{': 3, '[': 3, '(': 3, '}': 1, ']': 1, ')': 1}

// topSkipValue returns the end of the value starting at b[i], strictly: strings
// and containers are delimited the way gjson delimits them, scalars must be exact
// JSON literals or numbers followed by a delimiter.
func topSkipValue(b []byte, i int) (end int, ok bool) {
	s := jsonView(b)
	n := len(s)
	switch c := s[i]; {
	case c == '"':
		end, _, ok = jsonSkipString(s, i)
		return end, ok
	case c == '{' || c == '[':
		for depth := 0; i < n; {
			switch topSquashClass[s[i]] {
			case 3:
				depth++
				i++
			case 1:
				depth--
				i++
				if depth == 0 {
					return i, true
				}
			case 2:
				if i, _, ok = jsonSkipString(s, i); !ok {
					return n, false
				}
			default:
				i++
			}
		}
		return n, false
	case c == 't':
		ok, end = validJSONLiteral(b, i, "true"), i+4
	case c == 'f':
		ok, end = validJSONLiteral(b, i, "false"), i+5
	case c == 'n':
		ok, end = validJSONLiteral(b, i, "null"), i+4
	case c == '-' || (c >= '0' && c <= '9'):
		end, ok = validJSONNumber(b, i)
	}
	if !ok || end >= n {
		return end, false
	}
	switch s[end] {
	case ' ', '\t', '\n', '\r', ',', '}':
		return end, true
	}
	return end, false
}
