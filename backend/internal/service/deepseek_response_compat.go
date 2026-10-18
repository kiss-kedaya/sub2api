package service

// deepSeekResponseCompatEnabled reports whether the DeepSeek response rewriting
// chain (DSML inline tool-call parsing, reasoning reassembly, call_id rebuilding,
// search-tool injection and gateway-side execution) should run.
//
// Official DeepSeek now returns structured Responses natively — reasoning_text
// items, encrypted_content, and function_call with distinct id/call_id — so
// rewriting that output corrupts it. The chain is therefore off unless the
// operator explicitly opts in for a legacy DeepSeek-compatible relay.
func (s *OpenAIGatewayService) deepSeekResponseCompatEnabled() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	return s.cfg.Gateway.DeepSeekResponseCompatEnabled
}
