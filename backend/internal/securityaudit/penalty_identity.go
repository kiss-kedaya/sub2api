package securityaudit

import (
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

var penaltyClaudeSessionPattern = regexp.MustCompile(`(?:^|_)session_([a-zA-Z0-9-]{8,128})$`)

func penaltyRequestSessionIdentity(req Request) string {
	if identity := strings.TrimSpace(req.PenaltySessionIdentity); identity != "" {
		return identity
	}
	for _, path := range []string{"session_id", "conversation_id", "client_metadata.session_id", "client_metadata.thread_id", "metadata.session_id", "prompt_cache_key"} {
		if v := gjson.GetBytes(req.Body, path); v.Type == gjson.String && strings.TrimSpace(v.String()) != "" {
			return v.String()
		}
	}
	// Claude Code uses either a JSON string or the historical user_*_session_*
	// form. The device/account/user prefix is never a conversation identity.
	user := gjson.GetBytes(req.Body, "metadata.user_id").String()
	if len(user) > 4096 {
		return ""
	}
	if gjson.Valid(user) {
		if session := gjson.Get(user, "session_id"); session.Type == gjson.String {
			return session.String()
		}
	}
	if parts := penaltyClaudeSessionPattern.FindStringSubmatch(user); len(parts) == 2 {
		return parts[1]
	}
	return ""
}
