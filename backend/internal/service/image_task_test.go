package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageTaskMemoryStore struct {
	task    *ImageTaskRecord
	ttl     time.Duration
	saveErr error
	getErr  error
}

func (s *imageTaskMemoryStore) Save(_ context.Context, task *ImageTaskRecord, ttl time.Duration) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	copy := *task
	s.task = &copy
	s.ttl = ttl
	return nil
}

func (s *imageTaskMemoryStore) Get(_ context.Context, _ string) (*ImageTaskRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.task == nil {
		return nil, ErrImageTaskNotFound
	}
	copy := *s.task
	return &copy, nil
}

func TestImageTaskServiceLifecycleAndOwnership(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, 10*time.Minute)
	owner := ImageTaskOwner{UserID: 7, APIKeyID: 9}

	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusProcessing, created.Status)
	require.Equal(t, created.ID, created.TaskID)
	require.Equal(t, "image.generation.task", created.Object)
	require.Equal(t, time.Hour, store.ttl)
	require.Equal(t, owner.UserID, store.task.UserID)
	require.Equal(t, owner.APIKeyID, store.task.APIKeyID)

	_, err = svc.Get(context.Background(), ImageTaskOwner{UserID: 7, APIKeyID: 10}, created.ID)
	require.ErrorIs(t, err, ErrImageTaskNotFound)

	result := json.RawMessage(`{"created":123,"data":[{"url":"https://example.test/image.png"}]}`)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, result))

	completed, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, completed.Status)
	require.Equal(t, http.StatusOK, completed.HTTPStatus)
	require.Equal(t, "https://example.test/image.png", completed.ImageURL)
	require.JSONEq(t, string(result), string(completed.Result))
	require.NotNil(t, completed.CompletedAt)
}

func TestImageTaskServiceInvalidResultBecomesFailed(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	created, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.NoError(t, err)

	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, json.RawMessage(`not-json`)))
	got, err := svc.Get(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2}, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusFailed, got.Status)
	require.Equal(t, http.StatusBadGateway, got.HTTPStatus)
	require.Contains(t, string(got.Error), "non-JSON")
}

func TestImageTaskServiceMapsStoreFailures(t *testing.T) {
	store := &imageTaskMemoryStore{saveErr: errors.New("redis down")}
	svc := NewImageTaskService(store)

	_, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.ErrorIs(t, err, ErrImageTaskUnavailable)
}

func TestImageTaskServiceStagesBillingBeforePublishingResult(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: 7, APIKeyID: 9}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	result := json.RawMessage(`{"data":[{"url":"https://example.test/billed.png"}]}`)

	require.NoError(t, svc.StageBilling(context.Background(), created.ID, http.StatusOK, result))
	require.Equal(t, ImageTaskStatusBilling, store.task.Status)
	require.JSONEq(t, string(result), string(store.task.PendingResult), "private task keeps the pending result")
	require.Empty(t, store.task.Result, "legacy readers must not see unsettled results")
	require.Nil(t, store.task.CompletedAt)

	pending, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusBilling, pending.Status)
	require.Empty(t, pending.ImageURL)
	require.Empty(t, pending.Result)
	require.Nil(t, pending.CompletedAt)

	require.NoError(t, svc.PublishBilled(context.Background(), created.ID))
	completed, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, completed.Status)
	require.Equal(t, "https://example.test/billed.png", completed.ImageURL)
	require.JSONEq(t, string(result), string(completed.Result))
	require.Empty(t, store.task.PendingResult)
	require.NotNil(t, completed.CompletedAt)

	require.NoError(t, svc.PublishBilled(context.Background(), created.ID), "publishing an already completed task is idempotent")
}

func TestImageTaskServicePendingResultIsHiddenFromLegacyReader(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: 7, APIKeyID: 9}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	result := json.RawMessage(`{"data":[{"url":"https://example.test/unsettled.png"}]}`)
	require.NoError(t, svc.StageBilling(context.Background(), created.ID, http.StatusOK, result))

	stored, err := json.Marshal(store.task)
	require.NoError(t, err)
	// Version 331 decodes Result and publishes it without checking task status.
	var legacyRecord struct {
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(stored, &legacyRecord))
	require.Empty(t, legacyRecord.Result, "a 331 reader must never receive an unsettled result")

	public, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	body, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(body), "pending_result")
	require.NotContains(t, string(body), "unsettled.png")
}

func TestImageTaskServiceStageBillingSaveFailureKeepsProcessingForRetry(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	created, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 7, APIKeyID: 9})
	require.NoError(t, err)
	store.saveErr = errors.New("redis down")

	err = svc.StageBilling(context.Background(), created.ID, http.StatusOK, json.RawMessage(`{"data":[{"url":"https://example.test/retry.png"}]}`))

	require.ErrorIs(t, err, ErrImageTaskUnavailable)
	require.Equal(t, ImageTaskStatusProcessing, store.task.Status)
	require.Empty(t, store.task.Result)

	store.saveErr = nil
	require.NoError(t, svc.StageBilling(context.Background(), created.ID, http.StatusOK, json.RawMessage(`{"data":[{"url":"https://example.test/retry.png"}]}`)))
	require.Equal(t, ImageTaskStatusBilling, store.task.Status)
}

func TestImageTaskServicePublishSaveFailureKeepsBillingForRetry(t *testing.T) {
	store := &imageTaskMemoryStore{}
	svc := NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
	created, err := svc.Create(context.Background(), ImageTaskOwner{UserID: 7, APIKeyID: 9})
	require.NoError(t, err)
	require.NoError(t, svc.StageBilling(context.Background(), created.ID, http.StatusOK, json.RawMessage(`{"data":[{"url":"https://example.test/retry.png"}]}`)))
	store.saveErr = errors.New("redis down")

	err = svc.PublishBilled(context.Background(), created.ID)

	require.ErrorIs(t, err, ErrImageTaskUnavailable)
	require.Equal(t, ImageTaskStatusBilling, store.task.Status)
	require.Nil(t, store.task.CompletedAt)
}
