//go:build unit

package service

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type silenceReadBoundary struct {
	returned chan struct{}
	err      error
}

func (b *silenceReadBoundary) Read(p []byte) (int, error) {
	if b.err != nil {
		close(b.returned)
		return 0, b.err
	}
	const line = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"
	n := copy(p, line)
	close(b.returned)
	return n, nil
}
func (*silenceReadBoundary) Close() error { return nil }

func TestDeepSeekStreamSilenceReviewReadBeforeTimerLock(t *testing.T) {
	shortenDeepSeekStreamSilence(t, time.Hour)
	for _, eof := range []bool{false, true} {
		t.Run(map[bool]string{false: "output", true: "EOF"}[eof], func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			source := &silenceReadBoundary{returned: make(chan struct{})}
			if eof {
				source.err = io.EOF
			}
			b := newOpenAIStreamSilenceBody(ctx, cancel, source, nil, true)
			t.Cleanup(func() { require.NoError(t, b.Close()) })
			b.timer.Stop()
			b.mu.Lock()
			b.progress.Store(&openAIStreamSilenceProgress{lastOutput: time.Now().Add(-time.Hour)})
			b.limit = time.Minute
			// Queue expire first; a returned Read must publish activity even while
			// waiting for the same mutex behind that timer callback.
			expired := make(chan struct{})
			go func() { b.expire(); close(expired) }()
			readDone := make(chan error, 1)
			go func() {
				scanner := (&OpenAIGatewayService{}).newUpstreamSSEScanner(b)
				if scanner.Scan() {
					scanner.Scan()
				}
				readDone <- scanner.Err()
			}()
			<-source.returned
			require.Eventually(t, func() bool {
				progress := b.progress.Load()
				return progress != nil && (progress.finished || time.Since(progress.lastOutput) < time.Second)
			}, time.Second, time.Millisecond)
			b.mu.Unlock()
			<-expired
			err := <-readDone
			require.NoError(t, err) // Scanner consumes ordinary EOF.
			require.NotErrorIs(t, context.Cause(ctx), errOpenAIStreamSilenceLimit)
		})
	}
}

type silenceWaitForReadCloser struct {
	closed   chan struct{}
	readDone chan struct{}
	once     sync.Once
}

func (b *silenceWaitForReadCloser) Read([]byte) (int, error) { <-b.closed; return 0, io.ErrClosedPipe }
func (b *silenceWaitForReadCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	<-b.readDone
	return nil
}

func TestDeepSeekStreamSilenceReviewCloseOutsideStateLock(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 40*time.Millisecond)
	ctx, cancel := context.WithCancelCause(context.Background())
	source := &silenceWaitForReadCloser{closed: make(chan struct{}), readDone: make(chan struct{})}
	b := newOpenAIStreamSilenceBody(ctx, cancel, source, nil, true)
	readDone := make(chan error, 1)
	go func() { _, err := b.Read(make([]byte, 8)); close(source.readDone); readDone <- err }()
	select {
	case err := <-readDone:
		require.ErrorIs(t, err, errOpenAIStreamSilenceLimit)
	case <-time.After(3 * time.Second):
		t.Fatal("expire held the state mutex while waiting for Read to return")
	}
	require.NoError(t, b.Close())
}
