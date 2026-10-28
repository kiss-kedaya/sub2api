package requestcapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type contextKey struct{}

func WithSession(ctx context.Context, s *Session) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, s)
}
func FromContext(ctx context.Context) *Session {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(contextKey{}).(*Session)
	return s
}

type event struct {
	created time.Time
	s       *Session
	part    Part
	data    []byte
	end     bool
	targets []string
	charge  int64
	final   *Record
}
type inputChunk struct {
	part Part
	data []byte
	end  bool
}
type partState struct {
	part   Part
	filter *bodyFilter
	ended  bool
	charge int64
}
type recordState struct {
	Record
	parts   map[string]*partState
	blocked bool
	expired bool
	expiry  *captureExpiry
	owner   *Session
}
type Session struct {
	frameMu         sync.Mutex
	frameStreams    map[string]*Stream
	m               *Manager
	mu              sync.Mutex
	meta            Meta
	candidates      []*runtimeTask
	matched         map[string]bool
	inbound         []inputChunk
	inboundBytes    int64
	hasAccounts     bool
	clientObserved  bool
	reason          string
	closed          bool
	failed          bool
	status          int
	attempts        []Attempt
	nextPart        int
	turn            int
	turnEnded       bool
	expiredTurn     bool
	usage           map[string]int64
	errorCode       string
	resultBuffer    []byte
	resultSkip      bool
	resultError     bool
	clientOutcome   string
	done            atomic.Bool
	pending         atomic.Int64
	streams         map[*Stream]struct{}
	finishedTargets map[string]bool
	records         map[string]*recordState // Worker-owned, never accessed by forwarding goroutines.
}

func (m *Manager) Begin(meta Meta) *Session {
	// Never wait for administrative operations on the forwarding path.
	if !m.enabled.Load() {
		return nil
	}
	if !m.mu.TryLock() {
		m.admissionSkipped.Add(1)
		m.noteSkipped("admission_busy")
		return nil
	}
	defer m.mu.Unlock()
	if (!m.config.Enabled && !m.config.ID1DiagnosticEnabled) || m.unhealthy.Load() || m.stopping.Load() {
		return nil
	}
	candidates := []*runtimeTask{}
	matched := map[string]bool{}
	accounts := false
	for id, t := range m.tasks {
		if t.task.Status != "running" || (t.task.Mode != ID1DiagnosticMode && !time.Now().Before(t.task.ExpiresAt)) {
			continue
		}
		if t.task.Mode == ID1DiagnosticMode && (!m.isDiagnosticUser(meta.UserID) || !m.config.ID1DiagnosticEnabled) {
			continue
		}
		if t.task.Mode != ID1DiagnosticMode && !m.config.Enabled {
			continue
		}
		hit := t.task.TargetType == "user" && t.task.TargetID == meta.UserID ||
			t.task.TargetType == "group" && t.task.TargetID == meta.GroupID ||
			t.task.Mode == ID1DiagnosticMode && m.isDiagnosticUser(meta.UserID)
		if hit || t.task.TargetType == "account" {
			candidates = append(candidates, t)
			matched[id] = hit
			if t.task.TargetType == "account" {
				accounts = true
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if len(m.sessions) >= MaxSessions || !m.reserve(64<<10) {
		m.admissionSkipped.Add(1)
		m.noteSkipped("session_or_buffer_limit")
		for _, t := range candidates {
			if matched[t.task.ID] {
				t.task.Skipped++
				t.dirty = true
			}
		}
		return nil
	}
	meta.RequestID = bounded(meta.RequestID, 128)
	meta.ClientRequestID = bounded(meta.ClientRequestID, 128)
	meta.SessionID = bounded(meta.SessionID, 256)
	meta.ThreadID = bounded(meta.ThreadID, 256)
	meta.Path = bounded(SafeURL(meta.Path), 2048)
	meta.Method = bounded(meta.Method, 16)
	s := &Session{m: m, meta: meta, candidates: candidates, matched: matched, hasAccounts: accounts, streams: map[*Stream]struct{}{}, records: map[string]*recordState{}, finishedTargets: map[string]bool{}, usage: map[string]int64{}}
	for _, t := range candidates {
		t.refs++
	}
	m.sessions[s] = struct{}{}
	return s
}

func (m *Manager) isDiagnosticUser(id int64) bool {
	if id <= 0 {
		return false
	}
	for _, target := range m.config.DiagnosticTargets() {
		if target == id {
			return true
		}
	}
	return false
}
func bounded(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}
func (s *Session) SetRoutedGroup(id int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.meta.RoutedGroupID = id
	s.mu.Unlock()
}
func (s *Session) MarkPartial(reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.reason == "" {
		s.reason = reason
	}
	s.mu.Unlock()
}

// MarkError records a confirmed forwarding failure, not a capture resource error.
func (s *Session) MarkError(code string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed && !s.turnEnded {
		s.resultError = true
		s.errorCode = bounded(code, 128)
	}
}
func (s *Session) RequireClientInput() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clientObserved && s.reason == "" {
		s.reason = "client_input_unavailable"
	}
}
func (s *Session) SetProtocol(protocol string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.meta.Protocol = protocol
	s.mu.Unlock()
}

func (s *Session) ClientRequest(body []byte, ct string, h http.Header) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.meta.Model = bounded(gjson.GetBytes(body, "model").String(), 256)
	if s.meta.SessionID == "" {
		s.meta.SessionID = bounded(gjson.GetBytes(body, "session_id").String(), 256)
	}
	if s.meta.ThreadID == "" {
		s.meta.ThreadID = bounded(gjson.GetBytes(body, "client_metadata.thread_id").String(), 256)
	}
	if s.clientObserved || s.closed {
		s.mu.Unlock()
		return
	}
	s.clientObserved = true
	s.mu.Unlock()
	stream := s.NewStream("client_request", 0, 0, ct, h)
	_, _ = stream.Write(body)
	_ = stream.Close()
}
func (s *Session) ClientFrame(body []byte) {
	if s == nil {
		return
	}
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	s.mu.Lock()
	if s.closed || s.failed {
		s.mu.Unlock()
		return
	}
	if gjson.GetBytes(body, "type").String() == "response.create" {
		if s.turn > 0 && !s.turnEnded {
			// Pipelined turns cannot be safely attributed. Stop capture only.
			s.failed = true
			s.reason = "overlapping_websocket_turns"
			s.mu.Unlock()
			s.m.signal()
			return
		}
		s.clientObserved = true
		s.turn++
		s.turnEnded = false
		s.expiredTurn = false
		s.status, s.nextPart = 0, 0
		s.attempts = nil
		s.reason, s.errorCode = "", ""
		s.resultError, s.resultSkip = false, false
		s.clientOutcome = ""
		s.resultBuffer = s.resultBuffer[:0]
		s.usage = map[string]int64{}
		for _, t := range s.candidates {
			if t.task.TargetType == "account" {
				s.matched[t.task.ID] = false
			}
		}
		s.releaseInputLocked()
		s.meta.Model = bounded(gjson.GetBytes(body, "model").String(), 256)
		if id := gjson.GetBytes(body, "client_metadata.thread_id").String(); id != "" {
			s.meta.ThreadID = bounded(id, 256)
		}
	}
	turn := s.turn
	s.mu.Unlock()
	stream := s.NewStream("client_request", 0, turn, "application/json", nil)
	_, _ = stream.Write(body)
	_ = stream.Close()
}
func (s *Session) BeginAttempt(account int64) int {
	return s.BeginRoutedAttempt(account, transportdiag.Route{})
}

// BeginRoutedAttempt snapshots route metadata at the actual attempt boundary.
func (s *Session) BeginRoutedAttempt(account int64, route transportdiag.Route) int {
	return s.beginAttempt(account, route, "request")
}

func (s *Session) BeginHandshake(account int64, route transportdiag.Route) int {
	return s.beginAttempt(account, route, "websocket_handshake")
}

func (s *Session) beginAttempt(account int64, route transportdiag.Route, phase string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.turnEnded {
		return 0
	}
	if len(s.attempts) >= 256 {
		if s.reason == "" {
			s.reason = "attempt_limit"
		}
		return 0
	}
	n := len(s.attempts) + 1
	s.attempts = append(s.attempts, Attempt{Number: n, AccountID: account, StartedAt: time.Now().UTC(), Route: route, Phase: phase})
	s.bindAccountLocked(account)
	return n
}
func (s *Session) BindAccount(account int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.bindAccountLocked(account)
	}
}
func (s *Session) bindAccountLocked(account int64) {
	for _, t := range s.candidates {
		if !s.finishedTargets[t.task.ID] && t.active.Load() && t.task.TargetType == "account" && t.task.TargetID == account && !s.matched[t.task.ID] {
			s.matched[t.task.ID] = true
			for _, chunk := range s.inbound {
				s.enqueueLocked(chunk.part, chunk.data, chunk.end, []string{t.task.ID})
			}
		}
	}
}

// Selection failures have no request body sent upstream, but still identify the attempted account.
func (s *Session) SelectionFailed(account int64, status int, headers http.Header, body []byte, err error) {
	if s == nil {
		return
	}
	n := s.BeginAttempt(account)
	s.AttemptResponse(n, status, headers, err)
	if len(body) > 0 {
		st := s.NewStream("upstream_response", n, 0, headers.Get("Content-Type"), headers)
		_, _ = st.Write(body)
		_ = st.Close()
	}
}
func (s *Session) AttemptResponse(n, status int, h http.Header, err error) {
	if s == nil || n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > len(s.attempts) {
		return
	}
	a := &s.attempts[n-1]
	a.Status = status
	if a.Phase == "websocket_handshake" {
		ms := float64(time.Since(a.StartedAt)) / float64(time.Millisecond)
		a.HandshakeMS = &ms
	}
	if a.Route.Upstream == "http" {
		a.Route.ResponseFormat = transportdiag.ResponseFormat(h)
	}
	a.UpstreamRequestID = bounded(h.Get("x-request-id"), 128)
	if a.UpstreamRequestID == "" {
		a.UpstreamRequestID = bounded(h.Get("request-id"), 128)
	}
	// Transport errors may contain credentials or a signed URL. Preserve class only.
	if err != nil {
		a.Error = transportdiag.Classify(err)
		a.ErrorStage = "transport"
		if a.Phase == "websocket_handshake" {
			a.ErrorStage = "websocket_handshake"
		}
	}
}
func (s *Session) Finish(status int) {
	if s == nil {
		return
	}
	s.closeFrameStreams()
	s.mu.Lock()
	s.status = status
	s.done.Store(true)
	s.mu.Unlock()
	s.m.signal()
}
func (s *Session) releaseInputLocked() {
	s.m.buffer.Add(-s.inboundBytes)
	s.inboundBytes = 0
	s.inbound = nil
}

type Stream struct {
	s               *Session
	part            Part
	mu              sync.Mutex
	buffer          []byte
	diagnostic      []byte
	diagnosticSkip  bool
	observer        *responseObserver
	terminal        string
	responseFailed  bool
	sse             bool
	expectsTerminal bool
	charge          int64
	lastFlush       time.Time
	closed          bool
}

func (s *Session) NewStream(stage string, attempt, turn int, ct string, h http.Header) *Stream {
	if s == nil {
		return &Stream{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed || s.expiredTurn || (s.meta.Protocol == "websocket" && s.turnEnded) {
		return &Stream{}
	}
	s.nextPart++
	n := s.nextPart
	if turn == 0 && s.meta.Protocol == "websocket" {
		turn = s.turn
	}
	st := &Stream{s: s, part: Part{Name: fmt.Sprintf("%06d-%s.txt", n, stage), Stage: stage, Attempt: attempt, Turn: turn, ContentType: bounded(ct, 256), Headers: SafeHeaders(h)}, lastFlush: time.Now()}
	s.streams[st] = struct{}{}
	return st
}
func (st *Stream) Write(p []byte) (int, error) {
	n := len(p)
	if st == nil || st.s == nil {
		return n, nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return n, nil
	}
	st.s.mu.Lock()
	inactive := st.s.closed || st.s.failed
	st.s.mu.Unlock()
	if inactive {
		return n, nil
	}
	if st.buffer == nil {
		st.charge = ChunkSize
		if strings.HasSuffix(st.part.Stage, "_response") {
			st.charge += (16 << 10) + responseObserverCharge
		}
		if !st.s.m.reserve(st.charge) {
			st.s.failCapture("buffer_limit")
			return n, nil
		}
		st.buffer = make([]byte, 0, ChunkSize)
		if st.charge > ChunkSize {
			st.diagnostic = make([]byte, 0, 16<<10)
			st.observer = &responseObserver{framing: newBodyFraming(st.part.ContentType)}
		}
	}
	if strings.HasSuffix(st.part.Stage, "_response") {
		st.observer.write(p)
		st.observeOutcomeLocked()
		st.s.mu.Lock()
		if st.sse {
			st.s.observeResultLocked(p, &st.diagnostic, &st.diagnosticSkip)
		} else if !st.diagnosticSkip {
			if len(st.diagnostic)+len(p) > 16<<10 {
				st.diagnostic = st.diagnostic[:0]
				st.diagnosticSkip = true
				if st.s.reason == "" {
					st.s.reason = "diagnostic_limit"
				}
			} else {
				st.diagnostic = append(st.diagnostic, p...)
				if gjson.ValidBytes(st.diagnostic) {
					st.s.parseResultLocked(st.diagnostic)
				}
			}
		}
		st.s.mu.Unlock()
	}
	for len(p) > 0 {
		count := ChunkSize - len(st.buffer)
		if count > len(p) {
			count = len(p)
		}
		st.buffer = append(st.buffer, p[:count]...)
		p = p[count:]
		if len(st.buffer) == ChunkSize {
			st.s.send(st.part, st.buffer, false)
			st.buffer = st.buffer[:0]
			st.lastFlush = time.Now()
		}
	}
	if time.Since(st.lastFlush) >= 250*time.Millisecond && len(st.buffer) > 0 {
		st.s.send(st.part, st.buffer, false)
		st.buffer = st.buffer[:0]
		st.lastFlush = time.Now()
	}
	return n, nil
}
func (st *Stream) Close() error {
	if st == nil || st.s == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil
	}
	st.closed = true
	if st.observer != nil {
		st.observer.end()
		st.observeOutcomeLocked()
		st.observer = nil
	}
	st.s.mu.Lock()
	if !st.diagnosticSkip {
		st.s.parseResultLocked(st.diagnostic)
	}
	st.s.mu.Unlock()
	st.s.send(st.part, st.buffer, true)
	if st.buffer != nil {
		st.s.m.buffer.Add(-st.charge)
		st.buffer = nil
		st.diagnostic = nil
	}
	st.s.mu.Lock()
	delete(st.s.streams, st)
	st.s.mu.Unlock()
	return nil
}
func (st *Stream) discard() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.closed = true
	st.observer = nil
	if st.buffer != nil {
		st.s.m.buffer.Add(-st.charge)
		st.buffer = nil
		st.diagnostic = nil
	}
}

// Called under st.mu. A terminal never clears a real failure from this or an
// earlier attempt; it only distinguishes post-completion read/close noise.
func (st *Stream) observeOutcomeLocked() {
	st.terminal, st.responseFailed, st.sse = st.observer.terminal, st.observer.failed, st.observer.framing.sse
	st.expectsTerminal = st.observer.expectsTerminal
	st.s.mu.Lock()
	defer st.s.mu.Unlock()
	if st.s.closed || st.s.turnEnded {
		return
	}
	if st.part.Stage == "client_response" {
		if st.responseFailed {
			st.s.clientOutcome = "failed"
		} else if st.s.clientOutcome != "failed" && st.observer.successful() {
			st.s.clientOutcome = "completed"
		}
	}
	if st.responseFailed {
		st.s.resultError = true
		if st.s.errorCode == "" {
			st.s.errorCode = "response_failed"
		}
	}
	if st.part.Stage == "upstream_response" && st.part.Attempt > 0 && st.part.Attempt <= len(st.s.attempts) {
		a := &st.s.attempts[st.part.Attempt-1]
		a.ResponseTerminal = st.terminal
		if a.Phase != "websocket_handshake" && st.observer.sawEvent && a.FirstEventMS == nil {
			ms := float64(time.Since(a.StartedAt)) / float64(time.Millisecond)
			a.FirstEventMS = &ms
		}
	}
}
func (s *Session) failCapture(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
	if s.reason == "" {
		s.reason = reason
		s.m.noteSkipped(reason)
	}
}
func (s *Session) send(part Part, p []byte, end bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed || s.expiredTurn || s.m.stopping.Load() {
		return
	}
	targets := []string{}
	for _, t := range s.candidates {
		if s.finishedTargets[t.task.ID] || !t.active.Load() || !s.matched[t.task.ID] {
			continue
		}
		if strings.HasPrefix(part.Stage, "upstream_") && t.task.TargetType == "account" {
			if part.Attempt <= 0 || part.Attempt > len(s.attempts) || s.attempts[part.Attempt-1].AccountID != t.task.TargetID {
				continue
			}
		}
		targets = append(targets, t.task.ID)
	}
	if part.Stage == "client_request" && s.hasAccounts {
		charge := int64(len(p) + 512)
		if s.m.reserve(charge) {
			s.inbound = append(s.inbound, inputChunk{part, append([]byte(nil), p...), end})
			s.inboundBytes += charge
		} else {
			s.failed = true
			if s.reason == "" {
				s.reason = "buffer_limit"
			}
		}
	}
	if len(targets) > 0 {
		s.enqueueLocked(part, p, end, targets)
	}
}
func (s *Session) enqueueLocked(part Part, p []byte, end bool, targets []string) {
	if s.failed {
		return
	}
	charge := int64(len(p) + 512)
	if !s.m.reserve(charge) {
		s.failed = true
		if s.reason == "" {
			s.reason = "buffer_limit"
			s.m.noteSkipped("buffer_limit")
		}
		return
	}
	e := event{s: s, part: part, data: append([]byte(nil), p...), end: end, targets: targets, charge: charge, created: time.Now().UTC()}
	s.pending.Add(1)
	select {
	case s.m.queue <- e:
	default:
		s.pending.Add(-1)
		s.m.buffer.Add(-charge)
		s.failed = true
		if s.reason == "" {
			s.reason = "queue_full"
			s.m.noteSkipped("queue_full")
		}
	}
}
func (s *Session) snapshotLocked(r *Record) {
	r.Meta = s.meta
	r.Turn = s.turn
	r.Status = s.status
	r.IsError = s.status >= 400 || s.resultError
	r.Attempts = append([]Attempt(nil), s.attempts...)
	for _, a := range s.attempts {
		if a.Status >= 400 || a.Error != "" {
			r.IsError = true
		}
	}
	r.ErrorCode = s.errorCode
	r.ClientOutcome = s.clientOutcome
	r.Usage = map[string]int64{}
	for k, v := range s.usage {
		r.Usage[k] = v
	}
	if s.reason != "" {
		r.Partial = true
		r.Reason = s.reason
	}
}

// Keep at most 16 KiB of a partial diagnostic event, charged to the session budget.
func (s *Session) ObserveResult(body []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.observeResultLocked(body, &s.resultBuffer, &s.resultSkip)
}
func (s *Session) observeResultLocked(body []byte, buffer *[]byte, skip *bool) {
	for _, b := range body {
		if b == '\n' {
			if !*skip {
				s.parseResultLocked(*buffer)
			}
			*buffer = (*buffer)[:0]
			*skip = false
			continue
		}
		if *skip {
			continue
		}
		if len(*buffer) == 16<<10 {
			*buffer = (*buffer)[:0]
			*skip = true
			if s.reason == "" {
				s.reason = "diagnostic_limit"
			}
			continue
		}
		*buffer = append(*buffer, b)
	}
	if len(*buffer) > 0 && gjson.ValidBytes(*buffer) {
		s.parseResultLocked(*buffer)
		*buffer = (*buffer)[:0]
	}
}
func (s *Session) parseResultLocked(body []byte) {
	line := strings.TrimSpace(strings.TrimPrefix(string(body), "data:"))
	if strings.HasPrefix(line, "event:") {
		event := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		if event == "error" || event == "response.failed" {
			s.resultError = true
		}
		return
	}
	if !gjson.Valid(line) {
		return
	}
	event := gjson.Get(line, "type").String()
	hasError := func(path string) bool {
		v := gjson.Get(line, path)
		switch v.Type {
		case gjson.True:
			return true
		case gjson.String:
			return v.Str != ""
		case gjson.JSON:
			if !v.IsObject() {
				return false
			}
			nonempty := false
			v.ForEach(func(_, _ gjson.Result) bool { nonempty = true; return false })
			return nonempty
		default:
			return false
		}
	}
	if hasError("error") || hasError("response.error") || event == "response.failed" || event == "error" || gjson.Get(line, "response.status").String() == "failed" {
		s.resultError = true
	}
	for _, prefix := range []string{"error.", "response.error."} {
		if code := gjson.Get(line, prefix+"code"); code.Exists() {
			s.errorCode = bounded(code.String(), 128)
		}
	}
	for _, prefix := range []string{"usage.", "response.usage.", "message.usage."} {
		for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "prompt_tokens", "completion_tokens"} {
			if v := gjson.Get(line, prefix+key); v.Exists() {
				s.usage[key] = v.Int()
			}
		}
	}
}

func (m *Manager) process(e event) {
	defer func() {
		m.buffer.Add(-e.charge)
		if e.s.pending.Add(-1) == 0 && e.s.done.Load() {
			m.finish(e.s)
		}
	}()
	if e.final != nil {
		m.finishTurn(e.s, e.final, e.targets)
		return
	}
	for _, target := range e.targets {
		if e.s.finishedTargets[target] {
			continue
		}
		m.mu.Lock()
		t := m.tasks[target]
		running := t != nil && t.task.Status == "running" && (t.task.Mode == ID1DiagnosticMode || time.Now().Before(t.task.ExpiresAt))
		m.mu.Unlock()
		key := recordKey(target, e.part.Turn)
		r := e.s.records[key]
		if !running {
			if r != nil {
				r.Partial = true
				if r.Reason == "" {
					r.Reason = "task_stopped"
				}
			}
			continue
		}
		if r == nil {
			created := e.created
			if created.IsZero() {
				created = time.Now().UTC()
			}
			r = &recordState{Record: Record{ID: uuid.NewString(), TaskID: target, InstanceID: m.instance, Turn: e.part.Turn, CreatedAt: created}, parts: map[string]*partState{}}
			if !m.prepareDiagnosticRecord(r, t) {
				m.mu.Lock()
				t.task.Skipped++
				t.dirty = true
				m.mu.Unlock()
				continue
			}
			r.owner = e.s
			// Pending bodies have no queryable index. Publish only finalized errors.
			e.s.records[key] = r
		}
		if r.blocked || recordExpired(&r.Record, time.Now()) {
			continue
		}
		part := r.parts[e.part.Name]
		if part == nil {
			if len(r.parts) >= 512 || !m.reserve(24<<10) {
				r.Partial = true
				r.Reason = "part_or_buffer_limit"
				r.blocked = true
				m.finishTarget(e.s, t)
				continue
			}
			part = &partState{part: e.part, filter: newBodyFilter(e.part.ContentType, t.task.SaveMedia), charge: 24 << 10}
			r.parts[e.part.Name] = part
		}
		if part.ended {
			continue
		}
		data := part.filter.Write(e.data)
		if e.end {
			tail, reason := part.filter.End()
			data = append(data, tail...)
			part.ended = true
			part.part.Omitted = reason
			if reason != "" && reason != "media_metadata_only" {
				r.Partial = true
				if r.Reason == "" {
					r.Reason = reason
				}
			}
		}
		if err := m.writePart(r, part, data); err != nil {
			r.Partial = true
			r.Reason = err.Error()
			r.blocked = true
		}
		if e.end {
			part.filter = nil
			m.buffer.Add(-(part.charge - 1024))
			part.charge = 1024
		}
		if r.blocked {
			m.finishTarget(e.s, t)
		}
	}
}
func (m *Manager) writePart(r *recordState, p *partState, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	m.storageMu.Lock()
	defer m.storageMu.Unlock()
	if r.expired || recordExpired(&r.Record, time.Now()) {
		return fmt.Errorf("retention_expired")
	}
	m.mu.Lock()
	t := m.tasks[r.TaskID]
	running := t != nil && t.task.Status == "running"
	quota := m.config.QuotaMiB << 20
	m.mu.Unlock()
	if !running {
		return fmt.Errorf("task_stopped")
	}
	if r.Bytes+int64(len(data)) > RecordLimit {
		return fmt.Errorf("record_limit")
	}
	// The reservation remains charged during append; readers see only persisted bytes.
	if m.used.Load()+int64(len(data)) > quota {
		m.stopAll("quota_exceeded")
		return fmt.Errorf("quota_exceeded")
	}
	dir := filepath.Join(m.dir, r.TaskID, r.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		m.unhealthy.Store(true)
		m.stopAll("disk_write_failed")
		return fmt.Errorf("disk_write_failed")
	}
	file, err := os.OpenFile(filepath.Join(dir, p.part.Name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		m.unhealthy.Store(true)
		m.stopAll("disk_write_failed")
		return fmt.Errorf("disk_write_failed")
	}
	m.used.Add(int64(len(data)))
	n, err := file.Write(data)
	closeErr := file.Close()
	m.used.Add(int64(n - len(data)))
	r.Bytes += int64(n)
	p.part.Bytes += int64(n)
	if err != nil || closeErr != nil {
		m.unhealthy.Store(true)
		m.stopAll("disk_write_failed")
		return fmt.Errorf("disk_write_failed")
	}
	return nil
}
func recordKey(task string, turn int) string { return fmt.Sprintf("%s:%d", task, turn) }

// Only the worker finalizes records. Ordinary targeted captures discard success
// and unclassified payloads; ID1 diagnostics retain all outcomes until expiry.
func (m *Manager) finishRecord(r *recordState, t *runtimeTask, snapshot *Record) {
	defer func() {
		for _, p := range r.parts {
			m.buffer.Add(-p.charge)
			p.charge = 0
		}
		if r.expiry != nil {
			r.expiry.record = nil
		}
		r.parts = nil
	}()
	if r.expired || recordExpired(&r.Record, time.Now()) {
		return
	}
	r.Meta, r.Turn = snapshot.Meta, snapshot.Turn
	r.Status, r.IsError = snapshot.Status, snapshot.IsError
	r.ClientOutcome = snapshot.ClientOutcome
	r.Attempts, r.Usage, r.ErrorCode = snapshot.Attempts, snapshot.Usage, snapshot.ErrorCode
	if snapshot.Partial && r.Reason == "" {
		r.Partial, r.Reason = true, snapshot.Reason
	}
	if !t.active.Load() {
		r.Partial = true
		if r.Reason == "" {
			r.Reason = "task_stopped"
		}
	}
	for _, p := range r.parts {
		if !p.ended && (r.IsError || t.task.Mode == ID1DiagnosticMode) {
			tail, reason := p.filter.End()
			p.part.Omitted = reason
			r.Partial = true
			if r.Reason == "" {
				r.Reason = "stream_not_completed"
			}
			if t.active.Load() && !r.blocked {
				if err := m.writePart(r, p, tail); err != nil {
					r.Reason = err.Error()
				}
			}
		}
		m.buffer.Add(-p.charge)
		p.charge = 0
		p.filter = nil
		r.Parts = append(r.Parts, p.part)
	}
	sort.Slice(r.Parts, func(i, j int) bool { return r.Parts[i].Name < r.Parts[j].Name })
	if !r.IsError && t.task.Mode != ID1DiagnosticMode {
		if r.Partial {
			m.mu.Lock()
			t.task.Skipped++
			t.dirty = true
			m.mu.Unlock()
		}
		m.discardRecord(r)
		return
	}
	now := time.Now().UTC()
	r.FinishedAt = &now
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m.storageMu.Lock()
	m.mu.Lock()
	exists := m.tasks[r.TaskID] != nil
	m.mu.Unlock()
	var err error
	if exists {
		err = m.store.SaveRecord(ctx, &r.Record)
	}
	m.storageMu.Unlock()
	if !exists || err != nil {
		m.discardRecord(r)
		if err != nil {
			m.unhealthy.Store(true)
			m.stopAll("index_write_failed")
		}
		return
	}
	m.mu.Lock()
	t.task.Requests++
	t.task.Bytes += r.Bytes
	if r.Partial {
		t.task.Partial++
		m.noteSkipped(r.Reason)
	}
	if r.expiry != nil {
		r.expiry.persisted = true
		r.expiry.bytes = r.Bytes
		r.expiry.partial = r.Partial
	}
	t.dirty = true
	m.mu.Unlock()
}

func (m *Manager) discardRecord(r *recordState) {
	m.storageMu.Lock()
	defer m.storageMu.Unlock()
	if err := os.RemoveAll(filepath.Join(m.dir, r.TaskID, r.ID)); err != nil {
		// Fail closed. Orphan bodies have no index and are retried on restart.
		m.unhealthy.Store(true)
		m.stopAll("privacy_cleanup_failed")
		return
	}
	m.mu.Lock()
	exists := m.tasks[r.TaskID] != nil
	m.mu.Unlock()
	// Delete(task) has already released files belonging to removed tasks.
	if exists {
		m.used.Add(-r.Bytes)
	}
}

func (m *Manager) finishTurn(s *Session, snapshot *Record, targets []string) {
	for _, id := range targets {
		if s.finishedTargets[id] {
			continue
		}
		m.mu.Lock()
		t := m.tasks[id]
		m.mu.Unlock()
		if t == nil {
			continue
		}
		key := recordKey(id, snapshot.Turn)
		r := s.records[key]
		if r == nil && (snapshot.IsError || t.task.Mode == ID1DiagnosticMode) {
			r = &recordState{Record: Record{ID: uuid.NewString(), TaskID: id, InstanceID: m.instance, CreatedAt: time.Now().UTC(), Partial: true, Reason: "no_payload_recorded"}, parts: map[string]*partState{}}
			if !m.prepareDiagnosticRecord(r, t) {
				continue
			}
		}
		if r != nil {
			m.finishRecord(r, t, snapshot)
			delete(s.records, key)
		}
	}
}

func (m *Manager) finish(s *Session) {
	s.mu.Lock()
	if s.closed || s.pending.Load() != 0 {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if !s.done.Load() && s.reason == "" {
		s.reason = "task_stopped"
	}
	s.releaseInputLocked()
	streams := make([]*Stream, 0, len(s.streams))
	for st := range s.streams {
		streams = append(streams, st)
	}
	s.streams = nil
	s.mu.Unlock()
	for _, st := range streams {
		st.discard()
	}
	for _, target := range s.candidates {
		m.finishTarget(s, target)
	}
	m.mu.Lock()
	delete(m.sessions, s)
	m.mu.Unlock()
	m.buffer.Add(-(64 << 10))
}

// The worker finalizes each target independently, including overlapping tasks.
func (m *Manager) finishTarget(s *Session, t *runtimeTask) {
	id := t.task.ID
	s.mu.Lock()
	if s.finishedTargets[id] {
		s.mu.Unlock()
		return
	}
	s.finishedTargets[id] = true
	hit := s.matched[id] && !s.turnEnded
	snapshot := &Record{}
	s.snapshotLocked(snapshot)
	if !s.done.Load() {
		snapshot.Partial = true
		if snapshot.Reason == "" {
			snapshot.Reason = "task_stopped"
		}
	}
	s.mu.Unlock()
	found := false
	for key, r := range s.records {
		if r.TaskID != id {
			continue
		}
		found = true
		// A missing turn barrier must not attach a later turn's error to old data.
		snap := snapshot
		if r.Turn != snapshot.Turn {
			snap = &Record{Turn: r.Turn}
		}
		m.finishRecord(r, t, snap)
		delete(s.records, key)
	}
	if hit && !found && (snapshot.IsError || t.task.Mode == ID1DiagnosticMode) {
		r := &recordState{Record: Record{ID: uuid.NewString(), TaskID: id, InstanceID: m.instance, CreatedAt: time.Now().UTC(), Partial: true, Reason: "no_payload_recorded"}, parts: map[string]*partState{}}
		if !m.prepareDiagnosticRecord(r, t) {
			m.mu.Lock()
			t.refs--
			t.task.Skipped++
			t.dirty = true
			m.mu.Unlock()
			return
		}
		m.finishRecord(r, t, snapshot)
	}
	m.mu.Lock()
	t.refs--
	t.dirty = true
	m.mu.Unlock()
}

// WrapBody observes only bytes actually read and preserves the original Close.
type observedBody struct {
	io.ReadCloser
	stream  *Stream
	closing atomic.Bool
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if b.stream.part.Stage == "client_request" && b.stream.s != nil {
			b.stream.s.mu.Lock()
			b.stream.s.clientObserved = true
			b.stream.s.mu.Unlock()
		}
		_, _ = b.stream.Write(p[:n])
	}
	if err != nil {
		_ = b.stream.Close()
		if err == io.EOF {
			b.observeIncomplete("eof_before_terminal")
		} else {
			b.observeReadError(readErrorClass(err))
		}
	}
	return n, err
}
func (b *observedBody) Close() error {
	b.closing.Store(true)
	_ = b.stream.Close()
	err := b.ReadCloser.Close()
	b.observeIncomplete("closed_before_terminal")
	return err
}

func (b *observedBody) observeIncomplete(reason string) {
	b.stream.mu.Lock()
	expectsTerminal, terminal := b.stream.expectsTerminal, b.stream.terminal
	b.stream.mu.Unlock()
	if b.stream.part.Stage == "upstream_response" && expectsTerminal && terminal == "" {
		b.observeReadError(reason)
	}
}

func (b *observedBody) observeReadError(class string) {
	st := b.stream
	st.mu.Lock()
	outcome := responseObserver{terminal: st.terminal, failed: st.responseFailed}
	success := outcome.successful()
	st.mu.Unlock()
	s := st.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.turnEnded {
		return
	}
	if st.part.Attempt > 0 && st.part.Attempt <= len(s.attempts) {
		a := &s.attempts[st.part.Attempt-1]
		if a.ReadError == "" {
			a.ReadError = class
			if a.ErrorStage == "" {
				a.ErrorStage = st.part.Stage
			}
			a.LocalClose = b.closing.Load()
		}
	}
	if success {
		return
	}
	code := "upstream_read_failed"
	if st.part.Stage == "upstream_request" {
		code = "upstream_request_read_failed"
	}
	s.resultError = true
	if s.errorCode == "" {
		s.errorCode = code
	}
	if s.reason == "" {
		s.reason = code
	}
}

// Persist allowlisted classes only: Error() can contain credentials or URLs.
func readErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, http.ErrBodyReadAfterClose), errors.Is(err, net.ErrClosed), errors.Is(err, io.ErrClosedPipe):
		return "body_closed"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "read_error"
}
func ObserveBody(body io.ReadCloser, stream *Stream) io.ReadCloser {
	if body == nil || stream == nil || stream.s == nil {
		return body
	}
	return &observedBody{ReadCloser: body, stream: stream}
}

func (s *Session) ObserveHTTPRequest(req *http.Request, account int64) (int, func(*http.Response, error)) {
	if s == nil {
		return 0, func(*http.Response, error) {}
	}
	diagnostics := traceCaptureRequest(req)
	route := transportdiag.HTTPRoute(req)
	if route.Downstream == "" {
		s.mu.Lock()
		route.Downstream = s.meta.Protocol
		s.mu.Unlock()
	}
	n := s.BeginRoutedAttempt(account, route)
	st := s.NewStream("upstream_request", n, 0, req.Header.Get("Content-Type"), req.Header)
	st.part.URL = SafeURL(req.URL.String())
	req.Body = ObserveBody(req.Body, st)
	return n, func(resp *http.Response, err error) {
		_ = st.Close()
		status := 0
		var h http.Header
		if resp != nil {
			status = resp.StatusCode
			h = resp.Header
			resp.Body = ObserveBody(resp.Body, s.NewStream("upstream_response", n, 0, h.Get("Content-Type"), h))
		}
		s.AttemptResponse(n, status, h, err)
		if err != nil {
			s.mu.Lock()
			if n > 0 && n <= len(s.attempts) {
				s.attempts[n-1].ErrorStage = diagnostics.stage()
			}
			s.mu.Unlock()
		}
	}
}

type accountContextKey struct{}

func WithAccount(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, accountContextKey{}, id)
}
func AccountFromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(accountContextKey{}).(int64)
	return v
}
