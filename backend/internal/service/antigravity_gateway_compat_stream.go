package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

type antigravityCompatStreamAdapter interface {
	Emit(*apicompat.AnthropicStreamEvent, *antigravityClientWriter)
	Finalize(*antigravityClientWriter)
	WriteError(*antigravityClientWriter, string)
}

type antigravityChatStreamAdapter struct {
	anthropicState *apicompat.AnthropicEventToResponsesState
	chatState      *apicompat.ResponsesEventToChatState
}

func newAntigravityChatStreamAdapter(model string, includeUsage bool) *antigravityChatStreamAdapter {
	anthropicState := apicompat.NewAnthropicEventToResponsesState()
	anthropicState.Model = model
	chatState := apicompat.NewResponsesEventToChatState()
	chatState.Model = model
	chatState.IncludeUsage = includeUsage
	return &antigravityChatStreamAdapter{
		anthropicState: anthropicState,
		chatState:      chatState,
	}
}

func (a *antigravityChatStreamAdapter) Emit(event *apicompat.AnthropicStreamEvent, writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.AnthropicEventToResponsesEvents(event, a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
}

func (a *antigravityChatStreamAdapter) Finalize(writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
	for _, chunk := range apicompat.FinalizeResponsesChatStream(a.chatState) {
		if data, err := apicompat.ChatChunkToSSE(chunk); err == nil {
			writer.Write([]byte(data))
		}
	}
	writer.Write([]byte("data: [DONE]\n\n"))
}

func (a *antigravityChatStreamAdapter) WriteError(writer *antigravityClientWriter, reason string) {
	writer.Fprintf("data: {\"error\":{\"message\":%q,\"type\":\"upstream_error\"}}\n\n", reason)
}

func (a *antigravityChatStreamAdapter) emitResponseEvent(event *apicompat.ResponsesStreamEvent, writer *antigravityClientWriter) {
	for _, chunk := range apicompat.ResponsesEventToChatChunks(event, a.chatState) {
		if data, err := apicompat.ChatChunkToSSE(chunk); err == nil {
			writer.Write([]byte(data))
		}
	}
}

type antigravityResponsesStreamAdapter struct {
	anthropicState     *apicompat.AnthropicEventToResponsesState
	clientToolMapping  apicompat.ResponsesClientToolMapping
	clientToolRestorer *apicompat.ResponsesClientToolStreamRestorer
}

func newAntigravityResponsesStreamAdapter(model string, clientToolMapping apicompat.ResponsesClientToolMapping) *antigravityResponsesStreamAdapter {
	state := apicompat.NewAnthropicEventToResponsesState()
	state.Model = model
	var restorer *apicompat.ResponsesClientToolStreamRestorer
	if clientToolMapping.CustomTools != nil || clientToolMapping.ToolSearch || clientToolMapping.LocalShellTools != nil || clientToolMapping.NamespaceTools != nil {
		restorer = apicompat.NewResponsesClientToolStreamRestorer(clientToolMapping)
	}
	return &antigravityResponsesStreamAdapter{anthropicState: state, clientToolMapping: clientToolMapping, clientToolRestorer: restorer}
}

func (a *antigravityResponsesStreamAdapter) Emit(event *apicompat.AnthropicStreamEvent, writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.AnthropicEventToResponsesEvents(event, a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}

func (a *antigravityResponsesStreamAdapter) Finalize(writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}

func (a *antigravityResponsesStreamAdapter) WriteError(writer *antigravityClientWriter, reason string) {
	writer.Fprintf("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":%q}}\n\n", reason)
}

func (a *antigravityResponsesStreamAdapter) emitResponseEvent(event apicompat.ResponsesStreamEvent, writer *antigravityClientWriter) {
	if data, err := apicompat.ResponsesEventToSSE(event); err == nil {
		writer.Write(a.restoreClientToolPayload([]byte(data)))
	}
}

// restoreClientToolPayload 把上游的 function_call 还原为客户端工具类型
// （custom_tool_call/local_shell_call），codex 才能按类型路由调用到终端。
func (a *antigravityResponsesStreamAdapter) restoreClientToolPayload(data []byte) []byte {
	if a.clientToolRestorer == nil {
		return data
	}
	restored, _, err := a.clientToolRestorer.RestoreEvent(data)
	if err != nil || len(restored) == 0 {
		return data
	}
	// 自定义工具的完成事件可能展开为多个 payload；逐一写回由调用方 flush。
	var out []byte
	for _, payload := range restored {
		out = append(out, payload...)
	}
	return out
}

type antigravityCompatScanEvent struct {
	line string
	err  error
}

type antigravityCompatStreamSession struct {
	processor      *antigravity.StreamingProcessor
	adapter        antigravityCompatStreamAdapter
	writer         *antigravityClientWriter
	usage          *ClaudeUsage
	pendingEvents  []apicompat.AnthropicStreamEvent
	firstTokenMs   *int
	startTime      time.Time
	meaningfulData bool
}

func newAntigravityCompatStreamSession(
	model string,
	startTime time.Time,
	adapter antigravityCompatStreamAdapter,
	writer *antigravityClientWriter,
) *antigravityCompatStreamSession {
	return &antigravityCompatStreamSession{
		processor: antigravity.NewStreamingProcessor(model),
		adapter:   adapter,
		writer:    writer,
		usage:     &ClaudeUsage{},
		startTime: startTime,
	}
}

func (s *antigravityCompatStreamSession) consume(line string) {
	claudeEvents := s.processor.ProcessLine(strings.TrimRight(line, "\r\n"))
	if len(claudeEvents) == 0 {
		return
	}
	s.consumeClaudeEvents(claudeEvents)
}

func (s *antigravityCompatStreamSession) hasMeaningfulData() bool {
	return s.meaningfulData
}

func (s *antigravityCompatStreamSession) finish() *antigravityStreamResult {
	finalEvents, usage := s.processor.Finish()
	mergeAntigravityCompatUsage(s.usage, usage)
	s.consumeClaudeEvents(finalEvents)
	s.adapter.Finalize(s.writer)
	return s.result(s.writer.Disconnected())
}

func (s *antigravityCompatStreamSession) collectResult(clientDisconnect bool) *antigravityStreamResult {
	_, usage := s.processor.Finish()
	mergeAntigravityCompatUsage(s.usage, usage)
	return s.result(clientDisconnect)
}

func (s *antigravityCompatStreamSession) result(clientDisconnect bool) *antigravityStreamResult {
	return &antigravityStreamResult{
		usage:            s.usage,
		firstTokenMs:     s.firstTokenMs,
		clientDisconnect: clientDisconnect,
	}
}

func (s *antigravityCompatStreamSession) consumeClaudeEvents(data []byte) {
	var eventType string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			s.consumeClaudeData(eventType, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

func (s *antigravityCompatStreamSession) consumeClaudeData(eventType, payload string) {
	var event apicompat.AnthropicStreamEvent
	if json.Unmarshal([]byte(payload), &event) != nil {
		return
	}
	if event.Type == "" {
		event.Type = eventType
	}
	if event.Usage != nil {
		mergeAnthropicUsage(s.usage, *event.Usage)
	}
	if event.Message != nil {
		mergeAnthropicUsage(s.usage, event.Message.Usage)
	}
	s.emitOrBuffer(event)
}

func (s *antigravityCompatStreamSession) emitOrBuffer(event apicompat.AnthropicStreamEvent) {
	if s.meaningfulData {
		s.adapter.Emit(&event, s.writer)
		return
	}

	s.pendingEvents = append(s.pendingEvents, event)
	if !isMeaningfulAntigravityCompatEvent(&event) {
		return
	}

	s.meaningfulData = true
	ms := int(time.Since(s.startTime).Milliseconds())
	s.firstTokenMs = &ms
	for i := range s.pendingEvents {
		s.adapter.Emit(&s.pendingEvents[i], s.writer)
	}
	s.pendingEvents = nil
}

func isMeaningfulAntigravityCompatEvent(event *apicompat.AnthropicStreamEvent) bool {
	if event == nil {
		return false
	}
	// 只有真正带内容的事件才算「有效数据」。message_stop、单独的 stop_reason、
	// 单独的 signature 都不算：上游因工具调用畸形返回 MALFORMED_FUNCTION_CALL 时，
	// 流的形状恰好就是「只有 message_stop / stop_reason、没有任何 text 或 tool_use」。
	// 若把这类流判为有效，就不会触发 failover，客户端会拿到「成功但无输出」——
	// 表现就是工具调用凭空消失。与上游 52c2f4cb1 口径一致。
	if event.ContentBlock != nil {
		block := event.ContentBlock
		return block.Type == "tool_use" ||
			block.Text != "" ||
			block.Thinking != "" ||
			block.Source != nil
	}
	if event.Delta != nil {
		delta := event.Delta
		return delta.Text != "" ||
			delta.PartialJSON != "" ||
			delta.Thinking != ""
	}
	return false
}

func mergeAntigravityCompatUsage(dst *ClaudeUsage, src *antigravity.ClaudeUsage) {
	if dst == nil || src == nil {
		return
	}
	dst.InputTokens = src.InputTokens
	dst.OutputTokens = src.OutputTokens
	dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	dst.CacheReadInputTokens = src.CacheReadInputTokens
	dst.ImageOutputTokens = src.ImageOutputTokens
}

func (s *AntigravityGatewayService) handleAntigravityCompatStream(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	adapter antigravityCompatStreamAdapter,
	prefix string,
) (*antigravityStreamResult, error) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	writer := newAntigravityClientWriter(c.Writer, flusher, prefix)
	writer.beforeFirstWrite = func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
	}
	session := newAntigravityCompatStreamSession(originalModel, startTime, adapter, writer)
	events, stopScanner, maxLineSize := s.startAntigravityCompatScanner(resp.Body)
	defer stopScanner()

	timeout := s.antigravityCompatStreamTimeout()
	timeoutTimer, timeoutCh := newAntigravityCompatTimer(timeout)
	if timeoutTimer != nil {
		defer timeoutTimer.Stop()
	}
	keepaliveTicker, keepaliveCh := s.newAntigravityCompatKeepaliveTicker()
	if keepaliveTicker != nil {
		defer keepaliveTicker.Stop()
	}

	for {
		select {
		case event, open := <-events:
			if !open {
				if !session.hasMeaningfulData() && !writer.Disconnected() {
					return nil, antigravityCompatEmptyStreamError()
				}
				return session.finish(), nil
			}
			if event.err != nil {
				return s.handleAntigravityCompatReadError(c, session, event.err, maxLineSize, prefix)
			}
			resetAntigravityCompatTimer(timeoutTimer, timeout)
			s.observeAntigravityGeminiSSELine(c, event.line)
			session.consume(event.line)

		case <-timeoutCh:
			if writer.Disconnected() {
				return session.collectResult(true), nil
			}
			if !session.hasMeaningfulData() {
				return nil, antigravityCompatEmptyStreamError()
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (%s)", prefix)
			writeAntigravityCompatStreamError(c, adapter, writer, "stream_timeout")
			return session.collectResult(false), fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if session.hasMeaningfulData() && !writer.Disconnected() {
				writer.Write([]byte(": ping\n\n"))
			}
		}
	}
}

func (s *AntigravityGatewayService) startAntigravityCompatScanner(
	body io.Reader,
) (<-chan antigravityCompatScanEvent, func(), int) {
	maxLineSize := defaultMaxLineSize
	if s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.settingService.cfg.Gateway.MaxLineSize
	}
	scanner := bufio.NewScanner(body)
	scanBuf := getSSEScannerBuf64K()
	attachSSEScannerBuffer(scanner, scanBuf[:], maxLineSize)

	events := make(chan antigravityCompatScanEvent, 16)
	done := make(chan struct{})
	go func() {
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		send := func(event antigravityCompatScanEvent) bool {
			select {
			case events <- event:
				return true
			case <-done:
				return false
			}
		}
		for scanner.Scan() {
			if !send(antigravityCompatScanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			send(antigravityCompatScanEvent{err: err})
		}
	}()
	return events, func() { close(done) }, maxLineSize
}

func (s *AntigravityGatewayService) antigravityCompatStreamTimeout() time.Duration {
	if s.settingService == nil || s.settingService.cfg == nil {
		return 0
	}
	return time.Duration(s.settingService.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
}

func (s *AntigravityGatewayService) newAntigravityCompatKeepaliveTicker() (*time.Ticker, <-chan time.Time) {
	if s.settingService == nil || s.settingService.cfg == nil {
		return nil, nil
	}
	interval := time.Duration(s.settingService.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	if interval <= 0 {
		return nil, nil
	}
	ticker := time.NewTicker(interval)
	return ticker, ticker.C
}

func newAntigravityCompatTimer(timeout time.Duration) (*time.Timer, <-chan time.Time) {
	if timeout <= 0 {
		return nil, nil
	}
	timer := time.NewTimer(timeout)
	return timer, timer.C
}

func resetAntigravityCompatTimer(timer *time.Timer, timeout time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}

func (s *AntigravityGatewayService) handleAntigravityCompatReadError(
	c *gin.Context,
	session *antigravityCompatStreamSession,
	err error,
	maxLineSize int,
	prefix string,
) (*antigravityStreamResult, error) {
	if !session.hasMeaningfulData() && !session.writer.Disconnected() {
		return nil, antigravityCompatEmptyStreamError()
	}
	if disconnect, handled := handleStreamReadError(err, session.writer.Disconnected(), prefix); handled {
		return session.collectResult(disconnect), nil
	}
	if errors.Is(err, bufio.ErrTooLong) {
		logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (%s): max_size=%d error=%v", prefix, maxLineSize, err)
		writeAntigravityCompatStreamError(c, session.adapter, session.writer, "response_too_large")
		return session.result(false), err
	}
	writeAntigravityCompatStreamError(c, session.adapter, session.writer, "stream_read_error")
	return nil, fmt.Errorf("stream read error: %w", err)
}

func writeAntigravityCompatStreamError(
	c *gin.Context,
	adapter antigravityCompatStreamAdapter,
	writer *antigravityClientWriter,
	reason string,
) {
	adapter.WriteError(writer, reason)
	MarkResponseCommitted(c)
}

func antigravityCompatEmptyStreamError() error {
	logger.LegacyPrintf("service.antigravity_gateway", "Empty Antigravity compatibility stream, triggering failover")
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           []byte(`{"error":"empty stream response from upstream"}`),
		RetryableOnSameAccount: true,
	}
}

func (s *AntigravityGatewayService) handleChatCompletionsStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	includeUsage bool,
) (*antigravityStreamResult, error) {
	return s.handleAntigravityCompatStream(
		c,
		resp,
		startTime,
		originalModel,
		newAntigravityChatStreamAdapter(originalModel, includeUsage),
		"antigravity chat completions stream",
	)
}

func (s *AntigravityGatewayService) handleResponsesStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*antigravityStreamResult, error) {
	return s.handleAntigravityCompatStream(
		c,
		resp,
		startTime,
		originalModel,
		newAntigravityResponsesStreamAdapter(originalModel, clientToolMapping),
		"antigravity responses stream",
	)
}
