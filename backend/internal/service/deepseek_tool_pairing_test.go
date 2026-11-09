//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDropOrphanDeepSeekToolMessages(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","messages":[
		{"role":"user","content":"weather"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"ok"},
		{"role":"assistant","content":"done"},
		{"role":"tool","tool_call_id":"call_orphan","content":"stale"},
		{"role":"tool","content":"no id"}
	]}`)
	out := dropOrphanDeepSeekToolMessages(body)
	msgs := gjson.GetBytes(out, "messages").Array()
	require.Len(t, msgs, 4)
	require.Equal(t, "call_1", msgs[2].Get("tool_call_id").String())

	clean := []byte(`{"messages":[{"role":"user","content":"q"},{"role":"assistant","tool_calls":[{"id":"c1"}]},{"role":"tool","tool_call_id":"c1","content":"r"}]}`)
	require.Equal(t, string(clean), string(dropOrphanDeepSeekToolMessages(clean)))
}
