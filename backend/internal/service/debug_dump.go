package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Upstream debug dumps are written when a request to a provider fails, so the
// exact bytes the gateway sent can be compared with a working client request.
//
// Enabled only by setting SUB2API_DEBUG_DUMP_DIR. An empty value keeps the
// gateway from touching the filesystem on the error path.
const (
	DebugDumpDirEnv     = "SUB2API_DEBUG_DUMP_DIR"
	debugDumpMaxBody    = 512 * 1024
	debugDumpHeaderList = "Authorization,Content-Type,Accept,X-Request-Id,User-Agent"
)

// DebugDumpDir returns the configured dump directory, or "" when disabled.
func DebugDumpDir() string {
	return strings.TrimSpace(os.Getenv(DebugDumpDirEnv))
}

// DebugDumpEnabled reports whether failure dumps are being written.
func DebugDumpEnabled() bool { return DebugDumpDir() != "" }

// WriteUpstreamFailureDump stores one failed exchange as a plain-text file whose
// name starts with the timestamp so a directory listing is chronological. It is
// best-effort: a dump that cannot be written never changes the request outcome.
func WriteUpstreamFailureDump(
	c *gin.Context,
	account *Account,
	outboundBody []byte,
	resp *http.Response,
	responseBody []byte,
) {
	dir := DebugDumpDir()
	if dir == "" && IsDeepSeekDiagnosticError(responseBody) {
		// These failures are rare, unreproducible by hand, and carry the only
		// evidence we get: keep them even when the instance was never pointed at
		// a dump directory.
		dir = DefaultDebugDumpDir
	}
	if dir == "" || c == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}

	var b strings.Builder
	stamp := time.Now()
	fmt.Fprintf(&b, "# sub2api upstream failure dump\n")
	fmt.Fprintf(&b, "time: %s\n", stamp.Format(time.RFC3339Nano))
	if c.Request != nil {
		fmt.Fprintf(&b, "inbound: %s %s\n", c.Request.Method, c.Request.URL.Path)
	}
	fmt.Fprintf(&b, "gin_request_id: %s\n", c.GetString("request_id"))
	if account != nil {
		fmt.Fprintf(&b, "account_id: %d\naccount_name: %s\nplatform: %s\n", account.ID, account.Name, account.Platform)
		fmt.Fprintf(&b, "account_base_url: %s\n", account.GetCredential("base_url"))
	}
	if resp != nil {
		fmt.Fprintf(&b, "upstream_status: %d\n", resp.StatusCode)
		if resp.Request != nil && resp.Request.URL != nil {
			fmt.Fprintf(&b, "upstream_url: %s\n", resp.Request.URL.String())
		}
		fmt.Fprintf(&b, "\n## upstream request headers\n")
		writeDumpHeaders(&b, resp.Request)
		fmt.Fprintf(&b, "\n## upstream response headers\n")
		writeDumpHeaderMap(&b, resp.Header)
	}

	if steps := debugStepsOf(c); len(steps) > 0 {
		fmt.Fprintf(&b, "\n## request path (what sub2api did, in order)\n")
		for _, step := range steps {
			fmt.Fprintf(&b, "%s  %-22s %s\n", step.At.Format("15:04:05.000"), step.Name, step.Detail)
		}
	}

	if inbound, ok := c.Get(debugInboundBodyKey); ok {
		if raw, ok := inbound.([]byte); ok && len(raw) > 0 {
			fmt.Fprintf(&b, "\n## inbound body (what the client sent, %d bytes)\n", len(raw))
			fmt.Fprintf(&b, "%s\n", truncateDumpBody(string(raw)))
		}
	}

	fmt.Fprintf(&b, "\n## outbound body (what sub2api sent, %d bytes)\n", len(outboundBody))
	_, _ = b.WriteString(truncateDumpBody(string(outboundBody)))
	fmt.Fprintf(&b, "\n\n## upstream response body (%d bytes)\n", len(responseBody))
	_, _ = b.WriteString(truncateDumpBody(string(responseBody)))
	_, _ = b.WriteString("\n")

	name := fmt.Sprintf("%s_%s.txt", stamp.Format("20060102-150405.000000000"), dumpFileSlug(account, c))
	_ = os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o640)
}

func writeDumpHeaders(b *strings.Builder, req *http.Request) {
	if req == nil {
		_, _ = b.WriteString("(none)\n")
		return
	}
	writeDumpHeaderMap(b, req.Header)
}

func writeDumpHeaderMap(b *strings.Builder, header http.Header) {
	if len(header) == 0 {
		_, _ = b.WriteString("(none)\n")
		return
	}
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range header.Values(k) {
			fmt.Fprintf(b, "%s: %s\n", k, maskDumpHeaderValue(k, v))
		}
	}
}

// maskDumpHeaderValue keeps credentials out of a shareable text file while
// leaving enough of the value to tell keys apart.
func maskDumpHeaderValue(key, value string) string {
	switch strings.ToLower(key) {
	case "authorization", "x-api-key", "api-key", "cookie":
		v := strings.TrimSpace(value)
		if len(v) <= 12 {
			return "***"
		}
		return v[:8] + "***" + v[len(v)-4:]
	}
	return value
}

func truncateDumpBody(body string) string {
	if len(body) <= debugDumpMaxBody {
		return body
	}
	return body[:debugDumpMaxBody] + fmt.Sprintf("\n...[truncated, %d bytes total]", len(body))
}

func dumpFileSlug(account *Account, c *gin.Context) string {
	parts := make([]string, 0, 3)
	if account != nil {
		parts = append(parts, fmt.Sprintf("acc%d", account.ID))
	}
	if c != nil {
		if m := c.GetString("__upstream_model"); m != "" {
			parts = append(parts, sanitizeDumpSlug(m))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "unknown")
	}
	return strings.Join(parts, "_")
}

func sanitizeDumpSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			_, _ = b.WriteRune(r)
		default:
			_ = b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// ListDebugDumps returns the dump files, newest first.
func ListDebugDumps() ([]string, error) {
	dir := DebugDumpDir()
	if dir == "" {
		return nil, fmt.Errorf("debug dump disabled: set %s", DebugDumpDirEnv)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// ReadDebugDump returns one dump file by name. The name is validated so a
// caller cannot walk out of the dump directory.
func ReadDebugDump(name string) ([]byte, error) {
	dir := DebugDumpDir()
	if dir == "" {
		return nil, fmt.Errorf("debug dump disabled: set %s", DebugDumpDirEnv)
	}
	if name == "" || strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
		return nil, fmt.Errorf("invalid dump name")
	}
	if !strings.HasSuffix(name, ".txt") {
		return nil, fmt.Errorf("invalid dump name")
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// --- request path breadcrumbs -------------------------------------------------

const debugStepsCtxKey = "__sub2api_debug_steps"

type debugStep struct {
	At     time.Time
	Name   string
	Detail string
}

// RecordDebugStep appends one breadcrumb describing what the gateway did to the
// request. It is a no-op unless dumps are enabled, so the hot path pays one
// string comparison when debugging is off.
func RecordDebugStep(c *gin.Context, name, detail string) {
	if c == nil || !DebugDumpEnabled() {
		return
	}
	steps, _ := c.Get(debugStepsCtxKey)
	list, _ := steps.([]debugStep)
	list = append(list, debugStep{At: time.Now(), Name: name, Detail: detail})
	c.Set(debugStepsCtxKey, list)
}

func debugStepsOf(c *gin.Context) []debugStep {
	if c == nil {
		return nil
	}
	steps, _ := c.Get(debugStepsCtxKey)
	list, _ := steps.([]debugStep)
	return list
}

// countReasoningItems reports how many reasoning items the body carries and how
// many of them actually hold replayable text. A transform that lowers these
// numbers is the transform that broke the DeepSeek thinking-mode contract.
func countReasoningItems(body []byte) (items, withText int) {
	if len(body) == 0 {
		return 0, 0
	}
	var payload struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Input) == 0 {
		return 0, 0
	}
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(payload.Input, &list); err != nil {
		return 0, 0
	}
	for _, item := range list {
		var typ string
		if err := json.Unmarshal(item["type"], &typ); err != nil || typ != "reasoning" {
			continue
		}
		items++
		var content []map[string]json.RawMessage
		if err := json.Unmarshal(item["content"], &content); err != nil {
			continue
		}
		for _, part := range content {
			var partType, text string
			_ = json.Unmarshal(part["type"], &partType)
			_ = json.Unmarshal(part["text"], &text)
			if partType == "reasoning_text" && strings.TrimSpace(text) != "" {
				withText++
				break
			}
		}
	}
	return items, withText
}

// --- always-capture DeepSeek diagnostics -------------------------------------

// deepSeekDiagnosticMarkers are the upstream rejections that keep coming back
// from DeepSeek official keys and cannot be reproduced by hand. Whenever one of
// them is seen the request body is worth keeping, even on an instance that was
// never started with SUB2API_DEBUG_DUMP_DIR set.
var deepSeekDiagnosticMarkers = []string{
	"reasoning_text",
	"must be passed back",
	"No tool output found for tool call",
	"response_format type is unavailable",
}

// IsDeepSeekDiagnosticError reports whether an upstream body carries one of the
// known DeepSeek contract errors.
func IsDeepSeekDiagnosticError(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	text := string(body)
	for _, marker := range deepSeekDiagnosticMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// DefaultDebugDumpDir is the fallback location used when a diagnostic error is
// forced to disk on an instance without SUB2API_DEBUG_DUMP_DIR configured.
const DefaultDebugDumpDir = "/opt/sub2api/debug-dumps"

// debugInboundBodyKey stores the untouched client body for one request. Without
// it a dump cannot tell whether a field was dropped by the gateway or never
// arrived, which is exactly the question the DeepSeek reasoning failures ask.
const debugInboundBodyKey = "__sub2api_debug_inbound_body"

// RememberInboundBody keeps a copy of the client body for later diagnosis.
func RememberInboundBody(c *gin.Context, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	c.Set(debugInboundBodyKey, body)
}
