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

// deepSeekRequestRewriteEnabled reports whether the request-side rewrites
// (store=false, previous_response_id stripping, reasoning.summary removal,
// duplicate call_id dedupe) should run.
//
// These are far less invasive than the response rewrites and are still needed
// for DeepSeek-compatible relays that reject those fields, so they stay on
// regardless of DeepSeekResponseCompatEnabled. They never invent content.
func (s *OpenAIGatewayService) deepSeekRequestRewriteEnabled() bool {
	return true
}
