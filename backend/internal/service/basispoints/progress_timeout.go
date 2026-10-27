package basispoints

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type streamProgressTimeoutError struct{}

func (streamProgressTimeoutError) Error() string {
	return "basispoints upstream stream made no progress before the deadline"
}
func (streamProgressTimeoutError) Timeout() bool   { return true }
func (streamProgressTimeoutError) Temporary() bool { return false }

var ErrStreamProgressTimeout error = streamProgressTimeoutError{}

type progressTimeoutBody struct {
	upstream           io.ReadCloser
	ctx                context.Context
	idle, remaining    time.Duration
	closeOnce          sync.Once
	closeErr           error
	timedOut           atomic.Bool
	lineStart, comment bool
}

// WithProgressTimeout monitors the original upstream body, including correction
// and compaction reads. Downstream buffering is outside Read and does not spend
// the wait budget. SSE comments/blank lines do not replenish it; fragmented data
// does. No content is retained and no accepted request is replayed.
func WithProgressTimeout(ctx context.Context, upstream io.ReadCloser, idle time.Duration) io.ReadCloser {
	if upstream == nil || idle <= 0 {
		return upstream
	}
	return &progressTimeoutBody{ctx: ctx, upstream: upstream, idle: idle, remaining: idle, lineStart: true}
}

func (b *progressTimeoutBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.upstream.Close() })
	return b.closeErr
}

func (b *progressTimeoutBody) dataProgress(p []byte) bool {
	progress := false
	for _, c := range p {
		if c == '\n' {
			b.lineStart = true
			b.comment = false
			continue
		}
		if c == '\r' {
			continue
		}
		if b.lineStart {
			b.comment = c == ':'
			b.lineStart = false
		}
		if !b.comment {
			progress = true
		}
	}
	return progress
}

// The body has one reader, like the wrapped HTTP response. Close may be concurrent.
func (b *progressTimeoutBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.timedOut.Load() {
		return 0, ErrStreamProgressTimeout
	}
	started := time.Now()
	timer := time.AfterFunc(b.remaining, func() { b.timedOut.Store(true); _ = b.Close() })
	stopCancel := context.AfterFunc(b.ctx, func() { _ = b.Close() })
	n, err := b.upstream.Read(p)
	timer.Stop()
	stopCancel()
	if contextErr := b.ctx.Err(); contextErr != nil {
		return n, contextErr
	}
	if b.timedOut.Load() {
		return n, ErrStreamProgressTimeout
	}
	if b.dataProgress(p[:n]) {
		b.remaining = b.idle
	} else {
		b.remaining -= time.Since(started)
	}
	if b.remaining <= 0 && err == nil {
		b.timedOut.Store(true)
		_ = b.Close()
		return n, ErrStreamProgressTimeout
	}
	return n, err
}
