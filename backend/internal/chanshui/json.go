package chanshui

// Object coerces a decoded JSON value into a map, returning nil for anything
// else. Audit payloads are attacker-influenced, so callers must not assume a
// field keeps its expected type.
func Object(v any) map[string]any { o, _ := v.(map[string]any); return o }
