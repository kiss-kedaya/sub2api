package service

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/require"
)

func moderationLifecycleInput() (ContentModerationCheckInput, weak.Pointer[byte]) {
	body := []byte(`{"messages":[{"role":"user","content":"first user turn"},{"role":"assistant","content":"` + strings.Repeat("x", 4<<20) + `"},{"role":"user","content":"blocked prompt"}]}`)
	return ContentModerationCheckInput{
		RequestID: "lifecycle-test",
		UserID:    41,
		APIKeyID:  73,
		Protocol:  ContentModerationProtocolOpenAIChat,
		Body:      body,
	}, weak.Make(&body[0])
}

func requireModerationBodyCollected(t *testing.T, body weak.Pointer[byte]) {
	t.Helper()
	require.Eventually(t, func() bool {
		runtime.GC()
		return body.Value() == nil
	}, 2*time.Second, 10*time.Millisecond, "completed audit must not retain the original 4 MiB request body")
}

func TestContentModerationBuildLogReleasesRequestBody(t *testing.T) {
	log, body := func() (*ContentModerationLog, weak.Pointer[byte]) {
		input, body := moderationLifecycleInput()
		svc := &ContentModerationService{}
		log := svc.buildLog(input, defaultContentModerationConfig(), ContentModerationActionBlock, true, "sexual", 1, nil, "blocked prompt", nil, nil, "")
		return log, body
	}()
	defer runtime.KeepAlive(log)
	require.Equal(t, int64(41), *log.UserID)
	require.Equal(t, int64(73), *log.APIKeyID)
	require.Equal(t, "first user turn\n\nblocked prompt", log.InputExcerpt)
	requireModerationBodyCollected(t, body)
}

func TestContentModerationRecordQueueReleasesRequestBody(t *testing.T) {
	svc := NewContentModerationService(nil, nil, nil, nil, nil, nil, nil, nil)
	svc.repo = &contentModerationTestRepo{}
	svc.hashCache = &contentModerationTestHashCache{}
	svc.settingRepo = &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t, "blocked"),
	}}
	body := func() weak.Pointer[byte] {
		input, body := moderationLifecycleInput()
		decision, err := svc.Check(context.Background(), input)
		require.NoError(t, err)
		require.True(t, decision.Blocked)
		require.Equal(t, ContentModerationActionKeywordBlock, decision.Action)
		return body
	}()
	defer runtime.KeepAlive(svc)
	require.Len(t, svc.asyncQueue, 1)
	requireModerationBodyCollected(t, body)

	task, ok := svc.dequeueAsyncTask(context.Background(), time.Second)
	require.True(t, ok)
	require.Equal(t, 0, len(task.input.Body))
	require.Equal(t, "first user turn\n\nblocked prompt", task.log.InputExcerpt)
	require.Equal(t, int64(41), *task.log.UserID)
	require.Equal(t, int64(73), *task.log.APIKeyID)
	require.Equal(t, "blocked", task.log.MatchedKeyword)
	require.True(t, task.applySideEffects)
	require.NotNil(t, task.config)
	svc.persistContentModerationLog(context.Background(), task.config, task.log, task.inputHash, task.recordHash, task.applySideEffects)
	logs := svc.repo.(*contentModerationTestRepo).snapshotLogs()
	require.Len(t, logs, 1)
	require.Equal(t, "lifecycle-test", logs[0].RequestID)
	require.Equal(t, task.log.InputExcerpt, logs[0].InputExcerpt)
}

func BenchmarkContentModerationIdleQueue(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		svc := NewContentModerationService(nil, nil, nil, nil, nil, nil, nil, nil)
		runtime.KeepAlive(svc)
	}
}

func BenchmarkContentModerationQueueRoundTrip(b *testing.B) {
	svc := NewContentModerationService(nil, nil, nil, nil, nil, nil, nil, nil)
	cfg := defaultContentModerationConfig()
	input := ContentModerationCheckInput{RequestID: "bench", UserID: 41}
	content := ContentModerationInput{Text: "test prompt"}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		svc.enqueueAsync(input, cfg, content, "hash")
		if _, ok := svc.dequeueAsyncTask(ctx, time.Second); !ok {
			b.Fatal("enqueued task missing")
		}
	}
}
